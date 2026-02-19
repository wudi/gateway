package backendauth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware"
	"go.uber.org/zap"
)

// TokenProvider fetches and caches OAuth2 client_credentials access tokens.
type TokenProvider struct {
	tokenURL     string
	clientID     string
	clientSecret string
	scopes       []string
	extraParams  map[string]string
	timeout      time.Duration
	routeID      string

	mu          sync.RWMutex
	accessToken string
	expiresAt   time.Time

	refreshes    atomic.Int64
	errors       atomic.Int64
	lastRefresh  atomic.Int64 // unix nano
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// New creates a TokenProvider from config.
func New(routeID string, cfg config.BackendAuthConfig) (*TokenProvider, error) {
	if _, err := url.ParseRequestURI(cfg.TokenURL); err != nil {
		return nil, fmt.Errorf("invalid token_url: %w", err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &TokenProvider{
		tokenURL:     cfg.TokenURL,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		scopes:       cfg.Scopes,
		extraParams:  cfg.ExtraParams,
		timeout:      timeout,
		routeID:      routeID,
	}, nil
}

// getToken returns a cached token or refreshes if expired.
func (p *TokenProvider) getToken() (string, error) {
	p.mu.RLock()
	if p.accessToken != "" && time.Now().Before(p.expiresAt) {
		token := p.accessToken
		p.mu.RUnlock()
		return token, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if p.accessToken != "" && time.Now().Before(p.expiresAt) {
		return p.accessToken, nil
	}

	return p.refreshToken()
}

// refreshToken fetches a new token from the token endpoint. Must be called with p.mu held.
func (p *TokenProvider) refreshToken() (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
	}
	if len(p.scopes) > 0 {
		form.Set("scope", strings.Join(p.scopes, " "))
	}
	for k, v := range p.extraParams {
		form.Set(k, v)
	}

	client := &http.Client{Timeout: p.timeout}
	resp, err := client.PostForm(p.tokenURL, form)
	if err != nil {
		p.errors.Add(1)
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		p.errors.Add(1)
		return "", fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		p.errors.Add(1)
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		p.errors.Add(1)
		return "", fmt.Errorf("parsing token response: %w", err)
	}

	if tr.AccessToken == "" {
		p.errors.Add(1)
		return "", fmt.Errorf("token response missing access_token")
	}

	// Cache with 10s safety margin
	expiresIn := tr.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	p.accessToken = tr.AccessToken
	p.expiresAt = time.Now().Add(time.Duration(expiresIn)*time.Second - 10*time.Second)
	p.refreshes.Add(1)
	p.lastRefresh.Store(time.Now().UnixNano())

	return p.accessToken, nil
}

// Apply sets the Authorization header on the request.
func (p *TokenProvider) Apply(r *http.Request) {
	token, err := p.getToken()
	if err != nil {
		logging.Warn("backend auth token refresh failed",
			zap.String("route_id", p.routeID),
			zap.Error(err),
		)
		return
	}
	r.Header.Set("Authorization", "Bearer "+token)
}

// Middleware returns a middleware that applies backend auth.
func (p *TokenProvider) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.Apply(r)
			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns token provider statistics.
func (p *TokenProvider) Stats() map[string]interface{} {
	stats := map[string]interface{}{
		"refreshes": p.refreshes.Load(),
		"errors":    p.errors.Load(),
	}
	if ts := p.lastRefresh.Load(); ts > 0 {
		stats["last_refresh_at"] = time.Unix(0, ts).Format(time.RFC3339)
	}
	return stats
}

// BackendAuthByRoute manages per-route backend auth token providers.
type BackendAuthByRoute struct {
	byroute.Manager[*TokenProvider]
}

// NewBackendAuthByRoute creates a new per-route backend auth manager.
func NewBackendAuthByRoute() *BackendAuthByRoute {
	return &BackendAuthByRoute{}
}

// AddRoute adds a backend auth provider for a route.
func (m *BackendAuthByRoute) AddRoute(routeID string, cfg config.BackendAuthConfig) error {
	p, err := New(routeID, cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, p)
	return nil
}

// GetProvider returns the token provider for a route.
func (m *BackendAuthByRoute) GetProvider(routeID string) *TokenProvider {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route backend auth stats.
func (m *BackendAuthByRoute) Stats() map[string]interface{} {
	stats := make(map[string]interface{})
	m.Range(func(id string, p *TokenProvider) bool {
		stats[id] = p.Stats()
		return true
	})
	return stats
}
