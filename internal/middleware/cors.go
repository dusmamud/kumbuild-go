package middleware

import (
	"net/http"
	"strings"
)

// CORS returns a middleware allowing requests from the specified origins.
// Supports GET, POST, PATCH, OPTIONS and Content-Type, X-Admin-Token headers.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	originsMap := make(map[string]bool)
	for _, o := range allowedOrigins {
		originsMap[strings.ToLower(strings.TrimSpace(o))] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if originsMap["*"] || originsMap[strings.ToLower(origin)] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Admin-Token")
					w.Header().Set("Access-Control-Max-Age", "86400")
				}
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
