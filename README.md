# Kumbuild Orders Backend (`kumbuild-go`)

Lightweight, high-performance Go backend for **Kumbuild** (kumbuild.com) — receives website orders from the public order form, manages records in PostgreSQL, and dispatches transactional and promotional emails via Brevo.

---

## Tech Stack & Architecture

- **Go 1.23+** (Standard library focused, minimal external dependencies):
  - `github.com/go-chi/chi/v5` (HTTP router)
  - `github.com/jackc/pgx/v5` (PostgreSQL driver & connection pool)
  - `github.com/joho/godotenv` (Local `.env` loader)
- **PostgreSQL 16 / Supabase**: Sequential schema migrations embedded via `embed.FS` and auto-executed on server boot. Fail-fast error handling: if PostgreSQL connection fails, server immediately stops (no silent in-memory fallback).
- **Brevo API**: Asynchronous transactional notifications and promotional email campaigns.
- **Cloudflare Turnstile**: Bot protection on public order submissions with auto-skip in local dev when secret is omitted.
- **Admin Token Authentication**: Protected admin routes using constant-time `X-Admin-Token` verification. `ADMIN_TOKEN` is mandatory; server will refuse to start without it.

---

## Environment Variables

| Variable | Default / Example | Required | Description |
| :--- | :--- | :--- | :--- |
| `PORT` | `8080` | No | HTTP server port |
| `DATABASE_URL` | `postgres://...` | Yes | PostgreSQL connection string. `DATABASE_URL=memory` is for dev/tests only. The server fails fast if PostgreSQL is unreachable. |
| `ADMIN_TOKEN` | *(None)* | **YES** | Mandatory private token passed via `X-Admin-Token` header for admin routes. Server will refuse to boot if empty. |
| `OWNER_EMAIL` | `hello@kumbuild.com` | No | Kumbuild owner's alert email destination |
| `MAIL_FROM` | `hello@kumbuild.com` | No | Sender email address for Kumbuild notifications |
| `BREVO_API_KEY` | *(empty in dev)* | No | Brevo v3 API Key for sending emails (logs mock if empty) |
| `TURNSTILE_SECRET_KEY` | *(empty in dev)* | No | Cloudflare Turnstile secret key (skipped if empty) |
| `CORS_ORIGINS` | `http://localhost:5173,https://kumbuild.com,https://www.kumbuild.com` | No | Comma-separated allowed CORS origins |

---

## How to Run

### 1. Start PostgreSQL (Docker) or use Supabase
```bash
docker compose up -d
```
*(Runs Postgres 16 on port 5432 with database `kumbuild_orders`, user `kumbuild`, password `kumbuild`)*

Or provide a valid Supabase Session Pooler connection string (`port 5432`, `sslmode=require`).

### 2. Configure Environment
Copy `.env.example` to `.env` and set a secure `ADMIN_TOKEN`:
```bash
cp .env.example .env
```

### 3. Run the Backend
```bash
go run ./cmd/server
```

*(Note: The server requires a reachable PostgreSQL database unless `DATABASE_URL=memory` is explicitly passed for local unit testing.)*

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
| `POST` | `/api/orders` | Public website order submission (verifies Turnstile, retries reference collisions up to 3x, saves order, dispatches async emails) |

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
**Phone Normalization:**
- `9812345678` (10 digits) → `+919812345678`
- `09812345678` (11 digits with leading 0) → `+919812345678`
- `919812345678` (12 digits with 91 prefix) → `+919812345678`

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
