package baggage

import (
	"net/http"
	"sync/atomic"

	"go.opentelemetry.io/otel/baggage"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/internal/extract"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/variables"
)

// compiledTag is a pre-compiled baggage tag definition.
type compiledTag struct {
	name    string
	header  string       // custom backend header; empty = W3C-only tag
	w3cKey  string       // resolved W3C baggage key; empty if w3c_baggage disabled
	extract extract.Func // value extractor
}

// Propagator extracts and propagates baggage tags for a single route.
type Propagator struct {
	tags           []compiledTag
	propagateTrace bool
	w3cBaggage     bool
	propagated     atomic.Int64
}

// New creates a Propagator from config.
func New(cfg config.BaggageConfig) (*Propagator, error) {
	tags := make([]compiledTag, 0, len(cfg.Tags))
	for _, td := range cfg.Tags {
		ct := compiledTag{
			name:    td.Name,
			header:  td.Header,
			extract: extract.Build(td.Source),
		}
		if cfg.W3CBaggage {
			ct.w3cKey = td.BaggageKey
			if ct.w3cKey == "" {
				ct.w3cKey = td.Name
			}
		}
		tags = append(tags, ct)
	}
	return &Propagator{
		tags:           tags,
		propagateTrace: cfg.PropagateTrace,
		w3cBaggage:     cfg.W3CBaggage,
	}, nil
}

// Middleware returns the baggage propagation middleware.
func (p *Propagator) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vc := variables.GetFromRequest(r)

			// Build W3C baggage from existing upstream context
			var bag baggage.Baggage
			if p.w3cBaggage {
				bag = baggage.FromContext(r.Context())
			}

			for _, tag := range p.tags {
				val := tag.extract(r)
				if val == "" {
					continue
				}
				// Store in variable context custom data
				vc.SetCustom(tag.name, val)
				// Propagate as custom header to backend (skip when header is empty = W3C-only tag)
				if tag.header != "" {
					r.Header.Set(tag.header, val)
				}
				// Add to W3C baggage
				if tag.w3cKey != "" {
					m, err := baggage.NewMember(tag.w3cKey, val)
					if err != nil {
						continue // value not W3C-encodable, skip
					}
					bag, _ = bag.SetMember(m)
				}
			}

			// Store W3C baggage in context for OTEL propagator to serialize
			if p.w3cBaggage {
				ctx := baggage.ContextWithBaggage(r.Context(), bag)
				r = r.WithContext(ctx)
			}

			// Set propagate trace flag for proxy layer
			if p.propagateTrace {
				vc.PropagateTrace = true
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

// MergeBaggageConfig merges per-route over global. MergeNonZero(base=global, overlay=perRoute).
func MergeBaggageConfig(perRoute, global config.BaggageConfig) config.BaggageConfig {
	return config.MergeNonZero(global, perRoute)
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
			"tags":            len(p.tags),
			"propagated":      p.Propagated(),
			"propagate_trace": p.propagateTrace,
			"w3c_baggage":     p.w3cBaggage,
		}
	})
}
