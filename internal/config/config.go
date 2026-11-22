package config

import (
	"errors"
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
	AdminEmail         string
	AdminPasswordHash  string
	JWTSecret          string
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
	ownerEmail := getEnv("OWNER_EMAIL", "hello@kumbuild.com")
	adminToken := os.Getenv("ADMIN_TOKEN")
	adminEmail := os.Getenv("ADMIN_EMAIL")
	adminPwHash := os.Getenv("ADMIN_PASSWORD_HASH")
	jwtSecret := os.Getenv("JWT_SECRET")
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
		AdminEmail:         adminEmail,
		AdminPasswordHash:  adminPwHash,
		JWTSecret:          jwtSecret,
		TurnstileSecretKey: turnstileKey,
		CORSOrigins:        origins,
	}
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.AdminToken) == "" {
		return errors.New("ADMIN_TOKEN is required — set a long random value in .env")
	}
	if strings.TrimSpace(c.AdminEmail) == "" {
		return errors.New("ADMIN_EMAIL is required — set owner login email in .env")
	}
	if strings.TrimSpace(c.AdminPasswordHash) == "" {
		return errors.New("ADMIN_PASSWORD_HASH is required — run cmd/hashpw to generate bcrypt hash")
	}
	if strings.TrimSpace(c.JWTSecret) == "" {
		return errors.New("JWT_SECRET is required — set a long random secret in .env")
	}
	return nil
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
