package accesslog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestParseStatusRange(t *testing.T) {
	tests := []struct {
		input string
		lo    int
		hi    int
		err   bool
	}{
		{"200", 200, 200, false},
		{"4xx", 400, 499, false},
		{"5xx", 500, 599, false},
		{"2xx", 200, 299, false},
		{"200-299", 200, 299, false},
		{"400-499", 400, 499, false},
		{"0xx", 0, 0, true},
		{"abc", 0, 0, true},
		{"600", 0, 0, true},
		{"99", 0, 0, true},
		{"500-400", 0, 0, true}, // lo > hi
	}

	for _, tt := range tests {
		sr, err := ParseStatusRange(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("ParseStatusRange(%q) expected error, got %v", tt.input, sr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseStatusRange(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if sr.Lo != tt.lo || sr.Hi != tt.hi {
			t.Errorf("ParseStatusRange(%q) = [%d, %d], want [%d, %d]", tt.input, sr.Lo, sr.Hi, tt.lo, tt.hi)
		}
	}
}

func TestShouldLog_StatusCodes(t *testing.T) {
	c, err := New(config.AccessLogConfig{
		Conditions: config.AccessLogConditions{
			StatusCodes: []string{"4xx", "5xx"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !c.ShouldLog(404, "GET") {
		t.Error("ShouldLog(404) = false, want true")
	}
	if !c.ShouldLog(500, "POST") {
		t.Error("ShouldLog(500) = false, want true")
	}
	if c.ShouldLog(200, "GET") {
		t.Error("ShouldLog(200) = true, want false")
	}
	if c.ShouldLog(301, "GET") {
		t.Error("ShouldLog(301) = true, want false")
	}
}

func TestShouldLog_Methods(t *testing.T) {
	c, err := New(config.AccessLogConfig{
		Conditions: config.AccessLogConditions{
			Methods: []string{"POST", "DELETE"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !c.ShouldLog(200, "POST") {
		t.Error("ShouldLog(POST) = false, want true")
	}
	if !c.ShouldLog(200, "DELETE") {
		t.Error("ShouldLog(DELETE) = false, want true")
	}
	if c.ShouldLog(200, "GET") {
		t.Error("ShouldLog(GET) = true, want false")
	}
}

func TestShouldLog_Sampling(t *testing.T) {
	c, err := New(config.AccessLogConfig{
		Conditions: config.AccessLogConditions{
			SampleRate: 0.0, // 0 means log all
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Sample rate 0 = log all
	for i := 0; i < 100; i++ {
		if !c.ShouldLog(200, "GET") {
			t.Fatal("ShouldLog with sample_rate=0 should always return true")
		}
	}

	// Sample rate 1.0 = log all
	c2, _ := New(config.AccessLogConfig{
		Conditions: config.AccessLogConditions{
			SampleRate: 1.0,
		},
	})
	for i := 0; i < 100; i++ {
		if !c2.ShouldLog(200, "GET") {
			t.Fatal("ShouldLog with sample_rate=1.0 should always return true")
		}
	}
}

func TestShouldLog_NoConditions(t *testing.T) {
	c, err := New(config.AccessLogConfig{})
	if err != nil {
		t.Fatal(err)
	}

	// No conditions = always log
	if !c.ShouldLog(200, "GET") {
		t.Error("ShouldLog with no conditions should return true")
	}
	if !c.ShouldLog(500, "POST") {
		t.Error("ShouldLog with no conditions should return true")
	}
}

func TestMaskHeaderValue(t *testing.T) {
	c, err := New(config.AccessLogConfig{
		SensitiveHeaders: []string{"X-Custom-Secret"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Default sensitive
	if v := c.MaskHeaderValue("Authorization", "Bearer token"); v != "***" {
		t.Errorf("MaskHeaderValue(Authorization) = %q, want ***", v)
	}
	if v := c.MaskHeaderValue("Cookie", "session=abc"); v != "***" {
		t.Errorf("MaskHeaderValue(Cookie) = %q, want ***", v)
	}
	if v := c.MaskHeaderValue("Set-Cookie", "foo=bar"); v != "***" {
		t.Errorf("MaskHeaderValue(Set-Cookie) = %q, want ***", v)
	}
	if v := c.MaskHeaderValue("X-Api-Key", "secret123"); v != "***" {
		t.Errorf("MaskHeaderValue(X-Api-Key) = %q, want ***", v)
	}

	// User-configured sensitive
	if v := c.MaskHeaderValue("X-Custom-Secret", "value"); v != "***" {
		t.Errorf("MaskHeaderValue(X-Custom-Secret) = %q, want ***", v)
	}

	// Non-sensitive
	if v := c.MaskHeaderValue("Content-Type", "application/json"); v != "application/json" {
		t.Errorf("MaskHeaderValue(Content-Type) = %q, want application/json", v)
	}
}

func TestCaptureRequestHeaders(t *testing.T) {
	c, err := New(config.AccessLogConfig{
		HeadersInclude: []string{"Content-Type", "Authorization", "X-Request-Id"},
	})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Request-Id", "abc123")
	r.Header.Set("X-Other", "should-not-appear")

	headers := c.CaptureRequestHeaders(r)
	if headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q", headers["Content-Type"])
	}
	if headers["Authorization"] != "***" {
		t.Errorf("Authorization should be masked, got %q", headers["Authorization"])
	}
	if headers["X-Request-Id"] != "abc123" {
		t.Errorf("X-Request-Id = %q", headers["X-Request-Id"])
	}
	if _, ok := headers["X-Other"]; ok {
		t.Error("X-Other should not be captured")
	}
}

func TestCaptureResponseHeaders(t *testing.T) {
	c, err := New(config.AccessLogConfig{
		HeadersExclude: []string{"Set-Cookie"},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := http.Header{}
	h.Set("Content-Type", "text/html")
	h.Set("Set-Cookie", "session=abc")
	h.Set("X-Custom", "value")

	captured := c.CaptureResponseHeaders(h)
	if _, ok := captured["Set-Cookie"]; ok {
		t.Error("Set-Cookie should be excluded")
	}
	if captured["Content-Type"] != "text/html" {
		t.Errorf("Content-Type = %q", captured["Content-Type"])
	}
	if captured["X-Custom"] != "value" {
		t.Errorf("X-Custom = %q", captured["X-Custom"])
	}
}

func TestNew_Defaults(t *testing.T) {
	// Default body max size
	c, err := New(config.AccessLogConfig{
		Body: config.AccessLogBodyConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Body.MaxSize != 4096 {
		t.Errorf("default max_size = %d, want 4096", c.Body.MaxSize)
	}
}

func TestNew_InvalidStatusRange(t *testing.T) {
	_, err := New(config.AccessLogConfig{
		Conditions: config.AccessLogConditions{
			StatusCodes: []string{"abc"},
		},
	})
	if err == nil {
		t.Error("expected error for invalid status range")
	}
}

func TestShouldCaptureBody(t *testing.T) {
	c, err := New(config.AccessLogConfig{
		Body: config.AccessLogBodyConfig{
			Enabled:      true,
			ContentTypes: []string{"application/json", "text/plain"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !c.ShouldCaptureBody("application/json") {
		t.Error("ShouldCaptureBody(application/json) = false, want true")
	}
	if !c.ShouldCaptureBody("application/json; charset=utf-8") {
		t.Error("ShouldCaptureBody with charset = false, want true")
	}
	if !c.ShouldCaptureBody("text/plain") {
		t.Error("ShouldCaptureBody(text/plain) = false, want true")
	}
	if c.ShouldCaptureBody("text/html") {
		t.Error("ShouldCaptureBody(text/html) = true, want false")
	}

	// No content type filter = capture all
	c2, _ := New(config.AccessLogConfig{
		Body: config.AccessLogBodyConfig{Enabled: true},
	})
	if !c2.ShouldCaptureBody("anything/whatever") {
		t.Error("ShouldCaptureBody with no filter should return true")
	}

	// Body disabled
	c3, _ := New(config.AccessLogConfig{})
	if c3.ShouldCaptureBody("application/json") {
		t.Error("ShouldCaptureBody when disabled should return false")
	}
}

func TestEnabledFlag(t *testing.T) {
	f := false
	c, err := New(config.AccessLogConfig{
		Enabled: &f,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled == nil || *c.Enabled != false {
		t.Error("Enabled should be false")
	}
}
