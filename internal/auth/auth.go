package auth

import (
	"context"
	"net/http"
	"strings"
)

// APIKeyAuth provides simple API key authentication
type APIKeyAuth struct {
	APIKey string
}

// NewAPIKeyAuth creates a new API key authenticator
func NewAPIKeyAuth(apiKey string) *APIKeyAuth {
	return &APIKeyAuth{APIKey: apiKey}
}

// Middleware returns a middleware function that validates API keys
func (a *APIKeyAuth) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for health check and OpenAPI endpoints
			if r.URL.Path == "/health" || r.URL.Path == "/openapi.yaml" || r.URL.Path == "/openapi.json" {
				next.ServeHTTP(w, r)
				return
			}

			// Check for API key in header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "Missing Authorization header", http.StatusUnauthorized)
				return
			}

			// Check for Bearer token format
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
				return
			}

			apiKey := strings.TrimPrefix(authHeader, "Bearer ")
			if apiKey != a.APIKey {
				http.Error(w, "Invalid API key", http.StatusUnauthorized)
				return
			}

			// Add API key to context for potential use in handlers
			ctx := context.WithValue(r.Context(), "api_key", apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// HumaMiddleware returns a Huma-compatible middleware
func (a *APIKeyAuth) HumaMiddleware() func(http.Handler) http.Handler {
	return a.Middleware()
}
