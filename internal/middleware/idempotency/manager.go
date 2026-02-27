package idempotency

import (
	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/byroute"
)

// IdempotencyByRoute manages per-route idempotency handlers.
type IdempotencyByRoute = byroute.NamedFactory[*CompiledIdempotency, config.IdempotencyConfig]

// NewIdempotencyByRoute creates a new IdempotencyByRoute manager.
// The redis client is captured in the constructor closure.
func NewIdempotencyByRoute(redisClient *redis.Client) *IdempotencyByRoute {
	return byroute.NewNamedFactory(
		func(routeID string, cfg config.IdempotencyConfig) (*CompiledIdempotency, error) {
			return New(routeID, cfg, redisClient)
		},
		func(ci *CompiledIdempotency) any { return ci.Status() },
	).WithClose((*CompiledIdempotency).Close)
}
