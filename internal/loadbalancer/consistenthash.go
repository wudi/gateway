package loadbalancer

import (
	"crypto/md5"
	"encoding/binary"
	"net"
	"net/http"
	"sort"
	"sync"

	"github.com/wudi/runway/config"
)

// ConsistentHash implements a consistent hash (ketama) load balancer.
// Requests with the same key always go to the same backend.
type ConsistentHash struct {
	baseBalancer
	cfg      config.ConsistentHashConfig
	ring     []ringEntry
	ringMu   sync.RWMutex
	replicas int
}

type ringEntry struct {
	hash    uint32
	backend *Backend
}

// NewConsistentHash creates a new consistent hash balancer.
func NewConsistentHash(backends []*Backend, cfg config.ConsistentHashConfig) *ConsistentHash {
	replicas := cfg.Replicas
	if replicas <= 0 {
		replicas = 150
	}
	ch := &ConsistentHash{
		cfg:      cfg,
		replicas: replicas,
	}
	for _, b := range backends {
		if b.Weight == 0 {
			b.Weight = 1
		}
	}
	ch.backends = backends
	ch.buildIndex()
	ch.rebuildRing()
	return ch
}

// rebuildRing rebuilds the hash ring from healthy backends (caller need NOT hold ringMu).
func (ch *ConsistentHash) rebuildRing() {
	ch.mu.RLock()
	healthy := ch.healthyBackends()
	ch.mu.RUnlock()

	var ring []ringEntry
	for _, b := range healthy {
		vnodes := ch.replicas * b.Weight
		for i := 0; i < vnodes; i++ {
			h := ketamaHash(b.URL, i)
			ring = append(ring, ringEntry{hash: h, backend: b})
		}
	}

	sort.Slice(ring, func(i, j int) bool {
		return ring[i].hash < ring[j].hash
	})

	ch.ringMu.Lock()
	ch.ring = ring
	ch.ringMu.Unlock()
}

// ketamaHash produces a uint32 hash for a backend URL and virtual node index.
func ketamaHash(key string, idx int) uint32 {
	// Use MD5 for ketama compatibility
	data := make([]byte, len(key)+4)
	copy(data, key)
	binary.LittleEndian.PutUint32(data[len(key):], uint32(idx))
	sum := md5.Sum(data)
	return binary.LittleEndian.Uint32(sum[:4])
}

// hashKey produces a uint32 from an arbitrary string.
func hashKey(key string) uint32 {
	sum := md5.Sum([]byte(key))
	return binary.LittleEndian.Uint32(sum[:4])
}

// Next returns a backend using a default key (not request-aware). Falls back to first ring entry.
func (ch *ConsistentHash) Next() *Backend {
	ch.ringMu.RLock()
	defer ch.ringMu.RUnlock()

	if len(ch.ring) == 0 {
		return nil
	}
	return ch.ring[0].backend
}

// NextForHTTPRequest selects a backend based on the configured hash key extracted from the request.
func (ch *ConsistentHash) NextForHTTPRequest(r *http.Request) (*Backend, string) {
	key := ch.extractKey(r)
	h := hashKey(key)

	ch.ringMu.RLock()
	ring := ch.ring
	ch.ringMu.RUnlock()

	if len(ring) == 0 {
		return nil, ""
	}

	// Binary search for the first entry with hash >= h
	idx := sort.Search(len(ring), func(i int) bool {
		return ring[i].hash >= h
	})
	if idx >= len(ring) {
		idx = 0 // wrap around
	}

	return ring[idx].backend, ""
}

// extractKey extracts the hash key from the request based on configuration.
func (ch *ConsistentHash) extractKey(r *http.Request) string {
	switch ch.cfg.Key {
	case "header":
		return r.Header.Get(ch.cfg.HeaderName)
	case "cookie":
		if c, err := r.Cookie(ch.cfg.HeaderName); err == nil {
			return c.Value
		}
		return ""
	case "path":
		return r.URL.Path
	case "ip":
		return extractClientIP(r)
	default:
		return r.URL.Path
	}
}

// extractClientIP extracts the client IP from X-Forwarded-For or RemoteAddr.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// UpdateBackends updates backends and rebuilds the ring.
func (ch *ConsistentHash) UpdateBackends(backends []*Backend) {
	ch.baseBalancer.UpdateBackends(backends)
	ch.rebuildRing()
}

// MarkHealthy marks a backend healthy and rebuilds the ring.
func (ch *ConsistentHash) MarkHealthy(url string) {
	ch.baseBalancer.MarkHealthy(url)
	ch.rebuildRing()
}

// MarkUnhealthy marks a backend unhealthy and rebuilds the ring.
func (ch *ConsistentHash) MarkUnhealthy(url string) {
	ch.baseBalancer.MarkUnhealthy(url)
	ch.rebuildRing()
}
