package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/example/gateway/internal/logging"
)

// RedisStore is a Redis-backed cache store implementing Store.
type RedisStore struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

// NewRedisStore creates a new Redis-backed store.
// prefix should include the route ID, e.g. "gw:cache:myroute:".
func NewRedisStore(client *redis.Client, prefix string, ttl time.Duration) *RedisStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &RedisStore{
		client: client,
		prefix: prefix,
		ttl:    ttl,
	}
}

func init() {
	// Register http.Header for gob encoding (it's a map[string][]string).
	gob.Register(http.Header{})
}

func (s *RedisStore) Get(key string) (*Entry, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	data, err := s.client.Get(ctx, s.prefix+key).Bytes()
	if err != nil {
		if err != redis.Nil {
			logging.Warn("Redis cache get failed, treating as miss", zap.Error(err))
		}
		return nil, false
	}

	var entry Entry
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&entry); err != nil {
		logging.Warn("Redis cache decode failed, treating as miss", zap.Error(err))
		return nil, false
	}
	return &entry, true
}

func (s *RedisStore) Set(key string, entry *Entry) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(entry); err != nil {
		logging.Warn("Redis cache encode failed", zap.Error(err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := s.client.Set(ctx, s.prefix+key, buf.Bytes(), s.ttl).Err(); err != nil {
		logging.Warn("Redis cache set failed", zap.Error(err))
	}
}

func (s *RedisStore) Delete(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := s.client.Del(ctx, s.prefix+key).Err(); err != nil {
		logging.Warn("Redis cache delete failed", zap.Error(err))
	}
}

func (s *RedisStore) DeleteByPrefix(prefix string) {
	s.scanAndDelete(s.prefix + prefix)
}

func (s *RedisStore) Purge() {
	s.scanAndDelete(s.prefix)
}

func (s *RedisStore) scanAndDelete(pattern string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cursor uint64
	for {
		keys, next, err := s.client.Scan(ctx, cursor, pattern+"*", 100).Result()
		if err != nil {
			logging.Warn("Redis cache scan failed", zap.Error(err))
			return
		}
		if len(keys) > 0 {
			if err := s.client.Del(ctx, keys...).Err(); err != nil {
				logging.Warn("Redis cache bulk delete failed", zap.Error(err))
				return
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func (s *RedisStore) Stats() StoreStats {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var count int
	var cursor uint64
	for {
		keys, next, err := s.client.Scan(ctx, cursor, s.prefix+"*", 100).Result()
		if err != nil {
			logging.Warn("Redis cache stats scan failed", zap.Error(err))
			return StoreStats{}
		}
		count += len(keys)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return StoreStats{Size: count}
}
