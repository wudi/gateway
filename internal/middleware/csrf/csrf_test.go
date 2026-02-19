package csrf

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func baseCfg() config.CSRFConfig {
	return config.CSRFConfig{
		Enabled:      true,
		Secret:       "test-secret-key-32bytes!!!!!!!",
		CookieSecure: true,
		InjectToken:  true,
	}
}

func TestSafeMethodInjectsToken(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page", nil)
	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("safe method should be allowed")
	}
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "_csrf" {
			found = true
			if c.Value == "" {
				t.Error("token cookie should have a value")
			}
		}
	}
	if !found {
		t.Error("expected Set-Cookie for _csrf")
	}
}

func TestSafeMethodNoInjectWhenDisabled(t *testing.T) {
	cfg := baseCfg()
	cfg.InjectToken = false
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page", nil)
	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("safe method should be allowed")
	}
	cookies := rec.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "_csrf" {
			t.Error("should not inject token cookie when inject_token is false")
		}
	}
}

func TestPOSTMissingBothCookieAndHeader(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	allowed, code, _ := cp.Check(rec, req)
	if allowed {
		t.Fatal("POST without token should be rejected")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
}

func TestPOSTWithCookieButNoHeader(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})

	allowed, code, _ := cp.Check(rec, req)
	if allowed {
		t.Fatal("POST with cookie but no header should be rejected")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
}

func TestPOSTWithMatchingValidToken(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token)

	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("POST with matching valid token should pass")
	}
}

func TestPOSTWithMismatchedTokens(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Use a valid token for cookie, but a different string for header
	token := cp.generateToken()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token+"tampered")

	allowed, code, _ := cp.Check(rec, req)
	if allowed {
		t.Fatal("POST with mismatched tokens should be rejected")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
}

func TestExpiredToken(t *testing.T) {
	cfg := baseCfg()
	cfg.TokenTTL = 1 * time.Millisecond
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()
	time.Sleep(10 * time.Millisecond) // let token expire

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token)

	allowed, code, msg := cp.Check(rec, req)
	if allowed {
		t.Fatal("expired token should be rejected")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
	if msg != "CSRF token expired" {
		t.Errorf("expected 'CSRF token expired', got %q", msg)
	}
}

func TestTamperedHMAC(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Create a different-secret protector to generate an invalid token
	cfg2 := baseCfg()
	cfg2.Secret = "different-secret-key-32bytes!!!!"
	cp2, _ := New("r1", cfg2)
	token := cp2.generateToken()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token)

	allowed, code, msg := cp.Check(rec, req)
	if allowed {
		t.Fatal("tampered HMAC should be rejected")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
	if msg != "CSRF token invalid signature" {
		t.Errorf("expected 'CSRF token invalid signature', got %q", msg)
	}
}

func TestOriginCheckValidOrigin(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowedOrigins = []string{"https://example.com"}
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.Header.Set("Origin", "https://example.com")
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token)

	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("valid origin should pass")
	}
}

func TestOriginCheckInvalidOrigin(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowedOrigins = []string{"https://example.com"}
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.Header.Set("Origin", "https://evil.com")
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token)

	allowed, code, _ := cp.Check(rec, req)
	if allowed {
		t.Fatal("invalid origin should be rejected")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
}

func TestOriginCheckRefererFallback(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowedOrigins = []string{"https://example.com"}
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.Header.Set("Referer", "https://example.com/page?q=1")
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token)

	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("referer fallback should match origin")
	}
}

func TestOriginCheckNeitherPresent(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowedOrigins = []string{"https://example.com"}
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token)
	// no Origin, no Referer

	allowed, code, _ := cp.Check(rec, req)
	if allowed {
		t.Fatal("missing origin/referer should be rejected when origins are configured")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
}

func TestRegexOriginPattern(t *testing.T) {
	cfg := baseCfg()
	cfg.AllowedOriginPatterns = []string{`^https://.*\.example\.com$`}
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()

	// Should match
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req.Header.Set("X-CSRF-Token", token)

	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("regex pattern should match subdomain")
	}

	// Should not match
	token2 := cp.generateToken()
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/submit", nil)
	req2.Header.Set("Origin", "https://evil.com")
	req2.AddCookie(&http.Cookie{Name: "_csrf", Value: token2})
	req2.Header.Set("X-CSRF-Token", token2)

	allowed2, _, _ := cp.Check(rec2, req2)
	if allowed2 {
		t.Fatal("regex pattern should not match evil.com")
	}
}

func TestShadowMode(t *testing.T) {
	cfg := baseCfg()
	cfg.ShadowMode = true
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	// No token at all

	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("shadow mode should allow even invalid requests")
	}
	if cp.metrics.ValidationFailed.Load() == 0 {
		t.Error("should still track validation failures in shadow mode")
	}
}

func TestExemptPath(t *testing.T) {
	cfg := baseCfg()
	cfg.ExemptPaths = []string{"/api/webhook*", "/health"}
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/health", nil)
	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("exempt path should be allowed without token")
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/webhooks", nil)
	allowed2, _, _ := cp.Check(rec2, req2)
	if !allowed2 {
		t.Fatal("exempt wildcard path should be allowed")
	}
}

func TestCustomSafeMethods(t *testing.T) {
	cfg := baseCfg()
	cfg.SafeMethods = []string{"GET", "POST"}
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", nil)
	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("POST should be safe when configured as safe method")
	}

	// PUT should now require token
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("PUT", "/submit", nil)
	allowed2, code, _ := cp.Check(rec2, req2)
	if allowed2 {
		t.Fatal("PUT should not be safe")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
}

func TestMergeCSRFConfig(t *testing.T) {
	global := config.CSRFConfig{
		Enabled:      true,
		Secret:       "global-secret",
		CookieName:   "_csrf_global",
		TokenTTL:     2 * time.Hour,
		CookieSecure: true,
		InjectToken:  true,
	}
	perRoute := config.CSRFConfig{
		Enabled:    true,
		CookieName: "_csrf_route",
		TokenTTL:   30 * time.Minute,
	}

	merged := MergeCSRFConfig(perRoute, global)
	if merged.Secret != "global-secret" {
		t.Errorf("expected global secret, got %q", merged.Secret)
	}
	if merged.CookieName != "_csrf_route" {
		t.Errorf("expected per-route cookie name, got %q", merged.CookieName)
	}
	if merged.TokenTTL != 30*time.Minute {
		t.Errorf("expected per-route TTL, got %v", merged.TokenTTL)
	}
	// MergeNonZero always takes bools from overlay: per-route false overrides global true
	if merged.CookieSecure {
		t.Error("expected per-route CookieSecure=false to override global")
	}
	if merged.InjectToken {
		t.Error("expected per-route InjectToken=false to override global")
	}
}

func TestCookieAttributes(t *testing.T) {
	cfg := baseCfg()
	cfg.CookieDomain = ".example.com"
	cfg.CookiePath = "/api"
	cfg.CookieSecure = true
	cfg.CookieSameSite = "strict"
	cfg.CookieHTTPOnly = true
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page", nil)
	cp.Check(rec, req)

	cookies := rec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "_csrf" {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("expected csrf cookie")
	}
	if csrfCookie.Domain != "example.com" && csrfCookie.Domain != ".example.com" {
		t.Errorf("expected domain example.com, got %q", csrfCookie.Domain)
	}
	if csrfCookie.Path != "/api" {
		t.Errorf("expected path /api, got %q", csrfCookie.Path)
	}
	if !csrfCookie.Secure {
		t.Error("expected Secure flag")
	}
	if !csrfCookie.HttpOnly {
		t.Error("expected HttpOnly flag")
	}
	if csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("expected SameSiteStrictMode, got %v", csrfCookie.SameSite)
	}
}

func TestMetricsIncrement(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	// GET → token generated
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	cp.Check(rec, req)
	if cp.metrics.TotalRequests.Load() != 1 {
		t.Errorf("expected 1 total, got %d", cp.metrics.TotalRequests.Load())
	}
	if cp.metrics.TokenGenerated.Load() != 1 {
		t.Errorf("expected 1 token generated, got %d", cp.metrics.TokenGenerated.Load())
	}

	// POST no token → missing + failed
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/", nil)
	cp.Check(rec2, req2)
	if cp.metrics.MissingToken.Load() != 1 {
		t.Errorf("expected 1 missing token, got %d", cp.metrics.MissingToken.Load())
	}
	if cp.metrics.ValidationFailed.Load() != 1 {
		t.Errorf("expected 1 validation failed, got %d", cp.metrics.ValidationFailed.Load())
	}

	// POST with valid token → success
	token := cp.generateToken()
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/", nil)
	req3.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req3.Header.Set("X-CSRF-Token", token)
	cp.Check(rec3, req3)
	if cp.metrics.ValidationSuccess.Load() != 1 {
		t.Errorf("expected 1 validation success, got %d", cp.metrics.ValidationSuccess.Load())
	}
}

func TestHEADAndOPTIONSAreSafe(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{"HEAD", "OPTIONS", "TRACE"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/", nil)
		allowed, _, _ := cp.Check(rec, req)
		if !allowed {
			t.Errorf("%s should be safe", method)
		}
	}
}

func TestCustomHeaderAndCookieName(t *testing.T) {
	cfg := baseCfg()
	cfg.CookieName = "my_csrf"
	cfg.HeaderName = "X-My-Token"
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	token := cp.generateToken()

	// Should work with custom names
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: "my_csrf", Value: token})
	req.Header.Set("X-My-Token", token)
	allowed, _, _ := cp.Check(rec, req)
	if !allowed {
		t.Fatal("should pass with custom header/cookie names")
	}

	// Should fail with default names
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/", nil)
	req2.AddCookie(&http.Cookie{Name: "_csrf", Value: token})
	req2.Header.Set("X-CSRF-Token", token)
	allowed2, _, _ := cp.Check(rec2, req2)
	if allowed2 {
		t.Fatal("should fail with wrong cookie/header names")
	}
}

func TestDELETERequiresToken(t *testing.T) {
	cfg := baseCfg()
	cp, err := New("r1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/resource/1", nil)
	allowed, code, _ := cp.Check(rec, req)
	if allowed {
		t.Fatal("DELETE should require CSRF token")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
}
