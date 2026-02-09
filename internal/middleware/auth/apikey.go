package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/variables"
)

// APIKeyAuth provides API key authentication
type APIKeyAuth struct {
	header     string
	queryParam string
	keys       map[string]*APIKeyData // key -> data
	mu         sync.RWMutex
}

// APIKeyData stores full API key information
type APIKeyData struct {
	ClientID  string    `json:"client_id"`
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Roles     []string  `json:"roles,omitempty"`
}

// NewAPIKeyAuth creates a new API key authenticator
func NewAPIKeyAuth(cfg config.APIKeyConfig) *APIKeyAuth {
	auth := &APIKeyAuth{
		header:     cfg.Header,
		queryParam: cfg.QueryParam,
		keys:       make(map[string]*APIKeyData),
	}

	if auth.header == "" && auth.queryParam == "" {
		auth.header = "X-API-Key"
	}

	for _, entry := range cfg.Keys {
		data := &APIKeyData{
			ClientID: entry.ClientID,
			Name:     entry.Name,
			Roles:    entry.Roles,
		}

		if entry.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, entry.ExpiresAt); err == nil {
				data.ExpiresAt = t
			}
		}

		auth.keys[entry.Key] = data
	}

	return auth
}

// Authenticate verifies the API key and returns the identity
func (a *APIKeyAuth) Authenticate(r *http.Request) (*variables.Identity, error) {
	apiKey := a.extractKey(r)
	if apiKey == "" {
		return nil, errors.ErrUnauthorized.WithDetails("API key not provided")
	}

	a.mu.RLock()
	data, ok := a.keys[apiKey]
	a.mu.RUnlock()

	if !ok {
		return nil, errors.ErrUnauthorized.WithDetails("Invalid API key")
	}

	// Check expiration
	if !data.ExpiresAt.IsZero() && time.Now().After(data.ExpiresAt) {
		return nil, errors.ErrUnauthorized.WithDetails("API key has expired")
	}

	claims := map[string]interface{}{
		"client_id": data.ClientID,
	}
	if len(data.Roles) > 0 {
		claims["roles"] = data.Roles
	}

	return &variables.Identity{
		ClientID: data.ClientID,
		AuthType: "api_key",
		Claims:   claims,
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
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.keys) > 0
}

// AddKey adds a new API key
func (a *APIKeyAuth) AddKey(key, clientID string) {
	a.mu.Lock()
	a.keys[key] = &APIKeyData{ClientID: clientID}
	a.mu.Unlock()
}

// AddKeyWithData adds a new API key with full metadata
func (a *APIKeyAuth) AddKeyWithData(key string, data *APIKeyData) {
	a.mu.Lock()
	a.keys[key] = data
	a.mu.Unlock()
}

// RemoveKey removes an API key
func (a *APIKeyAuth) RemoveKey(key string) {
	a.mu.Lock()
	delete(a.keys, key)
	a.mu.Unlock()
}

// GetKeyData returns the data for a key (for admin API)
func (a *APIKeyAuth) GetKeyData(key string) (*APIKeyData, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	data, ok := a.keys[key]
	return data, ok
}

// ListKeys returns all keys with their metadata (for admin API)
func (a *APIKeyAuth) ListKeys() map[string]*APIKeyData {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make(map[string]*APIKeyData, len(a.keys))
	for k, v := range a.keys {
		// Mask the key for security
		masked := k
		if len(masked) > 8 {
			masked = masked[:4] + "****" + masked[len(masked)-4:]
		}
		result[masked] = v
	}
	return result
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
	a.mu.RLock()
	data, ok := a.keys[key]
	a.mu.RUnlock()

	if !ok {
		return "", false
	}
	if !data.ExpiresAt.IsZero() && time.Now().After(data.ExpiresAt) {
		return "", false
	}
	return data.ClientID, true
}

// AdminKeyEntry is the JSON format for admin API key management
type AdminKeyEntry struct {
	Key       string   `json:"key"`
	ClientID  string   `json:"client_id"`
	Name      string   `json:"name,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	Roles     []string `json:"roles,omitempty"`
}

// HandleAdminKeys handles admin API requests for key management
func (a *APIKeyAuth) HandleAdminKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		keys := a.ListKeys()
		json.NewEncoder(w).Encode(keys)

	case http.MethodPost:
		var entry AdminKeyEntry
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			errors.ErrBadRequest.WithDetails("Invalid JSON body").WriteJSON(w)
			return
		}

		if entry.Key == "" || entry.ClientID == "" {
			errors.ErrBadRequest.WithDetails("key and client_id are required").WriteJSON(w)
			return
		}

		data := &APIKeyData{
			ClientID: entry.ClientID,
			Name:     entry.Name,
			Roles:    entry.Roles,
		}

		if entry.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, entry.ExpiresAt)
			if err != nil {
				errors.ErrBadRequest.WithDetails("expires_at must be RFC3339 format").WriteJSON(w)
				return
			}
			data.ExpiresAt = t
		}

		a.AddKeyWithData(entry.Key, data)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "created"})

	case http.MethodDelete:
		var entry struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil || entry.Key == "" {
			errors.ErrBadRequest.WithDetails("key is required").WriteJSON(w)
			return
		}

		a.RemoveKey(entry.Key)
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		errors.ErrMethodNotAllowed.WriteJSON(w)
	}
}
