package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"kumbuild-go/internal/auth"
)

type contextKey string

const AuthEmailKey contextKey = "auth_email"

// AdminAuth protects admin endpoints. Accepts EITHER:
// 1. Authorization: Bearer <JWT> signed with jwtSecret
// 2. X-Admin-Token: <token> matching expectedToken via constant time compare
func AdminAuth(expectedToken string, jwtSecret string, defaultEmail string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check Authorization: Bearer <token>
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
				email, err := auth.ValidateJWT(tokenStr, jwtSecret)
				if err == nil && email != "" {
					ctx := context.WithValue(r.Context(), AuthEmailKey, email)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// If Bearer was supplied but invalid/expired, reject immediately
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}

			// Fallback to X-Admin-Token
			token := r.Header.Get("X-Admin-Token")
			if token != "" && expectedToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) == 1 {
				ctx := context.WithValue(r.Context(), AuthEmailKey, defaultEmail)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		})
	}
}
