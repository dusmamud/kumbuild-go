# Kumbuild Orders Backend (`kumbuild-go`)

Lightweight, high-performance Go backend for **Kumbuild** (kumbuild.com) — receives website orders from the public order form, manages records in PostgreSQL, and dispatches transactional and promotional emails via Brevo.

---

## Tech Stack & Architecture

- **Go 1.23+** (Standard library focused, minimal external dependencies):
  - `github.com/go-chi/chi/v5` (HTTP router)
  - `github.com/jackc/pgx/v5` (PostgreSQL driver & connection pool)
  - `github.com/golang-jwt/jwt/v5` (JWT session tokens for admin panel)
  - `golang.org/x/crypto/bcrypt` (Secure password hashing)
  - `github.com/joho/godotenv` (Local `.env` loader)
- **PostgreSQL 16 / Supabase**: Sequential schema migrations embedded via `embed.FS` and auto-executed on server boot. Fail-fast error handling: if PostgreSQL connection fails, server immediately stops (no silent in-memory fallback).
- **Brevo API**: Asynchronous transactional notifications and non-blocking background promotional email campaigns.
- **Cloudflare Turnstile**: Bot protection on public order submissions with auto-skip in local dev when secret is omitted.
- **Dual Authentication**:
  - `Authorization: Bearer <JWT>`: 24-hour signed session tokens for the owner admin panel.
  - `X-Admin-Token: <token>`: Master secret header for backend scripts / automation.

---

## Environment Variables

| Variable | Default / Example | Required | Description |
| :--- | :--- | :--- | :--- |
| `PORT` | `8080` | No | HTTP server port |
| `DATABASE_URL` | `postgres://...` | Yes | PostgreSQL connection string. `DATABASE_URL=memory` is for dev/tests only. The server fails fast if PostgreSQL is unreachable. |
| `ADMIN_EMAIL` | *(None)* | **YES** | Owner's login email address for the admin panel. |
| `ADMIN_PASSWORD_HASH` | *(None)* | **YES** | Bcrypt hash (cost 12) of owner's password. Generated via `cmd/hashpw`. |
| `JWT_SECRET` | *(None)* | **YES** | Cryptographic secret for signing session JWTs (32+ chars). |
| `ADMIN_TOKEN` | *(None)* | **YES** | Master secret token passed via `X-Admin-Token` header for scripts. Server refuses to boot if empty. |
| `OWNER_EMAIL` | `hello@kumbuild.com` | No | Destination email for new order alerts. |
| `MAIL_FROM` | `hello@kumbuild.com` | No | Sender address for Kumbuild notifications. |
| `BREVO_API_KEY` | *(empty in dev)* | No | Brevo v3 API Key for sending emails (logs mock if empty). |
| `TURNSTILE_SECRET_KEY` | *(empty in dev)* | No | Cloudflare Turnstile secret key (skipped if empty). |
| `CORS_ORIGINS` | `http://localhost:5173,https://kumbuild.com,https://www.kumbuild.com` | No | Comma-separated allowed CORS origins. |

---

## First-Time Setup

### 1. Generate Admin Password Hash
Run the included password hash utility:
```bash
go run ./cmd/hashpw
# Or pass via PW environment variable:
PW="YourSecurePassword" go run ./cmd/hashpw
```
Copy the printed bcrypt hash into `ADMIN_PASSWORD_HASH` in `.env`.

> **IMPORTANT — wrap the hash in single quotes:**
> ```ini
> ADMIN_PASSWORD_HASH='$2a$12$...'
> ```
> A bcrypt hash contains `$` characters. Without the single quotes, the `.env`
> parser treats those as variable references and silently corrupts the hash —
> the server starts fine, but **login always fails with 401, even with the
> correct password**. If login keeps failing after setup, check this first.

### 2. Configure Environment
Copy `.env.example` to `.env` and configure all required variables:
```bash
cp .env.example .env
```
Ensure `ADMIN_EMAIL`, `ADMIN_PASSWORD_HASH`, `JWT_SECRET`, and `ADMIN_TOKEN` are set.

### 3. Run Server
```bash
go run ./cmd/server
```

### 4. Run Automated Tests
```bash
go test -v ./...
```

---

## HTTP API Reference

### Public Endpoints

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/healthz` | Health check endpoint returning `{"ok":true}` |
| `POST` | `/api/orders` | Public website order submission (verifies Turnstile, retries collisions, dispatches async emails) |
| `POST` | `/api/admin/login` | Owner authentication endpoint (`{"email":"...","password":"..."}`). Returns 24-hr JWT. Rate limited to 5 failed attempts per 15 min per IP. |

### Protected Admin Endpoints (`Bearer <JWT>` or `X-Admin-Token` Required)

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/api/admin/me` | Returns `{ "email": "..." }` for the currently authenticated admin session |
| `GET` | `/api/admin/orders` | List orders (supports `?status=`, `?q=`, `?page=`, `?limit=`) |
| `GET` | `/api/admin/orders/{id}` | Get single order details |
| `PATCH` | `/api/admin/orders/{id}` | Update order status (`{"status":"in_progress"}`) |
| `GET` | `/api/admin/customers` | Get customer overview aggregated by unique phone number |
| `POST` | `/api/admin/campaigns` | Non-blocking async promotional broadcast (`{"subject":"...","html":"...","recipients":"all"}`). Returns 201 immediately; sends in background. |
| `GET` | `/api/admin/campaigns` | Get list of historical campaigns and real-time delivery counts |
