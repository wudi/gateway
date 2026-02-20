package dedup

import (
	"bytes"
	"context"
	"encoding/gob"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/logging"
	"go.uber.org/zap"
)

func init() {
	gob.Register(http.Header{})
}

// RedisStore is a Redis-backed dedup store.
type RedisStore struct {
	client *redis.Client
	prefix string
}

// NewRedisStore creates a new Redis-backed dedup store.
func NewRedisStore(client *redis.Client, prefix string) *RedisStore {
	return &RedisStore{
		client: client,
		prefix: prefix,
	}
}

func (s *RedisStore) Get(ctx context.Context, key string) (*StoredResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	data, err := s.client.Get(ctx, s.prefix+key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		logging.Warn("Redis dedup get failed, treating as miss", zap.Error(err))
		return nil, nil // fail-open
	}

	var resp StoredResponse
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&resp); err != nil {
		logging.Warn("Redis dedup decode failed, treating as miss", zap.Error(err))
		return nil, nil
	}
	return &resp, nil
}

func (s *RedisStore) Set(ctx context.Context, key string, resp *StoredResponse, ttl time.Duration) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(resp); err != nil {
		logging.Warn("Redis dedup encode failed", zap.Error(err))
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	if err := s.client.Set(ctx, s.prefix+key, buf.Bytes(), ttl).Err(); err != nil {
		logging.Warn("Redis dedup set failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *RedisStore) Close() {
	// Redis client is shared — don't close it here.
}
