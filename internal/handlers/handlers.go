package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"kumbuild-go/internal/auth"
	"kumbuild-go/internal/config"
	"kumbuild-go/internal/email"
	"kumbuild-go/internal/middleware"
	"kumbuild-go/internal/models"
	"kumbuild-go/internal/store"
	"kumbuild-go/internal/turnstile"
)

type Handler struct {
	cfg       *config.Config
	store     store.Store
	email     email.Sender
	turnstile turnstile.Verifier
	limiter   *auth.RateLimiter
}

func NewHandler(cfg *config.Config, s store.Store, e email.Sender, t turnstile.Verifier) *Handler {
	return &Handler{
		cfg:       cfg,
		store:     s,
		email:     e,
		turnstile: t,
		limiter:   auth.NewRateLimiter(5, 15*time.Minute),
	}
}

func RegisterRoutes(r chi.Router, h *Handler) {
	// CORS middleware
	r.Use(middleware.CORS(h.cfg.CORSOrigins))

	// Health check
	r.Get("/healthz", h.Healthz)

	// Public order endpoint
	r.Post("/api/orders", h.CreateOrder)

	// Public login endpoint
	r.Post("/api/admin/login", h.AdminLogin)

	// Admin protected endpoints (Accepts Bearer JWT OR X-Admin-Token)
	r.Route("/api/admin", func(admin chi.Router) {
		admin.Use(middleware.AdminAuth(h.cfg.AdminToken, h.cfg.JWTSecret, h.cfg.AdminEmail))

		admin.Get("/me", h.AdminMe)
		admin.Get("/orders", h.ListOrders)
		admin.Get("/orders/{id}", h.GetOrder)
		admin.Patch("/orders/{id}", h.UpdateOrderStatus)

		admin.Get("/customers", h.GetCustomers)

		admin.Post("/campaigns", h.CreateCampaign)
		admin.Get("/campaigns", h.ListCampaigns)
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Healthz responds to /healthz
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func getClientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[0])
	}
	parts := strings.Split(r.RemoteAddr, ":")
	if len(parts) > 0 {
		return parts[0]
	}
	return r.RemoteAddr
}

// AdminLogin handles POST /api/admin/login
func (h *Handler) AdminLogin(w http.ResponseWriter, r *http.Request) {
	ip := getClientIP(r)
	if !h.limiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts — try again in a few minutes")
		return
	}

	var req models.AdminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	emailMatch := strings.EqualFold(strings.TrimSpace(req.Email), strings.TrimSpace(h.cfg.AdminEmail))
	pwErr := bcrypt.CompareHashAndPassword([]byte(h.cfg.AdminPasswordHash), []byte(req.Password))

	if !emailMatch || pwErr != nil {
		h.limiter.RecordFailed(ip)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Login successful: reset failed counter
	h.limiter.Reset(ip)

	token, err := auth.GenerateJWT(h.cfg.AdminEmail, h.cfg.JWTSecret)
	if err != nil {
		log.Printf("[ERROR] Failed to generate JWT: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to generate session")
		return
	}

	writeJSON(w, http.StatusOK, models.AdminLoginResponse{
		Token: token,
		Email: h.cfg.AdminEmail,
	})
}

// AdminMe handles GET /api/admin/me
func (h *Handler) AdminMe(w http.ResponseWriter, r *http.Request) {
	email, _ := r.Context().Value(middleware.AuthEmailKey).(string)
	if email == "" {
		email = h.cfg.AdminEmail
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

// CreateOrder handles POST /api/orders
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	var req models.CreateOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	normalizedPhone, ok := models.NormalizePhone(req.Phone)
	if !ok {
		writeError(w, http.StatusBadRequest, "valid phone number with at least 10 digits is required")
		return
	}

	pkg := strings.TrimSpace(req.Package)
	if pkg != "" && !models.IsValidPackage(pkg) {
		writeError(w, http.StatusBadRequest, "invalid package: must be starter, business, or premium")
		return
	}

	bType := strings.TrimSpace(req.BusinessType)
	if bType != "" && !models.IsValidBusinessType(bType) {
		writeError(w, http.StatusBadRequest, "invalid businessType")
		return
	}

	// Verify Cloudflare Turnstile
	clientIP := getClientIP(r)
	validTurnstile, err := h.turnstile.Verify(r.Context(), req.TurnstileToken, clientIP)
	if err != nil || !validTurnstile {
		log.Printf("[WARN] Turnstile verification failed: %v", err)
		writeError(w, http.StatusBadRequest, "turnstile verification failed")
		return
	}

	whatsapp := strings.TrimSpace(req.WhatsApp)
	if whatsapp == "" {
		whatsapp = normalizedPhone
	} else {
		if normWA, waOk := models.NormalizePhone(whatsapp); waOk {
			whatsapp = normWA
		}
	}

	var emailPtr *string
	if req.Email != nil {
		trimmedEmail := strings.TrimSpace(*req.Email)
		if trimmedEmail != "" {
			emailPtr = &trimmedEmail
		}
	}

	order := &models.Order{
		Name:            name,
		BusinessName:    strings.TrimSpace(req.BusinessName),
		BusinessType:    bType,
		Package:         pkg,
		Phone:           normalizedPhone,
		WhatsApp:        whatsapp,
		Email:           emailPtr,
		ContentLanguage: strings.TrimSpace(req.ContentLanguage),
		Message:         strings.TrimSpace(req.Message),
		Status:          "new",
	}

	const maxRetries = 3
	var orderCreated bool
	var ref string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ref = models.GenerateReference()
		order.Reference = ref

		err := h.store.CreateOrder(r.Context(), order)
		if err == nil {
			orderCreated = true
			break
		}

		if errors.Is(err, store.ErrDuplicateReference) {
			log.Printf("[WARN] Order reference collision on %s, retrying (attempt %d/%d)...", ref, attempt, maxRetries)
			continue
		}

		log.Printf("[ERROR] Failed to insert order: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create order")
		return
	}

	if !orderCreated {
		log.Printf("[ERROR] Failed to generate unique order reference after %d attempts", maxRetries)
		writeError(w, http.StatusInternalServerError, "failed to create order")
		return
	}

	// Async email dispatch — an email failure must NEVER fail the order request
	go func(o models.Order) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ERROR] Recovered from email panic: %v", r)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		if h.cfg.OwnerEmail != "" {
			if err := h.email.SendOwnerAlert(ctx, h.cfg.OwnerEmail, &o); err != nil {
				log.Printf("[ERROR] Failed to send owner alert email for order %s: %v", o.Reference, err)
			} else {
				log.Printf("[INFO] Sent owner alert email for order %s", o.Reference)
			}
		}

		if o.Email != nil && *o.Email != "" {
			if err := h.email.SendCustomerConfirmation(ctx, &o); err != nil {
				log.Printf("[ERROR] Failed to send customer confirmation email for order %s: %v", o.Reference, err)
			} else {
				log.Printf("[INFO] Sent customer confirmation email for order %s", o.Reference)
			}
		}
	}(*order)

	writeJSON(w, http.StatusCreated, map[string]string{"reference": ref})
}

// ListOrders handles GET /api/admin/orders
func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	filter := store.OrderFilter{
		Status: status,
		Search: q,
		Page:   page,
		Limit:  limit,
	}

	orders, total, err := h.store.ListOrders(r.Context(), filter)
	if err != nil {
		log.Printf("[ERROR] Failed to list orders: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to query orders")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": orders,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// GetOrder handles GET /api/admin/orders/{id}
func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order ID")
		return
	}

	order, err := h.store.GetOrderByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get order %d: %v", id, err)
		writeError(w, http.StatusInternalServerError, "failed to get order")
		return
	}
	if order == nil {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}

	writeJSON(w, http.StatusOK, order)
}

// UpdateOrderStatus handles PATCH /api/admin/orders/{id}
func (h *Handler) UpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order ID")
		return
	}

	var req models.UpdateOrderStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	status := strings.TrimSpace(req.Status)
	if !models.IsValidStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}

	order, err := h.store.UpdateOrderStatus(r.Context(), id, status)
	if err != nil {
		log.Printf("[ERROR] Failed to update order status %d: %v", id, err)
		writeError(w, http.StatusInternalServerError, "failed to update order status")
		return
	}
	if order == nil {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}

	writeJSON(w, http.StatusOK, order)
}

// GetCustomers handles GET /api/admin/customers
func (h *Handler) GetCustomers(w http.ResponseWriter, r *http.Request) {
	customers, err := h.store.GetCustomers(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to get customers: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get customers")
		return
	}

	writeJSON(w, http.StatusOK, customers)
}

// CreateCampaign handles POST /api/admin/campaigns
// Asynchronous: validates recipients, creates campaign record immediately, returns 201, and sends in background.
func (h *Handler) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	var req models.CreateCampaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}

	htmlContent := strings.TrimSpace(req.HTML)
	if htmlContent == "" {
		writeError(w, http.StatusBadRequest, "html content is required")
		return
	}

	var emailList []string
	switch v := req.Recipients.(type) {
	case string:
		if strings.ToLower(strings.TrimSpace(v)) == "all" {
			emails, err := h.store.GetAllCustomerEmails(r.Context())
			if err != nil {
				log.Printf("[ERROR] Failed to get customer emails: %v", err)
				writeError(w, http.StatusInternalServerError, "failed to fetch recipient emails")
				return
			}
			emailList = emails
		} else if strings.TrimSpace(v) != "" {
			emailList = []string{strings.TrimSpace(v)}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					emailList = append(emailList, t)
				}
			}
		}
	case []string:
		for _, s := range v {
			if t := strings.TrimSpace(s); t != "" {
				emailList = append(emailList, t)
			}
		}
	}

	// Deduplicate emails
	uniqueEmails := make([]string, 0, len(emailList))
	seen := make(map[string]bool)
	for _, em := range emailList {
		lower := strings.ToLower(em)
		if !seen[lower] {
			seen[lower] = true
			uniqueEmails = append(uniqueEmails, em)
		}
	}

	if len(uniqueEmails) == 0 {
		writeError(w, http.StatusBadRequest, "no recipients found")
		return
	}

	camp := &models.Campaign{
		Subject:         subject,
		BodyHTML:        htmlContent,
		RecipientsCount: len(uniqueEmails),
		SentCount:       0,
		FailedCount:     0,
		Status:          "sending",
		Detail:          "Queued for delivery",
	}

	if err := h.store.CreateCampaign(r.Context(), camp); err != nil {
		log.Printf("[ERROR] Failed to save campaign record: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create campaign")
		return
	}

	// Return 201 immediately with the created campaign record
	writeJSON(w, http.StatusCreated, camp)

	// Async send in background goroutine
	go func(campID int64, emails []string, subj, html string) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[ERROR] Recovered in background campaign worker: %v", rec)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		sent := 0
		failed := 0

		for _, recipient := range emails {
			if err := h.email.Send(ctx, recipient, subj, html); err != nil {
				failed++
				log.Printf("[WARN] Failed sending campaign %d email to %s: %v", campID, recipient, err)
			} else {
				sent++
			}
			_ = h.store.UpdateCampaignCounts(ctx, campID, sent, failed)
		}
		log.Printf("[INFO] Finished background campaign %d: %d sent, %d failed", campID, sent, failed)
	}(camp.ID, uniqueEmails, subject, htmlContent)
}

// ListCampaigns handles GET /api/admin/campaigns
func (h *Handler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
	campaigns, err := h.store.ListCampaigns(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to list campaigns: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get campaigns")
		return
	}

	writeJSON(w, http.StatusOK, campaigns)
}
