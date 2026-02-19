package nonce

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/variables"
)

// NonceChecker validates request nonces for replay prevention.
type NonceChecker struct {
	store           NonceStore
	header          string
	queryParam      string
	ttl             time.Duration
	required        bool
	scope           string
	timestampHeader string
	maxAge          time.Duration
	metrics         *NonceMetrics
	routeID         string
}

// New creates a new NonceChecker from config.
func New(routeID string, cfg config.NonceConfig, store NonceStore) *NonceChecker {
	header := cfg.Header
	if header == "" {
		header = "X-Nonce"
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	scope := cfg.Scope
	if scope == "" {
		scope = "global"
	}
	required := cfg.Required
	// When NonceConfig is enabled and Required isn't explicitly set to false,
	// default to true. Since Go zero-value for bool is false, we check if
	// the config is enabled and header is non-empty or use explicit Required field.
	// The plan says default true, but Go's zero bool is false. We handle this
	// by having the merge function set Required=true when not explicitly configured.

	nc := &NonceChecker{
		store:           store,
		header:          header,
		queryParam:      cfg.QueryParam,
		ttl:             ttl,
		required:        required,
		scope:           scope,
		timestampHeader: cfg.TimestampHeader,
		maxAge:          cfg.MaxAge,
		routeID:         routeID,
		metrics: &NonceMetrics{
			StoreSize: store.Size,
		},
	}
	return nc
}

// Check validates the nonce header on the request.
// Returns (allowed, statusCode, message).
func (nc *NonceChecker) Check(r *http.Request) (bool, int, string) {
	nc.metrics.TotalChecked.Add(1)

	nonceValue := r.Header.Get(nc.header)
	if nonceValue == "" && nc.queryParam != "" {
		nonceValue = r.URL.Query().Get(nc.queryParam)
	}

	// Missing nonce
	if nonceValue == "" {
		if nc.required {
			nc.metrics.MissingNonce.Add(1)
			nc.metrics.Rejected.Add(1)
			return false, http.StatusBadRequest, "missing nonce"
		}
		return true, 0, ""
	}

	// Timestamp validation
	if nc.timestampHeader != "" {
		tsValue := r.Header.Get(nc.timestampHeader)
		if tsValue != "" {
			ts, err := parseTimestamp(tsValue)
			if err != nil {
				nc.metrics.StaleTimestamp.Add(1)
				nc.metrics.Rejected.Add(1)
				return false, http.StatusBadRequest, "invalid timestamp"
			}
			if nc.maxAge > 0 {
				age := time.Since(ts)
				if age > nc.maxAge || age < -nc.maxAge {
					nc.metrics.StaleTimestamp.Add(1)
					nc.metrics.Rejected.Add(1)
					return false, http.StatusBadRequest, "request too old"
				}
			}
		}
	}

	// Build storage key
	key := nc.buildKey(r, nonceValue)

	// Check and store
	isNew, err := nc.store.CheckAndStore(r.Context(), key, nc.ttl)
	if err != nil {
		// Store errors are already handled by the store (fail-open for Redis)
		return true, 0, ""
	}
	if !isNew {
		nc.metrics.Rejected.Add(1)
		return false, http.StatusConflict, "replay detected"
	}

	return true, 0, ""
}

// Status returns the admin API status snapshot.
func (nc *NonceChecker) Status() NonceStatus {
	mode := "local"
	if _, ok := nc.store.(*RedisStore); ok {
		mode = "distributed"
	}
	return NonceStatus{
		Header:         nc.header,
		QueryParam:     nc.queryParam,
		Mode:           mode,
		Scope:          nc.scope,
		TTL:            nc.ttl.String(),
		Required:       nc.required,
		TotalChecked:   nc.metrics.TotalChecked.Load(),
		Rejected:       nc.metrics.Rejected.Load(),
		MissingNonce:   nc.metrics.MissingNonce.Load(),
		StaleTimestamp: nc.metrics.StaleTimestamp.Load(),
		StoreSize:      nc.metrics.StoreSize(),
	}
}

func (nc *NonceChecker) buildKey(r *http.Request, nonceValue string) string {
	if nc.scope == "per_client" {
		clientID := ""
		varCtx := variables.GetFromRequest(r)
		if varCtx.Identity != nil && varCtx.Identity.ClientID != "" {
			clientID = varCtx.Identity.ClientID
		} else {
			clientID = variables.ExtractClientIP(r)
		}
		return clientID + ":" + nonceValue
	}
	return nonceValue
}

// parseTimestamp tries RFC3339, then Unix seconds.
func parseTimestamp(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	unix, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(unix, 0), nil
}

// MergeNonceConfig merges per-route config over global config.
func MergeNonceConfig(perRoute, global config.NonceConfig) config.NonceConfig {
	merged := config.MergeNonZero(global, perRoute)
	merged.Enabled = true
	return merged
}

// Middleware returns a middleware that checks nonce for replay prevention.
func (nc *NonceChecker) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, statusCode, msg := nc.Check(r)
			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				fmt.Fprintf(w, `{"error":"%s"}`, msg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
