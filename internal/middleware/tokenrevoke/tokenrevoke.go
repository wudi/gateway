package tokenrevoke

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
)

// TokenChecker checks and manages revoked tokens.
type TokenChecker struct {
	store      TokenStore
	defaultTTL time.Duration
	checked    atomic.Int64
	revoked    atomic.Int64
}

// New creates a new TokenChecker.
func New(cfg config.TokenRevocationConfig, redisClient *redis.Client) *TokenChecker {
	ttl := cfg.DefaultTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}

	var store TokenStore
	if cfg.Mode == "distributed" && redisClient != nil {
		store = NewRedisStore(redisClient)
	} else {
		store = NewMemoryStore(ttl / 2)
	}

	return &TokenChecker{
		store:      store,
		defaultTTL: ttl,
	}
}

// Check returns true if the token is allowed (not revoked), false if revoked.
func (tc *TokenChecker) Check(r *http.Request) bool {
	token := extractBearerToken(r)
	if token == "" {
		return true // no token to check
	}

	tc.checked.Add(1)

	key := tokenKey(token)
	revoked, _ := tc.store.Contains(r.Context(), key)
	if revoked {
		tc.revoked.Add(1)
		return false
	}
	return true
}

// Revoke adds a token or JTI to the revocation list.
// If tokenOrJTI looks like a JWT (contains dots), it extracts the key.
// Otherwise, it is treated as a raw JTI.
func (tc *TokenChecker) Revoke(tokenOrJTI string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = tc.defaultTTL
	}
	if ttl > tc.defaultTTL {
		ttl = tc.defaultTTL
	}

	var key string
	if strings.Count(tokenOrJTI, ".") == 2 {
		// Looks like a JWT token
		key = tokenKey(tokenOrJTI)
		// Try to compute TTL from exp claim
		if expTTL := tokenExpTTL(tokenOrJTI); expTTL > 0 && expTTL < ttl {
			ttl = expTTL
		}
	} else {
		// Treat as raw JTI
		key = tokenOrJTI
	}

	return tc.store.Add(context.Background(), key, ttl)
}

// Unrevoke removes a token or JTI from the revocation list.
func (tc *TokenChecker) Unrevoke(tokenOrJTI string) error {
	var key string
	if strings.Count(tokenOrJTI, ".") == 2 {
		key = tokenKey(tokenOrJTI)
	} else {
		key = tokenOrJTI
	}
	return tc.store.Remove(context.Background(), key)
}

// Close closes the underlying store.
func (tc *TokenChecker) Close() {
	tc.store.Close()
}

// Stats returns token revocation statistics.
func (tc *TokenChecker) Stats() map[string]interface{} {
	return map[string]interface{}{
		"checked":    tc.checked.Load(),
		"revoked":    tc.revoked.Load(),
		"store_size": tc.store.Size(),
	}
}

// extractBearerToken extracts the Bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		return auth[7:]
	}
	return ""
}

// tokenKey computes the revocation key for a JWT token.
// If the token has a "jti" claim, that is used. Otherwise, the first 32 chars
// of the SHA256 hex digest of the full token are used.
func tokenKey(token string) string {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) >= 2 {
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err == nil {
			var claims map[string]interface{}
			if json.Unmarshal(payload, &claims) == nil {
				if jti, ok := claims["jti"]; ok {
					if s, ok := jti.(string); ok && s != "" {
						return s
					}
				}
			}
		}
	}

	// Fallback: SHA256 hash of full token
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h[:16]) // 32 hex chars
}

// tokenExpTTL computes the remaining TTL from the token's exp claim.
// Returns 0 if exp is missing or in the past.
func tokenExpTTL(token string) time.Duration {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims map[string]interface{}
	if json.Unmarshal(payload, &claims) != nil {
		return 0
	}
	exp, ok := claims["exp"]
	if !ok {
		return 0
	}
	var expTime time.Time
	switch v := exp.(type) {
	case float64:
		expTime = time.Unix(int64(v), 0)
	default:
		return 0
	}
	ttl := time.Until(expTime)
	if ttl <= 0 {
		return 0
	}
	return ttl
}

// Middleware returns a middleware that rejects requests with revoked JWT tokens.
func (tc *TokenChecker) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !tc.Check(r) {
				errors.New(http.StatusUnauthorized, "Token has been revoked").WriteJSON(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
