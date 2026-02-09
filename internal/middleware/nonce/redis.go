package nonce

import (
	"context"
	"time"

	"github.com/example/gateway/internal/logging"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisStore is a Redis-backed NonceStore using SET NX PX for atomic check-and-store.
type RedisStore struct {
	client *redis.Client
	prefix string // key prefix, e.g. "gw:nonce:{routeID}:"
}

// NewRedisStore creates a new Redis-backed nonce store.
func NewRedisStore(client *redis.Client, routeID string) *RedisStore {
	return &RedisStore{
		client: client,
		prefix: "gw:nonce:" + routeID + ":",
	}
}

// CheckAndStore atomically checks if the nonce exists in Redis using SET NX PX.
// Returns true if new (allowed), false if duplicate (replay).
// Fails open on Redis errors (logs warning, allows request).
func (rs *RedisStore) CheckAndStore(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	fullKey := rs.prefix + key
	ok, err := rs.client.SetNX(ctx, fullKey, "1", ttl).Result()
	if err != nil {
		logging.Warn("Nonce Redis error (failing open)",
			zap.String("key", fullKey),
			zap.Error(err),
		)
		return true, nil // fail open
	}
	return ok, nil
}

// Size returns -1 because counting keys in Redis is expensive.
func (rs *RedisStore) Size() int {
	return -1
}

// Close is a no-op — the Redis client is shared and managed externally.
func (rs *RedisStore) Close() {}
