package coalesce

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/graphql"
	"golang.org/x/sync/singleflight"
)

// Response captures a buffered HTTP response for replay to multiple callers.
type Response struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// Stats holds coalescing metrics.
type Stats struct {
	GroupsCreated     int64 `json:"groups_created"`
	RequestsCoalesced int64 `json:"requests_coalesced"`
	Timeouts          int64 `json:"timeouts"`
	InFlight          int64 `json:"in_flight"`
}

// Coalescer deduplicates concurrent identical requests using singleflight.
type Coalescer struct {
	group      singleflight.Group
	timeout    time.Duration
	methods    map[string]bool
	keyHeaders []string

	groupsCreated     atomic.Int64
	requestsCoalesced atomic.Int64
	timeouts          atomic.Int64
	inFlight          atomic.Int64
}

// New creates a Coalescer from config.
func New(cfg config.CoalesceConfig) *Coalescer {
	methods := make(map[string]bool)
	if len(cfg.Methods) > 0 {
		for _, m := range cfg.Methods {
			methods[strings.ToUpper(m)] = true
		}
	} else {
		methods["GET"] = true
		methods["HEAD"] = true
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// Pre-sort keyHeaders so BuildKey doesn't need to copy+sort per request
	keyHeaders := make([]string, len(cfg.KeyHeaders))
	copy(keyHeaders, cfg.KeyHeaders)
	sort.Strings(keyHeaders)

	return &Coalescer{
		timeout:    timeout,
		methods:    methods,
		keyHeaders: keyHeaders,
	}
}

// ShouldCoalesce returns true if the request method is eligible for coalescing.
func (c *Coalescer) ShouldCoalesce(r *http.Request) bool {
	return c.methods[r.Method]
}

// BuildKey constructs a deterministic coalesce key from the request.
// keyHeaders are pre-sorted at construction time.
func (c *Coalescer) BuildKey(r *http.Request) string {
	h := sha256.New()
	io.WriteString(h, r.Method)
	h.Write([]byte{'\n'})
	io.WriteString(h, r.URL.Path)
	h.Write([]byte{'\n'})
	io.WriteString(h, r.URL.RawQuery)
	h.Write([]byte{'\n'})

	for _, hdr := range c.keyHeaders {
		io.WriteString(h, hdr)
		h.Write([]byte{':'})
		io.WriteString(h, r.Header.Get(hdr))
		h.Write([]byte{'\n'})
	}

	// Include GraphQL info if present
	if info := graphql.GetInfo(r.Context()); info != nil {
		io.WriteString(h, "gql:")
		io.WriteString(h, info.OperationName)
		h.Write([]byte{'|'})
		io.WriteString(h, info.VariablesHash)
		h.Write([]byte{'\n'})
	}

	return hex.EncodeToString(h.Sum(nil))
}

// Execute runs fn via singleflight, sharing the result with concurrent callers.
// Returns the response and whether this caller shared a result (true = coalesced).
// If the coalesce timeout expires, the caller falls through to fn directly.
func (c *Coalescer) Execute(ctx context.Context, key string, fn func() (*Response, error)) (*Response, bool, error) {
	c.inFlight.Add(1)
	defer c.inFlight.Add(-1)

	ch := c.group.DoChan(key, func() (interface{}, error) {
		c.groupsCreated.Add(1)
		// Use a context detached from the original caller so one client
		// disconnecting doesn't cancel the shared backend call.
		// Values (tracing spans, variables) are preserved.
		return fn()
	})

	select {
	case result := <-ch:
		if result.Err != nil {
			return nil, false, result.Err
		}
		resp := result.Val.(*Response)
		if result.Shared {
			c.requestsCoalesced.Add(1)
		}
		return resp, result.Shared, nil

	case <-time.After(c.timeout):
		// Timeout: forget the key so future callers don't wait on the same group,
		// then fall through to direct execution.
		c.group.Forget(key)
		c.timeouts.Add(1)
		resp, err := fn()
		return resp, false, err

	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

// Stats returns a snapshot of coalescing metrics.
func (c *Coalescer) Stats() Stats {
	return Stats{
		GroupsCreated:     c.groupsCreated.Load(),
		RequestsCoalesced: c.requestsCoalesced.Load(),
		Timeouts:          c.timeouts.Load(),
		InFlight:          c.inFlight.Load(),
	}
}

// bufferingWriter captures an HTTP response without forwarding to the client.
type bufferingWriter struct {
	statusCode int
	header     http.Header
	body       bytes.Buffer
}

func newBufferingWriter() *bufferingWriter {
	return &bufferingWriter{
		statusCode: 200,
		header:     make(http.Header),
	}
}

func (w *bufferingWriter) Header() http.Header {
	return w.header
}

func (w *bufferingWriter) WriteHeader(code int) {
	w.statusCode = code
}

func (w *bufferingWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

// Response converts the captured data to a Response.
func (w *bufferingWriter) Response() *Response {
	return &Response{
		StatusCode: w.statusCode,
		Headers:    w.header.Clone(),
		Body:       w.body.Bytes(),
	}
}

// WriteResponse replays a captured response to a real ResponseWriter.
// If shared is true, an X-Coalesced: true header is added.
func WriteResponse(w http.ResponseWriter, resp *Response, shared bool) {
	for k, vv := range resp.Headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	if shared {
		w.Header().Set("X-Coalesced", "true")
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(resp.Body)
}

// ServeCoalesced is a convenience that runs next.ServeHTTP through the coalescer.
// It captures the response into a buffer, shares it with concurrent callers, and replays it.
func (c *Coalescer) ServeCoalesced(w http.ResponseWriter, r *http.Request, next http.Handler) {
	key := c.BuildKey(r)

	// Detach context from caller cancellation but preserve values
	detachedCtx := context.WithoutCancel(r.Context())

	resp, shared, err := c.Execute(r.Context(), key, func() (*Response, error) {
		bw := newBufferingWriter()
		next.ServeHTTP(bw, r.WithContext(detachedCtx))
		return bw.Response(), nil
	})
	if err != nil {
		http.Error(w, "coalesce error", http.StatusBadGateway)
		return
	}

	WriteResponse(w, resp, shared)
}

// CoalesceByRoute manages per-route Coalescers.
type CoalesceByRoute struct {
	byroute.Manager[*Coalescer]
}

// NewCoalesceByRoute creates a new CoalesceByRoute manager.
func NewCoalesceByRoute() *CoalesceByRoute {
	return &CoalesceByRoute{}
}

// AddRoute adds a Coalescer for the given route.
func (m *CoalesceByRoute) AddRoute(routeID string, cfg config.CoalesceConfig) {
	m.Add(routeID, New(cfg))
}

// GetCoalescer returns the Coalescer for a route, or nil if not configured.
func (m *CoalesceByRoute) GetCoalescer(routeID string) *Coalescer {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route coalescing metrics.
func (m *CoalesceByRoute) Stats() map[string]Stats {
	return byroute.CollectStats(&m.Manager, func(c *Coalescer) Stats { return c.Stats() })
}

// Middleware returns a middleware that deduplicates concurrent identical requests via singleflight.
func (c *Coalescer) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !c.ShouldCoalesce(r) {
				next.ServeHTTP(w, r)
				return
			}
			c.ServeCoalesced(w, r, next)
		})
	}
}
