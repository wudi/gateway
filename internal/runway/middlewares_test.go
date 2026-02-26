package runway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/runway/internal/cache"
	"github.com/wudi/runway/internal/circuitbreaker"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/metrics"
	"github.com/wudi/runway/internal/middleware/compression"
	"github.com/wudi/runway/internal/middleware/cors"
	"github.com/wudi/runway/internal/middleware/ipfilter"
	"github.com/wudi/runway/internal/middleware/ratelimit"
	"github.com/wudi/runway/internal/middleware/transform"
	"github.com/wudi/runway/internal/middleware/validation"
	"github.com/wudi/runway/internal/rules"
	"github.com/wudi/runway/variables"
)

// ok200 is a simple handler that returns 200 OK with a JSON body.
func ok200() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
}

// --- ipFilterMW ---

func TestIPFilterMW_GlobalDeny(t *testing.T) {
	f, err := ipfilter.New(config.IPFilterConfig{
		Enabled: true,
		Deny:    []string{"192.168.1.0/24"},
		Order:   "deny_first",
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := ipFilterMW(f, nil)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestIPFilterMW_RouteAllow(t *testing.T) {
	routeFilter, err := ipfilter.New(config.IPFilterConfig{
		Enabled: true,
		Allow:   []string{"10.0.0.0/8"},
		Order:   "allow_first",
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := ipFilterMW(nil, routeFilter)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestIPFilterMW_NilFilters(t *testing.T) {
	mw := ipFilterMW(nil, nil)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// --- corsMW ---

func TestCorsMW_Preflight(t *testing.T) {
	h, err := cors.New(config.CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"https://example.com"},
		AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"Content-Type"},
		MaxAge:       3600,
	})
	if err != nil {
		t.Fatal(err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	mw := h.Middleware()
	handler := mw(next)

	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if called {
		t.Error("next handler should not be called on preflight")
	}
	if w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("expected Access-Control-Allow-Origin header")
	}
}

func TestCorsMW_NormalRequest(t *testing.T) {
	h, err := cors.New(config.CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET"},
	})
	if err != nil {
		t.Fatal(err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})

	mw := h.Middleware()
	handler := mw(next)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next handler should be called on normal request")
	}
}

// --- rateLimitMW ---

func TestRateLimitMW_AllowAndReject(t *testing.T) {
	limiter := ratelimit.NewLimiter(ratelimit.Config{
		Rate:   2,
		Period: time.Second,
		Burst:  2,
		PerIP:  true,
	})

	mw := limiter.Middleware()
	handler := mw(ok200())

	// First 2 requests should pass
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, w.Code)
		}
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

// --- bodyLimitMW ---

func TestBodyLimitMW_Reject(t *testing.T) {
	mw := bodyLimitMW(100)
	handler := mw(ok200())

	req := httptest.NewRequest("POST", "/test", strings.NewReader("x"))
	req.ContentLength = 200
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", w.Code)
	}
}

func TestBodyLimitMW_Allow(t *testing.T) {
	mw := bodyLimitMW(1024)
	handler := mw(ok200())

	body := strings.NewReader("hello")
	req := httptest.NewRequest("POST", "/test", body)
	req.ContentLength = 5
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// --- validationMW ---

func TestValidationMW_Reject(t *testing.T) {
	v, err := validation.New(config.ValidationConfig{
		Enabled: true,
		Schema:  `{"required":["name"],"properties":{"name":{"type":"string"}}}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	handler := mw(ok200())

	// POST with empty JSON body — missing required "name" field
	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestValidationMW_Allow(t *testing.T) {
	v, err := validation.New(config.ValidationConfig{
		Enabled: true,
		Schema:  `{"required":["name"],"properties":{"name":{"type":"string"}}}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	handler := mw(ok200())

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// --- cacheMW ---

func TestCacheMW_MissThenHit(t *testing.T) {
	h := cache.NewHandler(config.CacheConfig{
		Enabled:     true,
		TTL:         5 * time.Second,
		MaxSize:     100,
		MaxBodySize: 1 << 20,
		Methods:     []string{"GET"},
	}, cache.NewMemoryStore(100, 5*time.Second))
	mc := metrics.NewCollector()

	backendCalls := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"data":"backend"}`))
	})

	mw := cacheMW(h, mc, "test-route")
	handler := mw(backend)

	// First request — MISS
	req := httptest.NewRequest("GET", "/data", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("MISS: expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Cache") != "MISS" {
		t.Errorf("expected X-Cache: MISS, got %q", w.Header().Get("X-Cache"))
	}
	if backendCalls != 1 {
		t.Errorf("expected 1 backend call, got %d", backendCalls)
	}

	// Second request — HIT
	req2 := httptest.NewRequest("GET", "/data", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Errorf("HIT: expected 200, got %d", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache: HIT, got %q", w2.Header().Get("X-Cache"))
	}
	if backendCalls != 1 {
		t.Errorf("expected no additional backend call, got %d", backendCalls)
	}
}

func TestCacheMW_SkipNonCacheable(t *testing.T) {
	h := cache.NewHandler(config.CacheConfig{
		Enabled: true,
		Methods: []string{"GET"},
	}, cache.NewMemoryStore(1000, 60*time.Second))
	mc := metrics.NewCollector()

	mw := cacheMW(h, mc, "test-route")
	handler := mw(ok200())

	// POST should not be cached
	req := httptest.NewRequest("POST", "/data", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Cache") != "" {
		t.Errorf("expected no X-Cache header on POST, got %q", w.Header().Get("X-Cache"))
	}
}

// --- circuitBreakerMW ---

func TestCircuitBreakerMW_Closed(t *testing.T) {
	cb := circuitbreaker.NewBreaker(config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 5,
		MaxRequests:      1,
		Timeout:          5 * time.Second,
	}, nil)

	mw := circuitBreakerMW(cb, false)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 when circuit closed, got %d", w.Code)
	}
}

func TestCircuitBreakerMW_Open(t *testing.T) {
	cb := circuitbreaker.NewBreaker(config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 2,
		MaxRequests:      1,
		Timeout:          5 * time.Second,
	}, nil)

	// Trip the breaker with failures
	failHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})
	mw := circuitBreakerMW(cb, false)
	handler := mw(failHandler)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	// Now circuit should be open
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when circuit open, got %d", w.Code)
	}
}

func TestCircuitBreakerMW_ReportsSuccess(t *testing.T) {
	cb := circuitbreaker.NewBreaker(config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 5,
		MaxRequests:      1,
		Timeout:          5 * time.Second,
	}, nil)

	mw := circuitBreakerMW(cb, false)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	snap := cb.Snapshot()
	if snap.State != "closed" {
		t.Errorf("expected closed, got %s", snap.State)
	}
}

// --- compressionMW ---

func TestCompressionMW_Compresses(t *testing.T) {
	c := compression.New(config.CompressionConfig{
		Enabled:      true,
		Level:        6,
		MinSize:      0, // compress everything
		ContentTypes: []string{"application/json"},
	})

	largeBody := strings.Repeat(`{"data":"value"},`, 100)
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(largeBody))
	})

	mw := c.Middleware()
	handler := mw(backend)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	// br is preferred by server when both are accepted
	if w.Header().Get("Content-Encoding") != "br" {
		t.Errorf("expected Content-Encoding: br, got %q", w.Header().Get("Content-Encoding"))
	}
}

func TestCompressionMW_SkipsNoAcceptEncoding(t *testing.T) {
	c := compression.New(config.CompressionConfig{
		Enabled: true,
		Level:   6,
	})

	mw := c.Middleware()
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	// No Accept-Encoding header
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if enc := w.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("should not compress without Accept-Encoding, got %q", enc)
	}
}

func TestCompressionMW_NegotiatesGzip(t *testing.T) {
	c := compression.New(config.CompressionConfig{
		Enabled:      true,
		Level:        6,
		MinSize:      0,
		ContentTypes: []string{"application/json"},
	})

	largeBody := strings.Repeat(`{"data":"value"},`, 100)
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(largeBody))
	})

	mw := c.Middleware()
	handler := mw(backend)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %q", w.Header().Get("Content-Encoding"))
	}
}

// --- ResponseBodyTransformMiddleware ---

func TestResponseBodyTransformMW_AddFields(t *testing.T) {
	cfg := config.BodyTransformConfig{
		AddFields: map[string]string{"added": "value"},
	}

	ct, err := transform.NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile transform: %v", err)
	}
	mw := transform.ResponseBodyTransformMiddleware(ct)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if result["added"] != "value" {
		t.Errorf("expected added=value, got %v", result["added"])
	}
	if result["status"] != "ok" {
		t.Errorf("expected original status=ok, got %v", result["status"])
	}
}

func TestResponseBodyTransformMW_RemoveFields(t *testing.T) {
	cfg := config.BodyTransformConfig{
		RemoveFields: []string{"status"},
	}

	ct, err := transform.NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile transform: %v", err)
	}
	mw := transform.ResponseBodyTransformMiddleware(ct)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if _, ok := result["status"]; ok {
		t.Error("expected status field to be removed")
	}
}

func TestResponseBodyTransformMW_RenameFields(t *testing.T) {
	cfg := config.BodyTransformConfig{
		RenameFields: map[string]string{"status": "state"},
	}

	ct, err := transform.NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile transform: %v", err)
	}
	mw := transform.ResponseBodyTransformMiddleware(ct)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if _, ok := result["status"]; ok {
		t.Error("expected old field name to be gone")
	}
	if result["state"] != "ok" {
		t.Errorf("expected state=ok, got %v", result["state"])
	}
}

// --- metricsMW ---

func TestMetricsMW_Records(t *testing.T) {
	mc := metrics.NewCollector()

	mw := metricsMW(mc, "metrics-route")
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	snap := mc.Snapshot()
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
}

func TestMetricsMW_CapturesStatus(t *testing.T) {
	mc := metrics.NewCollector()

	fail := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})

	mw := metricsMW(mc, "fail-route")
	handler := mw(fail)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// The statusRecorder captures 500 but the real writer also gets 500
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- varContextMW ---

func TestVarContextMW(t *testing.T) {
	var capturedRouteID string
	var capturedParams map[string]string

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.GetFromRequest(r)
		capturedRouteID = varCtx.RouteID
		capturedParams = varCtx.PathParams
		w.WriteHeader(200)
	})

	mw := varContextMW("my-route")
	handler := mw(next)

	req := httptest.NewRequest("GET", "/test", nil)
	// Simulate serveHTTP: set PathParams on the existing varCtx before handler runs.
	varCtx := variables.NewContext(req)
	varCtx.PathParams = map[string]string{"id": "42"}
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if capturedRouteID != "my-route" {
		t.Errorf("expected RouteID=my-route, got %q", capturedRouteID)
	}
	if capturedParams["id"] != "42" {
		t.Errorf("expected PathParams[id]=42, got %q", capturedParams["id"])
	}
}

// --- responseRulesMW ---

func TestResponseRulesMW_SetHeaders(t *testing.T) {
	engine, err := rules.NewEngine(nil, []config.RuleConfig{
		{
			Expression: "true",
			Action:     "set_headers",
			Headers: config.HeaderTransform{
				Set: map[string]string{"X-Custom": "injected"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := responseRulesMW(nil, engine)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	// Add variable context so rules can evaluate
	varCtx := variables.NewContext(req)
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("X-Custom") != "injected" {
		t.Errorf("expected X-Custom: injected, got %q", w.Header().Get("X-Custom"))
	}
}

func TestResponseRulesMW_NilEngines(t *testing.T) {
	mw := responseRulesMW(nil, nil)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	varCtx := variables.NewContext(req)
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// --- requestRulesMW ---

func TestRequestRulesMW_Block(t *testing.T) {
	engine, err := rules.NewEngine([]config.RuleConfig{
		{
			Expression: `http.request.method == "POST"`,
			Action:     "block",
			StatusCode: 403,
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	mw := requestRulesMW(engine, nil)
	handler := mw(next)

	req := httptest.NewRequest("POST", "/test", nil)
	varCtx := variables.NewContext(req)
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if called {
		t.Error("next should not be called when rule blocks")
	}
	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRequestRulesMW_PassThrough(t *testing.T) {
	engine, err := rules.NewEngine([]config.RuleConfig{
		{
			Expression: `http.request.method == "DELETE"`,
			Action:     "block",
			StatusCode: 403,
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	mw := requestRulesMW(engine, nil)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)
	varCtx := variables.NewContext(req)
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// --- CompiledBodyTransform ---

func TestCompiledBodyTransform(t *testing.T) {
	body := []byte(`{"name":"alice","age":30}`)
	cfg := config.BodyTransformConfig{
		AddFields:    map[string]string{"role": "admin"},
		RemoveFields: []string{"age"},
		RenameFields: map[string]string{"name": "username"},
	}

	ct, err := transform.NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile transform: %v", err)
	}

	result := ct.Transform(body, nil)

	var data map[string]interface{}
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if data["role"] != "admin" {
		t.Errorf("expected role=admin, got %v", data["role"])
	}
	if _, ok := data["age"]; ok {
		t.Error("expected age to be removed")
	}
	if _, ok := data["name"]; ok {
		t.Error("expected name to be renamed")
	}
	if data["username"] != "alice" {
		t.Errorf("expected username=alice, got %v", data["username"])
	}
}

func TestCompiledBodyTransform_InvalidJSON(t *testing.T) {
	body := []byte(`not json`)
	cfg := config.BodyTransformConfig{
		AddFields: map[string]string{"key": "val"},
	}

	ct, err := transform.NewCompiledBodyTransform(cfg)
	if err != nil {
		t.Fatalf("failed to compile transform: %v", err)
	}

	result := ct.Transform(body, nil)

	if !bytes.Equal(result, body) {
		t.Errorf("expected original body returned for invalid JSON")
	}
}

// --- statusRecorder ---

func TestStatusRecorder(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, statusCode: 200}

	rec.WriteHeader(404)

	if rec.StatusCode() != 404 {
		t.Errorf("expected 404, got %d", rec.StatusCode())
	}
	if w.Code != 404 {
		t.Errorf("underlying writer: expected 404, got %d", w.Code)
	}
}

// Ensure unused imports are satisfied.
var _ = io.Discard
