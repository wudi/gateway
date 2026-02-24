package mirror

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/logging"
	"go.uber.org/zap"
)

// Mirror handles traffic mirroring/shadowing for a route
type Mirror struct {
	enabled       bool
	backends      []string
	percentage    int
	client        *http.Client
	conditions    *Conditions
	compare       bool
	logMismatch   bool
	detailedDiff  bool
	diffConfig    *DiffConfig
	mismatchStore *MismatchStore
	metrics       *MirrorMetrics
}

// New creates a new Mirror from config
func New(cfg config.MirrorConfig) (*Mirror, error) {
	m := &Mirror{
		enabled:     cfg.Enabled,
		percentage:  cfg.Percentage,
		compare:     cfg.Compare.Enabled,
		logMismatch: cfg.Compare.LogMismatches,
		metrics:     NewMirrorMetrics(),
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

	// Set up detailed diff mode
	if cfg.Compare.DetailedDiff {
		m.detailedDiff = true
		m.diffConfig = NewDiffConfig(cfg.Compare)
		m.mismatchStore = NewMismatchStore(cfg.Compare.MaxMismatches)
	}

	// Compile conditions
	hasConds := len(cfg.Conditions.Methods) > 0 || len(cfg.Conditions.Headers) > 0 || cfg.Conditions.PathRegex != ""
	if hasConds {
		conds, err := NewConditions(cfg.Conditions)
		if err != nil {
			return nil, err
		}
		m.conditions = conds
	}

	return m, nil
}

// IsEnabled returns whether mirroring is enabled
func (m *Mirror) IsEnabled() bool {
	return m.enabled && len(m.backends) > 0
}

// ShouldMirror returns whether this request should be mirrored
func (m *Mirror) ShouldMirror(r *http.Request) bool {
	if !m.IsEnabled() {
		return false
	}
	// Check conditions first
	if m.conditions != nil && !m.conditions.Match(r) {
		return false
	}
	if m.percentage >= 100 {
		return true
	}
	return rand.Intn(100) < m.percentage
}

// CompareEnabled returns whether response comparison is enabled.
func (m *Mirror) CompareEnabled() bool {
	return m.compare
}

// GetMetrics returns the mirror metrics.
func (m *Mirror) GetMetrics() *MirrorMetrics {
	return m.metrics
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

// SendAsync sends mirrored requests asynchronously (fire-and-forget).
// If primary is non-nil and compare is enabled, responses are compared.
func (m *Mirror) SendAsync(r *http.Request, body []byte, primary *PrimaryResponse) {
	for _, backend := range m.backends {
		go m.sendMirrorWithMetrics(r, backend, body, primary)
	}
}

// SendAsyncDetailed sends mirrored requests with detailed diff comparison.
func (m *Mirror) SendAsyncDetailed(r *http.Request, body []byte, primary *PrimaryDiffResponse) {
	for _, backend := range m.backends {
		go m.sendMirrorDetailed(r, backend, body, primary)
	}
}

func (m *Mirror) sendMirrorWithMetrics(original *http.Request, backendURL string, body []byte, primary *PrimaryResponse) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	targetURL, err := url.Parse(backendURL)
	if err != nil {
		m.metrics.RecordError()
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
		m.metrics.RecordError()
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

	resp, err := m.client.Do(req)
	if err != nil {
		m.metrics.RecordError()
		return
	}

	latency := time.Since(start)

	// Compare if enabled and primary is available
	if m.compare && primary != nil {
		result := CompareMirrorResponse(primary, resp)
		m.metrics.RecordComparison(result)
		if m.logMismatch && (!result.StatusMatch || !result.BodyMatch) {
			logging.Warn("mirror mismatch",
				zap.String("route", original.Host),
				zap.String("path", original.URL.Path),
				zap.Bool("status_match", result.StatusMatch),
				zap.Bool("body_match", result.BodyMatch),
				zap.Int("primary_status", primary.StatusCode),
				zap.Int("mirror_status", resp.StatusCode),
			)
		}
	} else {
		io.Copy(io.Discard, resp.Body)
	}
	resp.Body.Close()

	m.metrics.RecordSuccess(latency)
}

func (m *Mirror) sendMirrorDetailed(original *http.Request, backendURL string, body []byte, primary *PrimaryDiffResponse) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	targetURL, err := url.Parse(backendURL)
	if err != nil {
		m.metrics.RecordError()
		return
	}

	mirrorURL := *targetURL
	mirrorURL.Path = original.URL.Path
	mirrorURL.RawQuery = original.URL.RawQuery

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, original.Method, mirrorURL.String(), bodyReader)
	if err != nil {
		m.metrics.RecordError()
		return
	}

	for k, vv := range original.Header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("X-Mirrored-From", original.Host)

	resp, err := m.client.Do(req)
	if err != nil {
		m.metrics.RecordError()
		return
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	result, detail := CompareMirrorResponseDetailed(primary, resp, m.diffConfig)
	m.metrics.RecordDetailedComparison(result, detail)

	if detail.HasDiffs() {
		entry := MismatchEntry{
			Timestamp: time.Now(),
			Method:    original.Method,
			Path:      original.URL.Path,
			Backend:   backendURL,
			Detail:    *detail,
			DiffTypes: detail.DiffTypes(),
		}
		m.mismatchStore.Add(entry)

		if m.logMismatch {
			logging.Warn("mirror detailed mismatch",
				zap.String("path", original.URL.Path),
				zap.String("backend", backendURL),
				zap.Bool("status_match", result.StatusMatch),
				zap.Bool("body_match", result.BodyMatch),
				zap.Strings("diff_types", detail.DiffTypes()),
				zap.Int("primary_status", primary.StatusCode),
				zap.Int("mirror_status", resp.StatusCode),
			)
		}
	}

	m.metrics.RecordSuccess(latency)
}

// GetMismatchStore returns the mismatch store (nil if detailed diff is not enabled).
func (m *Mirror) GetMismatchStore() *MismatchStore {
	return m.mismatchStore
}

// MirrorByRoute manages mirrors per route
type MirrorByRoute struct {
	byroute.Manager[*Mirror]
}

// NewMirrorByRoute creates a new per-route mirror manager
func NewMirrorByRoute() *MirrorByRoute {
	return &MirrorByRoute{}
}

// AddRoute adds a mirror for a route
func (m *MirrorByRoute) AddRoute(routeID string, cfg config.MirrorConfig) error {
	mirror, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, mirror)
	return nil
}

// GetMirror returns the mirror for a route
func (m *MirrorByRoute) GetMirror(routeID string) *Mirror {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns a snapshot of metrics for all routes.
func (m *MirrorByRoute) Stats() map[string]MirrorSnapshot {
	return byroute.CollectStats(&m.Manager, func(mir *Mirror) MirrorSnapshot {
		snap := mir.metrics.Snapshot()
		if mir.mismatchStore != nil {
			snap.MismatchStoreSize = mir.mismatchStore.Size()
		}
		return snap
	})
}

// GetMismatchSnapshot returns a mismatch snapshot for a route.
func (m *MirrorByRoute) GetMismatchSnapshot(routeID string) *MismatchSnapshot {
	mir := m.GetMirror(routeID)
	if mir == nil || mir.mismatchStore == nil {
		return nil
	}
	snap := mir.mismatchStore.Snapshot()
	return &snap
}

// ClearMismatches clears the mismatch store for a route.
func (m *MirrorByRoute) ClearMismatches(routeID string) bool {
	mir := m.GetMirror(routeID)
	if mir == nil || mir.mismatchStore == nil {
		return false
	}
	mir.mismatchStore.Clear()
	return true
}

// Middleware returns a middleware that buffers the request body and sends mirrored requests async.
func (m *Mirror) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !m.ShouldMirror(r) {
				next.ServeHTTP(w, r)
				return
			}

			mirrorBody, err := BufferRequestBody(r)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			if m.detailedDiff {
				dw := NewDiffCapturingWriter(w, m.diffConfig.maxBodyCapture)
				next.ServeHTTP(dw, r)
				primary := &PrimaryDiffResponse{
					StatusCode: dw.StatusCode(),
					Headers:    dw.CapturedHeaders(),
					Body:       dw.CapturedBody(),
					BodyHash:   dw.BodyHash(),
					Truncated:  dw.BodyTruncated(),
				}
				m.SendAsyncDetailed(r, mirrorBody, primary)
			} else if m.CompareEnabled() {
				cw := NewCapturingWriter(w)
				next.ServeHTTP(cw, r)
				primary := &PrimaryResponse{
					StatusCode: cw.StatusCode(),
					BodyHash:   cw.BodyHash(),
				}
				m.SendAsync(r, mirrorBody, primary)
			} else {
				next.ServeHTTP(w, r)
				m.SendAsync(r, mirrorBody, nil)
			}
		})
	}
}
