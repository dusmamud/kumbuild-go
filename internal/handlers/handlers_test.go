package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-chi/chi/v5"
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

func setupTestApp() (http.Handler, store.Store, *mockEmailSender) {
	cfg := &config.Config{
		Port:               "8080",
		DatabaseURL:        "memory",
		BrevoAPIKey:        "",
		MailFrom:           "hello@kumbuild.com",
		OwnerEmail:         "owner@kumbuild.com",
		AdminToken:         "test_admin_secret",
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
	req3.Header.Set("X-Admin-Token", "test_admin_secret")
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
	req1.Header.Set("X-Admin-Token", "test_admin_secret")
	rec1 := httptest.NewRecorder()
	app.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status on PATCH, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Valid status
	patchValid, _ := json.Marshal(map[string]string{"status": "advance_paid"})
	req2 := httptest.NewRequest(http.MethodPatch, "/api/admin/orders/1", bytes.NewReader(patchValid))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Admin-Token", "test_admin_secret")
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
	req1.Header.Set("X-Admin-Token", "test_admin_secret")
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
	req2.Header.Set("X-Admin-Token", "test_admin_secret")
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
