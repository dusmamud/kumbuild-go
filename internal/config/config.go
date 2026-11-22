package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port               string
	DatabaseURL        string
	BrevoAPIKey        string
	MailFrom           string
	OwnerEmail         string
	AdminToken         string
	TurnstileSecretKey string
	CORSOrigins        []string
}

func Load() *Config {
	// Attempt to load .env; ignore error if not found
	_ = godotenv.Load()

	port := getEnv("PORT", "8080")
	dbURL := getEnv("DATABASE_URL", "postgres://kumbuild:kumbuild@localhost:5432/kumbuild_orders?sslmode=disable")
	brevoKey := os.Getenv("BREVO_API_KEY")
	mailFrom := getEnv("MAIL_FROM", "hello@kumbuild.com")
	ownerEmail := getEnv("OWNER_EMAIL", "owner@kumbuild.com")
	adminToken := getEnv("ADMIN_TOKEN", "kumbuild_secret_admin_token_2026")
	turnstileKey := os.Getenv("TURNSTILE_SECRET_KEY")

	corsRaw := getEnv("CORS_ORIGINS", "http://localhost:5173,https://kumbuild.com,https://www.kumbuild.com")
	origins := strings.Split(corsRaw, ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}

	return &Config{
		Port:               port,
		DatabaseURL:        dbURL,
		BrevoAPIKey:        brevoKey,
		MailFrom:           mailFrom,
		OwnerEmail:         ownerEmail,
		AdminToken:         adminToken,
		TurnstileSecretKey: turnstileKey,
		CORSOrigins:        origins,
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
