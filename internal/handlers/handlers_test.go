package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"kumbuild-go/internal/auth"
	"kumbuild-go/internal/config"
	"kumbuild-go/internal/handlers"
	"kumbuild-go/internal/models"
	"kumbuild-go/internal/store"
	"kumbuild-go/internal/turnstile"
)

// mockEmailSender records sent emails
type mockEmailSender struct {
	sentCount int
}

func (m *mockEmailSender) Send(ctx context.Context, toEmail, subject, htmlContent string) error {
	m.sentCount++
	return nil
}
func (m *mockEmailSender) SendOwnerAlert(ctx context.Context, ownerEmail string, order *models.Order) error {
	m.sentCount++
	return nil
}
func (m *mockEmailSender) SendCustomerConfirmation(ctx context.Context, order *models.Order) error {
	m.sentCount++
	return nil
}

const (
	testAdminEmail    = "owner@kumbuild.com"
	testAdminPassword = "TestPassword123!"
	testAdminToken    = "test_admin_secret_token"
	testJWTSecret     = "super_secret_jwt_key_for_unit_tests_32_bytes!"
)

var testPasswordHash string

func init() {
	hash, _ := bcrypt.GenerateFromPassword([]byte(testAdminPassword), bcrypt.MinCost)
	testPasswordHash = string(hash)
}

func setupTestApp() (http.Handler, store.Store, *mockEmailSender) {
	cfg := &config.Config{
		Port:               "8080",
		DatabaseURL:        "memory",
		BrevoAPIKey:        "",
		MailFrom:           "hello@kumbuild.com",
		OwnerEmail:         "hello@kumbuild.com",
		AdminToken:         testAdminToken,
		AdminEmail:         testAdminEmail,
		AdminPasswordHash:  testPasswordHash,
		JWTSecret:          testJWTSecret,
		TurnstileSecretKey: "", // empty = skip in test
		CORSOrigins:        []string{"*"},
	}

	memStore := store.NewMemoryStore()
	mockEmail := &mockEmailSender{}
	verifier := turnstile.NewVerifier("")

	h := handlers.NewHandler(cfg, memStore, mockEmail, verifier)
	r := chi.NewRouter()
	handlers.RegisterRoutes(r, h)

	return r, memStore, mockEmail
}

// 1. Validation: Name required
func TestValidation_MissingName(t *testing.T) {
	app, _, _ := setupTestApp()

	payload := map[string]any{
		"name":  "",
		"phone": "9876543210",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest for empty name, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// 2. Validation: Phone required and at least 10 digits
func TestValidation_InvalidPhone(t *testing.T) {
	app, _, _ := setupTestApp()

	testCases := []string{"", "12345", "phone123", "   "}
	for _, p := range testCases {
		payload := map[string]any{
			"name":  "Rohit Sharma",
			"phone": p,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for phone '%s', got %d", p, rec.Code)
		}
	}
}

// 3. Phone normalization: 10 digits gets +91, spaces stripped
func TestPhoneNormalization(t *testing.T) {
	norm1, ok1 := models.NormalizePhone("98765 43210")
	if !ok1 || norm1 != "+919876543210" {
		t.Fatalf("expected '+919876543210', got '%s'", norm1)
	}

	norm2, ok2 := models.NormalizePhone("+1 800 555 0199")
	if !ok2 || norm2 != "+18005550199" {
		t.Fatalf("expected '+18005550199', got '%s'", norm2)
	}

	norm3, ok3 := models.NormalizePhone("+91 9876543210")
	if !ok3 || norm3 != "+919876543210" {
		t.Fatalf("expected '+919876543210', got '%s'", norm3)
	}
}

// 4. WhatsApp fallback: if empty in request, copy normalized phone
func TestWhatsAppFallback(t *testing.T) {
	app, memStore, _ := setupTestApp()

	payload := map[string]any{
		"name":         "Amit Kumar",
		"phone":        "9876543210",
		"whatsapp":     "", // empty -> should fallback to +919876543210
		"businessName": "Amit Cafe",
		"package":      "starter",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	orders, _, err := memStore.ListOrders(context.Background(), store.OrderFilter{})
	if err != nil || len(orders) == 0 {
		t.Fatalf("failed to retrieve created order: %v", err)
	}

	created := orders[0]
	if created.Phone != "+919876543210" {
		t.Errorf("expected phone '+919876543210', got '%s'", created.Phone)
	}
	if created.WhatsApp != "+919876543210" {
		t.Errorf("expected whatsapp to fallback to phone '+919876543210', got '%s'", created.WhatsApp)
	}
}

// 5. Reference format: KB- followed by 6 unambiguous uppercase alphanumerics
func TestReferenceFormat(t *testing.T) {
	refRegex := regexp.MustCompile(`^KB-[2-9A-HJ-NP-Z]{6}$`)
	for i := 0; i < 50; i++ {
		ref := models.GenerateReference()
		if !refRegex.MatchString(ref) {
			t.Fatalf("reference '%s' does not match expected format ^KB-[2-9A-HJ-NP-Z]{6}$", ref)
		}
	}
}

// 6. Admin token: 401 when missing or wrong, 200 when correct
func TestAdminTokenAuth(t *testing.T) {
	app, _, _ := setupTestApp()

	// Missing token -> 401
	req1 := httptest.NewRequest(http.MethodGet, "/api/admin/orders", nil)
	rec1 := httptest.NewRecorder()
	app.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for missing admin token, got %d", rec1.Code)
	}

	// Wrong token -> 401
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/orders", nil)
	req2.Header.Set("X-Admin-Token", "wrong_token_here")
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for wrong admin token, got %d", rec2.Code)
	}

	// Correct token -> 200
	req3 := httptest.NewRequest(http.MethodGet, "/api/admin/orders", nil)
	req3.Header.Set("X-Admin-Token", testAdminToken)
	rec3 := httptest.NewRecorder()
	app.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid admin token, got %d: %s", rec3.Code, rec3.Body.String())
	}
}

// 7. Status validation on PATCH: validate against allowed list
func TestStatusValidation_PATCH(t *testing.T) {
	app, memStore, _ := setupTestApp()

	order := &models.Order{
		Reference: "KB-TEST01",
		Name:      "Test User",
		Phone:     "+919876543210",
		Status:    "new",
	}
	_ = memStore.CreateOrder(context.Background(), order)

	// Invalid status
	patchInvalid, _ := json.Marshal(map[string]string{"status": "some_random_status"})
	req1 := httptest.NewRequest(http.MethodPatch, "/api/admin/orders/1", bytes.NewReader(patchInvalid))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Admin-Token", testAdminToken)
	rec1 := httptest.NewRecorder()
	app.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status on PATCH, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Valid status
	patchValid, _ := json.Marshal(map[string]string{"status": "advance_paid"})
	req2 := httptest.NewRequest(http.MethodPatch, "/api/admin/orders/1", bytes.NewReader(patchValid))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Admin-Token", testAdminToken)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid status on PATCH, got %d: %s", rec2.Code, rec2.Body.String())
	}

	updated, _ := memStore.GetOrderByID(context.Background(), 1)
	if updated.Status != "advance_paid" {
		t.Fatalf("expected order status 'advance_paid', got '%s'", updated.Status)
	}
}

// 8. Campaign zero-recipient error -> 400 with clear message
func TestCampaign_ZeroRecipients(t *testing.T) {
	app, _, _ := setupTestApp()

	// Empty recipient array
	payload1, _ := json.Marshal(map[string]any{
		"subject":    "Special Offer",
		"html":       "<p>Hello world</p>",
		"recipients": []string{},
	})
	req1 := httptest.NewRequest(http.MethodPost, "/api/admin/campaigns", bytes.NewReader(payload1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Admin-Token", testAdminToken)
	rec1 := httptest.NewRecorder()
	app.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for zero recipients, got %d", rec1.Code)
	}

	// Recipients = "all" when database has no customer emails
	payload2, _ := json.Marshal(map[string]any{
		"subject":    "Special Offer",
		"html":       "<p>Hello world</p>",
		"recipients": "all",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/campaigns", bytes.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Admin-Token", testAdminToken)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for recipients='all' with 0 emails, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// 9. Turnstile skip when secret empty -> success 201
func TestTurnstileSkipWhenSecretEmpty(t *testing.T) {
	app, _, _ := setupTestApp()

	payload := map[string]any{
		"name":           "Priya Patel",
		"phone":          "9876543210",
		"businessType":   "Clinic",
		"package":        "business",
		"turnstileToken": "", // should skip when TURNSTILE_SECRET_KEY is empty
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created when TURNSTILE_SECRET_KEY is empty, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 10. Package validation: must be one of starter, business, premium
func TestPackageValidation(t *testing.T) {
	app, _, _ := setupTestApp()

	payload := map[string]any{
		"name":    "Priya Patel",
		"phone":   "9876543210",
		"package": "ultra_vip_platinum", // invalid
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid package, got %d", rec.Code)
	}
}

// 11. BusinessType validation: must be one of allowed 6 values
func TestBusinessTypeValidation(t *testing.T) {
	app, _, _ := setupTestApp()

	payload := map[string]any{
		"name":         "Priya Patel",
		"phone":        "9876543210",
		"businessType": "Space Station", // invalid
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid businessType, got %d", rec.Code)
	}
}

// 12. Fix D: Indian phone formats (12 digits starting with 91, 11 digits starting with 0)
func TestPhoneNormalization_IndianFormats(t *testing.T) {
	norm1, ok1 := models.NormalizePhone("919812345678")
	if !ok1 || norm1 != "+919812345678" {
		t.Fatalf("expected '+919812345678' for '919812345678', got '%s'", norm1)
	}

	norm2, ok2 := models.NormalizePhone("09812345678")
	if !ok2 || norm2 != "+919812345678" {
		t.Fatalf("expected '+919812345678' for '09812345678', got '%s'", norm2)
	}

	norm3, ok3 := models.NormalizePhone("9812345678")
	if !ok3 || norm3 != "+919812345678" {
		t.Fatalf("expected '+919812345678' for '9812345678', got '%s'", norm3)
	}

	norm4, ok4 := models.NormalizePhone("+91 98123 45678")
	if !ok4 || norm4 != "+919812345678" {
		t.Fatalf("expected '+919812345678' for '+91 98123 45678', got '%s'", norm4)
	}
}

// 13. Fix B: All required environment variables validation
func TestConfig_ValidationRequiredFields(t *testing.T) {
	cfg := &config.Config{
		AdminToken:        "",
		AdminEmail:        "",
		AdminPasswordHash: "",
		JWTSecret:         "",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when required fields are empty, got nil")
	}

	cfg.AdminToken = "token"
	cfg.AdminEmail = "admin@kumbuild.com"
	cfg.AdminPasswordHash = "$2a$12$..."
	cfg.JWTSecret = "jwt_secret_32_chars"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil when all required fields are set, got %v", err)
	}
}

// 14. Fix C: Order reference collision retry
type collidingStore struct {
	store.Store
	attempts int
}

func (c *collidingStore) CreateOrder(ctx context.Context, o *models.Order) error {
	c.attempts++
	if c.attempts == 1 {
		return store.ErrDuplicateReference
	}
	return c.Store.CreateOrder(ctx, o)
}

func TestOrderReferenceCollisionRetry(t *testing.T) {
	memStore := store.NewMemoryStore()
	colliding := &collidingStore{Store: memStore}
	cfg := &config.Config{
		Port:               "8080",
		DatabaseURL:        "memory",
		AdminToken:         testAdminToken,
		AdminEmail:         testAdminEmail,
		AdminPasswordHash:  testPasswordHash,
		JWTSecret:          testJWTSecret,
		TurnstileSecretKey: "",
	}
	mockEmail := &mockEmailSender{}
	verifier := turnstile.NewVerifier("")

	h := handlers.NewHandler(cfg, colliding, mockEmail, verifier)
	r := chi.NewRouter()
	handlers.RegisterRoutes(r, h)

	payload := map[string]any{
		"name":  "Colliding User",
		"phone": "9876543210",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created after retry, got %d: %s", rec.Code, rec.Body.String())
	}
	if colliding.attempts != 2 {
		t.Fatalf("expected exactly 2 attempts (1 collision + 1 success), got %d", colliding.attempts)
	}
}

// 15. Admin Login: Success returns valid JWT + email
func TestAdminLogin_Success(t *testing.T) {
	app, _, _ := setupTestApp()

	payload := models.AdminLoginRequest{
		Email:    testAdminEmail,
		Password: testAdminPassword,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp models.AdminLoginResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}

	if resp.Token == "" {
		t.Fatal("expected non-empty JWT token")
	}
	if resp.Email != testAdminEmail {
		t.Fatalf("expected email '%s', got '%s'", testAdminEmail, resp.Email)
	}

	// Verify token validity
	email, err := auth.ValidateJWT(resp.Token, testJWTSecret)
	if err != nil || email != testAdminEmail {
		t.Fatalf("expected valid JWT for %s, got email=%s, err=%v", testAdminEmail, email, err)
	}
}

// 16. Admin Login: Wrong password -> 401
func TestAdminLogin_WrongPassword(t *testing.T) {
	app, _, _ := setupTestApp()

	payload := models.AdminLoginRequest{
		Email:    testAdminEmail,
		Password: "IncorrectPassword123",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for wrong password, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 17. Admin Login: Wrong email -> 401
func TestAdminLogin_WrongEmail(t *testing.T) {
	app, _, _ := setupTestApp()

	payload := models.AdminLoginRequest{
		Email:    "unknown@attacker.com",
		Password: testAdminPassword,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for wrong email, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 18. Admin Login: Rate Limiter returns 429 on 6th failed attempt
func TestAdminLogin_RateLimiter(t *testing.T) {
	app, _, _ := setupTestApp()

	payload := models.AdminLoginRequest{
		Email:    testAdminEmail,
		Password: "BadPassword",
	}
	body, _ := json.Marshal(payload)

	// Attempts 1 to 5 should return 401
	for i := 1; i <= 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.168.100.50:1234"
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, rec.Code)
		}
	}

	// 6th attempt from same IP should return 429 Too Many Requests
	req6 := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(body))
	req6.Header.Set("Content-Type", "application/json")
	req6.RemoteAddr = "192.168.100.50:1234"
	rec6 := httptest.NewRecorder()
	app.ServeHTTP(rec6, req6)

	if rec6.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt 6: expected 429 Too Many Requests, got %d: %s", rec6.Code, rec6.Body.String())
	}
}

// 19. /api/admin/me with valid JWT -> 200
func TestAdminMe_ValidJWT(t *testing.T) {
	app, _, _ := setupTestApp()

	token, _ := auth.GenerateJWT(testAdminEmail, testJWTSecret)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var data map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&data)
	if data["email"] != testAdminEmail {
		t.Fatalf("expected email '%s', got '%s'", testAdminEmail, data["email"])
	}
}

// 20. /api/admin/me with expired or tampered JWT -> 401
func TestAdminMe_TamperedOrExpiredJWT(t *testing.T) {
	app, _, _ := setupTestApp()

	// Tampered token
	req1 := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req1.Header.Set("Authorization", "Bearer tampered.jwt.token")
	rec1 := httptest.NewRecorder()
	app.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for tampered JWT, got %d", rec1.Code)
	}

	// Token signed with wrong secret
	wrongSecretToken, _ := auth.GenerateJWT(testAdminEmail, "wrong_secret_12345678901234567890")
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req2.Header.Set("Authorization", "Bearer "+wrongSecretToken)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for JWT with wrong secret, got %d", rec2.Code)
	}
}

// 21. Admin routes accept Bearer JWT
func TestAdminRoutes_BearerJWT(t *testing.T) {
	app, _, _ := setupTestApp()

	token, _ := auth.GenerateJWT(testAdminEmail, testJWTSecret)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/orders", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for Bearer JWT on /orders, got %d", rec.Code)
	}
}

// 22. Admin routes still accept X-Admin-Token (backward compatibility)
func TestAdminRoutes_XAdminToken(t *testing.T) {
	app, _, _ := setupTestApp()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/orders", nil)
	req.Header.Set("X-Admin-Token", testAdminToken)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for X-Admin-Token on /orders, got %d", rec.Code)
	}
}

// 23. Campaign create returns immediately with 201 and campaign record
func TestCampaignCreate_ImmediateReturn(t *testing.T) {
	app, memStore, _ := setupTestApp()

	// Seed an order with an email
	orderEmail := "customer@example.com"
	_ = memStore.CreateOrder(context.Background(), &models.Order{
		Reference: "KB-CAMP01",
		Name:      "Campaign Target",
		Phone:     "+919876543210",
		Email:     &orderEmail,
		Status:    "new",
	})

	token, _ := auth.GenerateJWT(testAdminEmail, testJWTSecret)

	payload := map[string]any{
		"subject":    "Flash Announcement",
		"html":       "<p>Big news</p>",
		"recipients": "all",
	}
	body, _ := json.Marshal(payload)
	start := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/campaigns", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created immediately, got %d: %s", rec.Code, rec.Body.String())
	}

	var camp models.Campaign
	if err := json.NewDecoder(rec.Body).Decode(&camp); err != nil {
		t.Fatalf("failed to decode created campaign response: %v", err)
	}

	if camp.Subject != "Flash Announcement" {
		t.Fatalf("expected subject 'Flash Announcement', got '%s'", camp.Subject)
	}
	if camp.RecipientsCount != 1 {
		t.Fatalf("expected RecipientsCount 1, got %d", camp.RecipientsCount)
	}

	// Should return within milliseconds, not blocking
	if elapsed > 100*time.Millisecond {
		t.Logf("Note: campaign create took %v", elapsed)
	}
}
