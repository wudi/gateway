package consumergroup

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/variables"
)

type contextKey struct{}

// GroupInfo contains the resolved consumer group stored in request context.
type GroupInfo struct {
	Name  string
	Group *config.ConsumerGroup
}

// WithGroup stores GroupInfo in a context.
func WithGroup(ctx context.Context, info *GroupInfo) context.Context {
	return context.WithValue(ctx, contextKey{}, info)
}

// FromContext retrieves the GroupInfo from a request context.
func FromContext(ctx context.Context) *GroupInfo {
	v, _ := ctx.Value(contextKey{}).(*GroupInfo)
	return v
}

// GroupManager manages consumer group resolution and per-group metrics.
type GroupManager struct {
	groups           map[string]*config.ConsumerGroup
	totalRequests    atomic.Int64
	perGroupRequests sync.Map // string -> *atomic.Int64
}

// NewGroupManager creates a GroupManager from config.
func NewGroupManager(cfg config.ConsumerGroupsConfig) *GroupManager {
	groups := make(map[string]*config.ConsumerGroup, len(cfg.Groups))
	m := &GroupManager{groups: groups}
	for name, g := range cfg.Groups {
		g := g // copy for pointer stability
		groups[name] = &g
		m.perGroupRequests.Store(name, &atomic.Int64{})
	}
	return m
}

// Middleware returns a middleware that resolves the consumer group from identity claims.
//
// Resolution order:
//  1. Claims["consumer_group"] (string) -- explicit assignment
//  2. Claims["roles"] ([]interface{} of strings) -- first role matching a defined group
//
// If no group is found the request passes through without setting context.
func (gm *GroupManager) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			v := variables.GetFromRequest(r)
			if v == nil || v.Identity == nil {
				next.ServeHTTP(w, r)
				return
			}

			name, group := gm.resolve(v.Identity)
			if group == nil {
				next.ServeHTTP(w, r)
				return
			}

			gm.totalRequests.Add(1)
			if counter, ok := gm.perGroupRequests.Load(name); ok {
				counter.(*atomic.Int64).Add(1)
			}

			info := &GroupInfo{Name: name, Group: group}
			ctx := WithGroup(r.Context(), info)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolve determines the consumer group for an identity.
func (gm *GroupManager) resolve(id *variables.Identity) (string, *config.ConsumerGroup) {
	if id.Claims == nil {
		return "", nil
	}

	// 1. Explicit consumer_group claim
	if cg, ok := id.Claims["consumer_group"]; ok {
		if name, ok := cg.(string); ok {
			if g, exists := gm.groups[name]; exists {
				return name, g
			}
		}
	}

	// 2. First matching role
	if roles, ok := id.Claims["roles"]; ok {
		if roleSlice, ok := roles.([]interface{}); ok {
			for _, role := range roleSlice {
				if name, ok := role.(string); ok {
					if g, exists := gm.groups[name]; exists {
						return name, g
					}
				}
			}
		}
	}

	return "", nil
}

// GetGroup returns the group definition for a name.
func (gm *GroupManager) GetGroup(name string) (*config.ConsumerGroup, bool) {
	g, ok := gm.groups[name]
	return g, ok
}

// Stats returns group definitions and per-group request counts.
func (gm *GroupManager) Stats() map[string]interface{} {
	groupStats := make(map[string]interface{}, len(gm.groups))
	for name, g := range gm.groups {
		gs := map[string]interface{}{
			"rate_limit": g.RateLimit,
			"quota":      g.Quota,
			"priority":   g.Priority,
		}
		if len(g.Metadata) > 0 {
			gs["metadata"] = g.Metadata
		}
		if counter, ok := gm.perGroupRequests.Load(name); ok {
			gs["requests"] = counter.(*atomic.Int64).Load()
		}
		groupStats[name] = gs
	}
	return map[string]interface{}{
		"enabled":        true,
		"group_count":    len(gm.groups),
		"total_requests": gm.totalRequests.Load(),
		"groups":         groupStats,
	}
}

// GroupByRoute tracks which routes have consumer group middleware enabled and
// holds a reference to the shared GroupManager.
type GroupByRoute struct {
	manager *GroupManager
	byroute.Manager[bool]
}

// NewGroupByRoute creates a new GroupByRoute.
func NewGroupByRoute() *GroupByRoute {
	return &GroupByRoute{}
}

// SetManager sets the shared GroupManager.
func (g *GroupByRoute) SetManager(m *GroupManager) {
	g.manager = m
}

// AddRoute registers a route as having consumer groups enabled.
func (g *GroupByRoute) AddRoute(routeID string) {
	g.Add(routeID, true)
}

// GetManager returns the shared GroupManager.
func (g *GroupByRoute) GetManager() *GroupManager {
	return g.manager
}

// Stats returns group statistics from the underlying manager.
func (g *GroupByRoute) Stats() map[string]interface{} {
	if g.manager == nil {
		return nil
	}
	stats := g.manager.Stats()
	stats["routes"] = g.RouteIDs()
	return stats
}

// String implements fmt.Stringer for debug display.
func (g *GroupByRoute) String() string {
	return fmt.Sprintf("GroupByRoute{routes=%d}", g.Len())
}
