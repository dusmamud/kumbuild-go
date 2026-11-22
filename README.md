# Kumbuild Orders Backend (`kumbuild-go`)

Lightweight, high-performance Go backend for **Kumbuild** (kumbuild.com) — receives website orders from the public order form, manages records in PostgreSQL, and dispatches transactional and promotional emails via Brevo.

---

## Tech Stack & Architecture

- **Go 1.23+** (Standard library focused, minimal external dependencies):
  - `github.com/go-chi/chi/v5` (HTTP router)
  - `github.com/jackc/pgx/v5` (PostgreSQL driver & connection pool)
  - `github.com/joho/godotenv` (Local `.env` loader)
- **PostgreSQL 16**: Sequential schema migrations embedded via `embed.FS` and auto-executed on server boot.
- **Brevo API**: Asynchronous transactional notifications and promotional email campaigns.
- **Cloudflare Turnstile**: Bot protection on public order submissions with auto-skip in local dev when secret is omitted.
- **Admin Token Authentication**: Protected admin routes using constant-time `X-Admin-Token` verification.

---

## Environment Variables

| Variable | Default / Example | Required | Description |
| :--- | :--- | :--- | :--- |
| `PORT` | `8080` | No | HTTP server port |
| `DATABASE_URL` | `postgres://kumbuild:kumbuild@localhost:5432/kumbuild_orders?sslmode=disable` | Yes | PostgreSQL connection string (or `memory` for in-memory testing) |
| `BREVO_API_KEY` | *(empty in dev)* | No | Brevo v3 API Key for sending emails (logs mock if empty) |
| `MAIL_FROM` | `hello@kumbuild.com` | No | Sender email address for Kumbuild notifications |
| `OWNER_EMAIL` | `owner@kumbuild.com` | Yes | Kumbuild owner's alert email destination |
| `ADMIN_TOKEN` | `kumbuild_secret_admin_token_2026` | Yes | Token passed via `X-Admin-Token` header for admin routes |
| `TURNSTILE_SECRET_KEY` | *(empty in dev)* | No | Cloudflare Turnstile secret key (skipped if empty) |
| `CORS_ORIGINS` | `http://localhost:5173,https://kumbuild.com,https://www.kumbuild.com` | No | Comma-separated allowed CORS origins |

---

## How to Run

### 1. Start PostgreSQL (Docker)
```bash
docker compose up -d
```
*(Runs Postgres 16 on port 5432 with database `kumbuild_orders`, user `kumbuild`, password `kumbuild`)*

### 2. Configure Environment
Copy `.env.example` to `.env`:
```bash
cp .env.example .env
```

### 3. Run the Backend
```bash
go run ./cmd/server
```

*(Note: If Postgres is not running locally, the server automatically falls back to an in-memory store so development and testing can proceed seamlessly.)*

### 4. Run Automated Tests
```bash
go test -v ./...
```

---

## HTTP API Reference

All responses return standard JSON. Errors follow the unified shape:
```json
{
  "error": "human-readable error message"
}
```

### Public Endpoints

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/healthz` | Health check endpoint returning `{"ok":true}` |
| `POST` | `/api/orders` | Public website order submission (verifies Turnstile, saves order, dispatches async emails) |

#### Order Payload Schema:
```json
{
  "name": "Dr. Ramesh Gupta",
  "businessName": "Gupta Dental Care",
  "businessType": "Clinic",
  "package": "business",
  "phone": "9876543210",
  "whatsapp": "9876543210",
  "email": "dr.gupta@example.com",
  "contentLanguage": "English, Hindi",
  "message": "Looking for clinic appointment booking and dental treatment showcase.",
  "turnstileToken": ""
}
```
**Allowed packages:** `starter`, `business`, `premium`  
**Allowed business types:** `Shop`, `Clinic`, `Tuition / Coaching`, `Salon`, `Restaurant / Cafe`, `Other`  
**Response (201 Created):**
```json
{
  "reference": "KB-7F3K9A"
}
```

---

### Admin Endpoints (`X-Admin-Token` Header Required)

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/api/admin/orders` | List orders (supports `?status=`, `?q=`, `?page=`, `?limit=`) |
| `GET` | `/api/admin/orders/{id}` | Get single order details |
| `PATCH` | `/api/admin/orders/{id}` | Update order status (`{"status":"in_progress"}`) |
| `GET` | `/api/admin/customers` | Get customer overview aggregated by unique phone number |
| `POST` | `/api/admin/campaigns` | Send promotional email campaign (`{"subject": "...", "html": "...", "recipients": "all"}`) |
| `GET` | `/api/admin/campaigns` | Get list of historical campaigns and delivery statistics |

**Allowed order statuses:**  
`new`, `contacted`, `advance_paid`, `in_progress`, `preview_ready`, `delivered`, `cancelled`

---

## Example cURL Commands

### 1. Create a New Order
```bash
curl -X POST http://localhost:8080/api/orders \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Arun Patel",
    "businessName": "Patel Sweets & Snacks",
    "businessType": "Shop",
    "package": "business",
    "phone": "9820098200",
    "whatsapp": "9820098200",
    "email": "arun@patelsweets.com",
    "contentLanguage": "Hindi, English",
    "message": "Need online menu catalog and WhatsApp order button."
  }'
```

### 2. List Orders (Admin)
```bash
curl -X GET "http://localhost:8080/api/admin/orders?limit=10" \
  -H "X-Admin-Token: kumbuild_secret_admin_token_2026"
```

### 3. Update Order Status (Admin)
```bash
curl -X PATCH http://localhost:8080/api/admin/orders/1 \
  -H "Content-Type: application/json" \
  -H "X-Admin-Token: kumbuild_secret_admin_token_2026" \
  -d '{"status":"advance_paid"}'
```

### 4. Send Promotional Campaign (Admin)
```bash
curl -X POST http://localhost:8080/api/admin/campaigns \
  -H "Content-Type: application/json" \
  -H "X-Admin-Token: kumbuild_secret_admin_token_2026" \
  -d '{
    "subject": "Special Festival Offer from Kumbuild",
    "html": "<h2>Exclusive festive discount on website upgrades!</h2><p>Contact your account manager today.</p>",
    "recipients": "all"
  }'
```
