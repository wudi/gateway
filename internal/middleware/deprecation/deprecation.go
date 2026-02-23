package deprecation

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware"
	"go.uber.org/zap"
)

// Handler injects RFC 8594 Deprecation/Sunset headers and optionally blocks after sunset.
type Handler struct {
	sunsetTime          *time.Time
	message             string
	link                string
	linkRelation        string
	responseAfterSunset *config.SunsetResponse
	logLevel            string

	requestsTotal atomic.Int64
	blocked       atomic.Int64
}

// New creates a new deprecation Handler from config.
func New(cfg config.DeprecationConfig) (*Handler, error) {
	h := &Handler{
		message:             cfg.Message,
		link:                cfg.Link,
		linkRelation:        cfg.LinkRelation,
		responseAfterSunset: cfg.ResponseAfterSunset,
		logLevel:            cfg.LogLevel,
	}

	if h.linkRelation == "" {
		h.linkRelation = "successor-version"
	}
	if h.logLevel == "" {
		h.logLevel = "warn"
	}

	if cfg.SunsetDate != "" {
		t, err := time.Parse(time.RFC3339, cfg.SunsetDate)
		if err != nil {
			return nil, fmt.Errorf("deprecation: invalid sunset_date: %w", err)
		}
		h.sunsetTime = &t
	}

	return h, nil
}

// Middleware returns the deprecation middleware.
func (h *Handler) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.requestsTotal.Add(1)

			// Check if past sunset and blocking is configured
			if h.sunsetTime != nil && h.responseAfterSunset != nil && time.Now().After(*h.sunsetTime) {
				h.blocked.Add(1)
				status := h.responseAfterSunset.Status
				if status == 0 {
					status = 410
				}
				for k, v := range h.responseAfterSunset.Headers {
					w.Header().Set(k, v)
				}
				w.Header().Set("Deprecation", "true")
				w.Header().Set("Sunset", h.sunsetTime.UTC().Format(http.TimeFormat))
				if h.link != "" {
					w.Header().Set("Link", fmt.Sprintf("<%s>; rel=%q", h.link, h.linkRelation))
				}
				w.WriteHeader(status)
				if h.responseAfterSunset.Body != "" {
					w.Write([]byte(h.responseAfterSunset.Body))
				}
				return
			}

			// Inject headers
			w.Header().Set("Deprecation", "true")
			if h.sunsetTime != nil {
				w.Header().Set("Sunset", h.sunsetTime.UTC().Format(http.TimeFormat))
			}
			if h.link != "" {
				w.Header().Set("Link", fmt.Sprintf("<%s>; rel=%q", h.link, h.linkRelation))
			}

			// Log deprecation notice
			msg := "deprecated API accessed"
			if h.message != "" {
				msg = h.message
			}
			if h.logLevel == "info" {
				logging.Info(msg, zap.String("path", r.URL.Path), zap.String("method", r.Method))
			} else {
				logging.Warn(msg, zap.String("path", r.URL.Path), zap.String("method", r.Method))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns handler metrics.
func (h *Handler) Stats() map[string]interface{} {
	stats := map[string]interface{}{
		"requests_total": h.requestsTotal.Load(),
		"blocked":        h.blocked.Load(),
		"message":        h.message,
	}
	if h.sunsetTime != nil {
		stats["sunset_date"] = h.sunsetTime.Format(time.RFC3339)
		stats["past_sunset"] = time.Now().After(*h.sunsetTime)
	}
	return stats
}

// DeprecationByRoute manages per-route deprecation handlers.
type DeprecationByRoute struct {
	byroute.Manager[*Handler]
}

// NewDeprecationByRoute creates a new per-route deprecation manager.
func NewDeprecationByRoute() *DeprecationByRoute {
	return &DeprecationByRoute{}
}

// AddRoute adds a deprecation handler for a route.
func (m *DeprecationByRoute) AddRoute(routeID string, cfg config.DeprecationConfig) error {
	h, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, h)
	return nil
}

// GetHandler returns the deprecation handler for a route.
func (m *DeprecationByRoute) GetHandler(routeID string) *Handler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route deprecation stats.
func (m *DeprecationByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *Handler) interface{} { return h.Stats() })
}

// MergeDeprecationConfig returns per-route config if enabled, else global.
func MergeDeprecationConfig(perRoute, global config.DeprecationConfig) config.DeprecationConfig {
	if perRoute.Enabled {
		return perRoute
	}
	return global
}
