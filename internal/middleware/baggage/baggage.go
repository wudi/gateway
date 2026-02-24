package baggage

import (
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/variables"
)

// extractorFunc extracts a value from a request.
type extractorFunc func(r *http.Request) string

// compiledTag is a pre-compiled baggage tag definition.
type compiledTag struct {
	name    string
	header  string
	extract extractorFunc
}

// Propagator extracts and propagates baggage tags for a single route.
type Propagator struct {
	tags       []compiledTag
	propagated atomic.Int64
}

// New creates a Propagator from config.
func New(cfg config.BaggageConfig) (*Propagator, error) {
	tags := make([]compiledTag, 0, len(cfg.Tags))
	for _, td := range cfg.Tags {
		ext := buildExtractor(td.Source)
		tags = append(tags, compiledTag{
			name:    td.Name,
			header:  td.Header,
			extract: ext,
		})
	}
	return &Propagator{tags: tags}, nil
}

func buildExtractor(source string) extractorFunc {
	switch {
	case strings.HasPrefix(source, "header:"):
		hdr := source[len("header:"):]
		return func(r *http.Request) string {
			return r.Header.Get(hdr)
		}
	case strings.HasPrefix(source, "jwt_claim:"):
		claim := source[len("jwt_claim:"):]
		return func(r *http.Request) string {
			vc := variables.GetFromRequest(r)
			if vc.Identity == nil || vc.Identity.Claims == nil {
				return ""
			}
			if v, ok := vc.Identity.Claims[claim]; ok {
				if s, ok := v.(string); ok {
					return s
				}
			}
			return ""
		}
	case strings.HasPrefix(source, "query:"):
		param := source[len("query:"):]
		return func(r *http.Request) string {
			return r.URL.Query().Get(param)
		}
	case strings.HasPrefix(source, "cookie:"):
		name := source[len("cookie:"):]
		return func(r *http.Request) string {
			if c, err := r.Cookie(name); err == nil {
				return c.Value
			}
			return ""
		}
	case strings.HasPrefix(source, "static:"):
		val := source[len("static:"):]
		return func(r *http.Request) string {
			return val
		}
	default:
		return func(r *http.Request) string { return "" }
	}
}

// Middleware returns the baggage propagation middleware.
func (p *Propagator) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vc := variables.GetFromRequest(r)
			for _, tag := range p.tags {
				val := tag.extract(r)
				if val == "" {
					continue
				}
				// Store in variable context custom data
				vc.SetCustom(tag.name, val)
				// Propagate as header to backend
				r.Header.Set(tag.header, val)
			}
			p.propagated.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

// Propagated returns the count of requests propagated.
func (p *Propagator) Propagated() int64 {
	return p.propagated.Load()
}

// BaggageByRoute manages per-route baggage propagators.
type BaggageByRoute struct {
	byroute.Manager[*Propagator]
}

// NewBaggageByRoute creates a new per-route baggage manager.
func NewBaggageByRoute() *BaggageByRoute {
	return &BaggageByRoute{}
}

// AddRoute adds a baggage propagator for a route.
func (b *BaggageByRoute) AddRoute(routeID string, cfg config.BaggageConfig) error {
	p, err := New(cfg)
	if err != nil {
		return err
	}
	b.Add(routeID, p)
	return nil
}

// GetPropagator returns the propagator for a route.
func (b *BaggageByRoute) GetPropagator(routeID string) *Propagator {
	v, _ := b.Get(routeID)
	return v
}

// Stats returns per-route baggage stats.
func (b *BaggageByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&b.Manager, func(p *Propagator) interface{} {
		return map[string]interface{}{
			"tags":       len(p.tags),
			"propagated": p.Propagated(),
		}
	})
}
