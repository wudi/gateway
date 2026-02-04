package mirror

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/example/gateway/internal/config"
)

// Mirror handles traffic mirroring/shadowing for a route
type Mirror struct {
	enabled    bool
	backends   []string
	percentage int
	client     *http.Client
}

// New creates a new Mirror from config
func New(cfg config.MirrorConfig) *Mirror {
	m := &Mirror{
		enabled:    cfg.Enabled,
		percentage: cfg.Percentage,
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}

	for _, b := range cfg.Backends {
		m.backends = append(m.backends, b.URL)
	}

	if m.percentage <= 0 {
		m.percentage = 100
	}

	return m
}

// IsEnabled returns whether mirroring is enabled
func (m *Mirror) IsEnabled() bool {
	return m.enabled && len(m.backends) > 0
}

// ShouldMirror returns whether this request should be mirrored (based on percentage)
func (m *Mirror) ShouldMirror() bool {
	if !m.IsEnabled() {
		return false
	}
	if m.percentage >= 100 {
		return true
	}
	return rand.Intn(100) < m.percentage
}

// BufferRequestBody reads and returns the request body, replacing it on the original request
func BufferRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// SendAsync sends mirrored requests asynchronously (fire-and-forget)
func (m *Mirror) SendAsync(r *http.Request, body []byte) {
	for _, backend := range m.backends {
		go m.sendMirror(r, backend, body)
	}
}

func (m *Mirror) sendMirror(original *http.Request, backendURL string, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	targetURL, err := url.Parse(backendURL)
	if err != nil {
		return
	}

	// Build the mirrored URL
	mirrorURL := *targetURL
	mirrorURL.Path = original.URL.Path
	mirrorURL.RawQuery = original.URL.RawQuery

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, original.Method, mirrorURL.String(), bodyReader)
	if err != nil {
		return
	}

	// Copy relevant headers
	for k, vv := range original.Header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}

	// Mark as mirrored
	req.Header.Set("X-Mirrored-From", original.Host)

	// Fire and forget — ignore response
	resp, err := m.client.Do(req)
	if err != nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// MirrorByRoute manages mirrors per route
type MirrorByRoute struct {
	mirrors map[string]*Mirror
	mu      sync.RWMutex
}

// NewMirrorByRoute creates a new per-route mirror manager
func NewMirrorByRoute() *MirrorByRoute {
	return &MirrorByRoute{
		mirrors: make(map[string]*Mirror),
	}
}

// AddRoute adds a mirror for a route
func (m *MirrorByRoute) AddRoute(routeID string, cfg config.MirrorConfig) {
	m.mu.Lock()
	m.mirrors[routeID] = New(cfg)
	m.mu.Unlock()
}

// GetMirror returns the mirror for a route
func (m *MirrorByRoute) GetMirror(routeID string) *Mirror {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mirrors[routeID]
}
