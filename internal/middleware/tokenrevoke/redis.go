package tokenrevoke

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/internal/logging"
	"go.uber.org/zap"
)

// RedisStore is a Redis-backed token revocation store.
type RedisStore struct {
	client *redis.Client
	prefix string
}

// NewRedisStore creates a new Redis-backed token store.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{
		client: client,
		prefix: "gw:revoked:",
	}
}

// Contains checks if a key exists in Redis. Fails open on errors.
func (rs *RedisStore) Contains(ctx context.Context, key string) (bool, error) {
	fullKey := rs.prefix + key
	n, err := rs.client.Exists(ctx, fullKey).Result()
	if err != nil {
		logging.Warn("Token revocation Redis error (failing open)",
			zap.String("key", fullKey),
			zap.Error(err),
		)
		return false, nil // fail open
	}
	return n > 0, nil
}

// Add stores a key in Redis with the given TTL.
func (rs *RedisStore) Add(ctx context.Context, key string, ttl time.Duration) error {
	fullKey := rs.prefix + key
	err := rs.client.Set(ctx, fullKey, "1", ttl).Err()
	if err != nil {
		logging.Warn("Token revocation Redis SET error",
			zap.String("key", fullKey),
			zap.Error(err),
		)
	}
	return err
}

// Remove deletes a key from Redis.
func (rs *RedisStore) Remove(ctx context.Context, key string) error {
	fullKey := rs.prefix + key
	err := rs.client.Del(ctx, fullKey).Err()
	if err != nil {
		logging.Warn("Token revocation Redis DEL error",
			zap.String("key", fullKey),
			zap.Error(err),
		)
	}
	return err
}

// Size returns -1 (too expensive to count in Redis).
func (rs *RedisStore) Size() int {
	return -1
}

// Close is a no-op (shared client).
func (rs *RedisStore) Close() {}
