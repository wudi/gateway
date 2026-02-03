package auth

import (
	"context"
	"net/http"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
	"github.com/example/gateway/internal/middleware"
	"github.com/example/gateway/internal/variables"
)

// APIKeyAuth provides API key authentication
type APIKeyAuth struct {
	header     string
	queryParam string
	keys       map[string]string // key -> clientID
}

// NewAPIKeyAuth creates a new API key authenticator
func NewAPIKeyAuth(cfg config.APIKeyConfig) *APIKeyAuth {
	auth := &APIKeyAuth{
		header:     cfg.Header,
		queryParam: cfg.QueryParam,
		keys:       make(map[string]string),
	}

	if auth.header == "" && auth.queryParam == "" {
		auth.header = "X-API-Key"
	}

	for _, entry := range cfg.Keys {
		auth.keys[entry.Key] = entry.ClientID
	}

	return auth
}

// Authenticate verifies the API key and returns the identity
func (a *APIKeyAuth) Authenticate(r *http.Request) (*variables.Identity, error) {
	apiKey := a.extractKey(r)
	if apiKey == "" {
		return nil, errors.ErrUnauthorized.WithDetails("API key not provided")
	}

	clientID, ok := a.keys[apiKey]
	if !ok {
		return nil, errors.ErrUnauthorized.WithDetails("Invalid API key")
	}

	return &variables.Identity{
		ClientID: clientID,
		AuthType: "api_key",
		Claims:   map[string]interface{}{"client_id": clientID},
	}, nil
}

// extractKey extracts the API key from the request
func (a *APIKeyAuth) extractKey(r *http.Request) string {
	// Check header first
	if a.header != "" {
		if key := r.Header.Get(a.header); key != "" {
			return key
		}
	}

	// Check query parameter
	if a.queryParam != "" {
		if key := r.URL.Query().Get(a.queryParam); key != "" {
			return key
		}
	}

	return ""
}

// IsEnabled returns true if API key auth is configured
func (a *APIKeyAuth) IsEnabled() bool {
	return len(a.keys) > 0
}

// AddKey adds a new API key
func (a *APIKeyAuth) AddKey(key, clientID string) {
	a.keys[key] = clientID
}

// RemoveKey removes an API key
func (a *APIKeyAuth) RemoveKey(key string) {
	delete(a.keys, key)
}

// Middleware creates a middleware for API key authentication
func (a *APIKeyAuth) Middleware(required bool) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, err := a.Authenticate(r)

			if err != nil {
				if required {
					gatewayErr := err.(*errors.GatewayError)
					w.Header().Set("WWW-Authenticate", "API-Key")
					gatewayErr.WriteJSON(w)
					return
				}
				// Not required, continue without identity
				next.ServeHTTP(w, r)
				return
			}

			// Add identity to context
			varCtx := variables.GetFromRequest(r)
			varCtx.Identity = identity
			ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ValidateKey validates an API key without creating identity
func (a *APIKeyAuth) ValidateKey(key string) (clientID string, valid bool) {
	clientID, valid = a.keys[key]
	return
}
