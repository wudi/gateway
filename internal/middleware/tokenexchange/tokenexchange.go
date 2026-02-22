package tokenexchange

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware"
	"go.uber.org/zap"
)

// TokenExchanger handles token exchange for a single route.
type TokenExchanger struct {
	routeID       string
	validator     SubjectValidator
	issuer        *TokenIssuer
	cache         *exchangeCache
	claimMappings map[string]string
	scopes        []string
	metrics       ExchangeMetrics
}

// ExchangeMetrics tracks exchange activity.
type ExchangeMetrics struct {
	Total           atomic.Int64
	Exchanged       atomic.Int64
	CacheHits       atomic.Int64
	ValidationFails atomic.Int64
	IssueFails      atomic.Int64
}

// ExchangeStatus is the admin API snapshot.
type ExchangeStatus struct {
	RouteID         string `json:"route_id"`
	CacheSize       int    `json:"cache_size"`
	Total           int64  `json:"total"`
	Exchanged       int64  `json:"exchanged"`
	CacheHits       int64  `json:"cache_hits"`
	ValidationFails int64  `json:"validation_fails"`
	IssueFails      int64  `json:"issue_fails"`
}

// New creates a TokenExchanger from config.
func New(routeID string, cfg config.TokenExchangeConfig) (*TokenExchanger, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("token exchange not enabled")
	}

	// Build validator
	var validator SubjectValidator
	switch cfg.ValidationMode {
	case "jwt":
		v, err := NewJWTValidator(cfg.JWKSURL, cfg.TrustedIssuers, 1*time.Hour)
		if err != nil {
			return nil, err
		}
		validator = v
	case "introspection":
		validator = NewIntrospectionValidator(cfg.IntrospectionURL, cfg.ClientID, cfg.ClientSecret)
	default:
		return nil, fmt.Errorf("token exchange: unsupported validation_mode %q", cfg.ValidationMode)
	}

	// Build issuer
	issuer, err := NewTokenIssuer(
		cfg.SigningAlgorithm,
		cfg.SigningKey, cfg.SigningKeyFile, cfg.SigningSecret,
		cfg.Issuer,
		cfg.Audience,
		cfg.TokenLifetime,
	)
	if err != nil {
		return nil, err
	}

	// Build cache
	var cache *exchangeCache
	if cfg.CacheTTL > 0 {
		cache = newExchangeCache(cfg.CacheTTL)
	}

	return &TokenExchanger{
		routeID:       routeID,
		validator:     validator,
		issuer:        issuer,
		cache:         cache,
		claimMappings: cfg.ClaimMappings,
		scopes:        cfg.Scopes,
	}, nil
}

// Exchange performs the token exchange: validate subject token, issue internal token.
func (te *TokenExchanger) Exchange(subjectToken string) (string, error) {
	te.metrics.Total.Add(1)

	// Check cache
	if te.cache != nil {
		if issued, ok := te.cache.get(subjectToken); ok {
			te.metrics.CacheHits.Add(1)
			return issued, nil
		}
	}

	// Validate subject token
	validated, err := te.validator.Validate(subjectToken, "access_token")
	if err != nil {
		te.metrics.ValidationFails.Add(1)
		return "", fmt.Errorf("subject token validation failed: %w", err)
	}

	// Issue internal token
	issued, err := te.issuer.Issue(validated, te.claimMappings, te.scopes)
	if err != nil {
		te.metrics.IssueFails.Add(1)
		return "", fmt.Errorf("token issuance failed: %w", err)
	}

	// Cache result
	if te.cache != nil {
		te.cache.put(subjectToken, issued)
	}

	te.metrics.Exchanged.Add(1)
	return issued, nil
}

// Status returns the admin API snapshot.
func (te *TokenExchanger) Status() ExchangeStatus {
	cacheSize := 0
	if te.cache != nil {
		cacheSize = te.cache.size()
	}
	return ExchangeStatus{
		RouteID:         te.routeID,
		CacheSize:       cacheSize,
		Total:           te.metrics.Total.Load(),
		Exchanged:       te.metrics.Exchanged.Load(),
		CacheHits:       te.metrics.CacheHits.Load(),
		ValidationFails: te.metrics.ValidationFails.Load(),
		IssueFails:      te.metrics.IssueFails.Load(),
	}
}

// Middleware returns a middleware that performs token exchange.
func (te *TokenExchanger) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract bearer token
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				// No bearer token — skip exchange, let downstream auth handle it
				next.ServeHTTP(w, r)
				return
			}
			subjectToken := strings.TrimPrefix(authHeader, "Bearer ")

			// Perform exchange
			issuedToken, err := te.Exchange(subjectToken)
			if err != nil {
				logging.Warn("token exchange failed",
					zap.String("route_id", te.routeID),
					zap.Error(err),
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "token exchange failed",
				})
				return
			}

			// Replace Authorization header with gateway-issued token
			r.Header.Set("Authorization", "Bearer "+issuedToken)
			next.ServeHTTP(w, r)
		})
	}
}

// TokenExchangeByRoute manages per-route token exchangers.
type TokenExchangeByRoute struct {
	byroute.Manager[*TokenExchanger]
}

// NewTokenExchangeByRoute creates a new manager.
func NewTokenExchangeByRoute() *TokenExchangeByRoute {
	return &TokenExchangeByRoute{}
}

// AddRoute registers a token exchanger for a route.
func (m *TokenExchangeByRoute) AddRoute(routeID string, cfg config.TokenExchangeConfig) error {
	te, err := New(routeID, cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, te)
	return nil
}

// GetExchanger returns the exchanger for a route, or nil if none.
func (m *TokenExchangeByRoute) GetExchanger(routeID string) *TokenExchanger {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns exchange status for all routes.
func (m *TokenExchangeByRoute) Stats() map[string]ExchangeStatus {
	return byroute.CollectStats(&m.Manager, func(te *TokenExchanger) ExchangeStatus { return te.Status() })
}
