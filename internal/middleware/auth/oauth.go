package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/variables"
)

// OAuthAuth provides OAuth 2.0 / OIDC token introspection
type OAuthAuth struct {
	introspectionURL string
	clientID         string
	clientSecret     string
	issuer           string
	audience         string
	scopes           []string
	cacheTTL         time.Duration

	// Token introspection cache
	cache   map[string]*oauthCacheEntry
	cacheMu sync.RWMutex

	client *http.Client
}

type oauthCacheEntry struct {
	identity  *variables.Identity
	expiresAt time.Time
}

// NewOAuthAuth creates a new OAuth authenticator
func NewOAuthAuth(cfg config.OAuthConfig) (*OAuthAuth, error) {
	auth := &OAuthAuth{
		introspectionURL: cfg.IntrospectionURL,
		clientID:         cfg.ClientID,
		clientSecret:     cfg.ClientSecret,
		issuer:           cfg.Issuer,
		audience:         cfg.Audience,
		scopes:           cfg.Scopes,
		cacheTTL:         cfg.CacheTTL,
		cache:            make(map[string]*oauthCacheEntry),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	if auth.cacheTTL == 0 {
		auth.cacheTTL = 5 * time.Minute
	}

	return auth, nil
}

// Authenticate verifies the OAuth token
func (a *OAuthAuth) Authenticate(r *http.Request) (*variables.Identity, error) {
	token := extractBearerToken(r)
	if token == "" {
		return nil, errors.ErrUnauthorized.WithDetails("Bearer token not provided")
	}

	// Check cache
	if identity := a.getCached(token); identity != nil {
		return identity, nil
	}

	// Introspect token
	identity, err := a.introspect(token)
	if err != nil {
		return nil, err
	}

	// Cache result
	a.setCached(token, identity)

	return identity, nil
}

// IsEnabled returns true if OAuth auth is configured
func (a *OAuthAuth) IsEnabled() bool {
	return a.introspectionURL != ""
}

func (a *OAuthAuth) introspect(token string) (*variables.Identity, error) {
	data := url.Values{}
	data.Set("token", token)

	req, err := http.NewRequest("POST", a.introspectionURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errors.ErrInternalServer.WithDetails("Failed to create introspection request")
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if a.clientID != "" && a.clientSecret != "" {
		req.SetBasicAuth(a.clientID, a.clientSecret)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, errors.ErrBadGateway.WithDetails("Token introspection failed")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.ErrInternalServer.WithDetails("Failed to read introspection response")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, errors.ErrInternalServer.WithDetails("Invalid introspection response")
	}

	// Check if token is active
	active, _ := result["active"].(bool)
	if !active {
		return nil, errors.ErrUnauthorized.WithDetails("Token is not active")
	}

	// Validate issuer
	if a.issuer != "" {
		if iss, ok := result["iss"].(string); !ok || iss != a.issuer {
			return nil, errors.ErrUnauthorized.WithDetails("Invalid token issuer")
		}
	}

	// Validate audience
	if a.audience != "" {
		if aud, ok := result["aud"].(string); !ok || aud != a.audience {
			return nil, errors.ErrUnauthorized.WithDetails("Invalid token audience")
		}
	}

	// Validate scopes
	if len(a.scopes) > 0 {
		scopeStr, _ := result["scope"].(string)
		tokenScopes := strings.Fields(scopeStr)
		if !hasAllScopes(tokenScopes, a.scopes) {
			return nil, errors.ErrForbidden.WithDetails("Insufficient scopes")
		}
	}

	// Extract client ID
	clientID := ""
	if sub, ok := result["sub"].(string); ok {
		clientID = sub
	} else if cid, ok := result["client_id"].(string); ok {
		clientID = cid
	}

	return &variables.Identity{
		ClientID: clientID,
		AuthType: "oauth",
		Claims:   result,
	}, nil
}

func (a *OAuthAuth) getCached(token string) *variables.Identity {
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()

	entry, ok := a.cache[token]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil
	}
	return entry.identity
}

func (a *OAuthAuth) setCached(token string, identity *variables.Identity) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()

	a.cache[token] = &oauthCacheEntry{
		identity:  identity,
		expiresAt: time.Now().Add(a.cacheTTL),
	}

	// Simple eviction: if cache too large, clear it
	if len(a.cache) > 10000 {
		a.cache = make(map[string]*oauthCacheEntry)
	}
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	if strings.HasPrefix(auth, "bearer ") {
		return auth[7:]
	}
	return ""
}

func hasAllScopes(tokenScopes, required []string) bool {
	scopeSet := make(map[string]bool)
	for _, s := range tokenScopes {
		scopeSet[s] = true
	}
	for _, s := range required {
		if !scopeSet[s] {
			return false
		}
	}
	return true
}

// CleanupCache removes expired entries
func (a *OAuthAuth) CleanupCache() {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()

	now := time.Now()
	for token, entry := range a.cache {
		if now.After(entry.expiresAt) {
			delete(a.cache, token)
		}
	}
}

// GenerateToken is not supported for OAuth (tokens come from identity provider)
func (a *OAuthAuth) GenerateToken(_ map[string]interface{}) (string, error) {
	return "", fmt.Errorf("token generation not supported for OAuth; use your identity provider")
}
