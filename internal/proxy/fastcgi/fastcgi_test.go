package fastcgi

import (
	"io"
	"net"
	"net/http"
	"net/http/fcgi"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestNew_RequiredFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.FastCGIConfig
		want string
	}{
		{
			name: "missing address",
			cfg:  config.FastCGIConfig{Address: "", DocumentRoot: "/var/www"},
			want: "address is required",
		},
		{
			name: "missing document_root",
			cfg:  config.FastCGIConfig{Address: "127.0.0.1:9000", DocumentRoot: ""},
			want: "document_root is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New("test-route", tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); got != "fastcgi: "+tt.want {
				t.Errorf("error = %q, want containing %q", got, tt.want)
			}
		})
	}
}

func TestNew_NetworkAutoDetection(t *testing.T) {
	tests := []struct {
		addr    string
		want    string
	}{
		{"/var/run/php.sock", "unix"},
		{"/run/php-fpm.sock", "unix"},
		{"custom.sock", "unix"},
		{"127.0.0.1:9000", "tcp"},
		{"localhost:9000", "tcp"},
		{"[::1]:9000", "tcp"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := detectNetwork(tt.addr)
			if got != tt.want {
				t.Errorf("detectNetwork(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestNew_NetworkExplicitOverride(t *testing.T) {
	// Verify explicit network overrides auto-detection.
	cfg := config.FastCGIConfig{
		Address:      "127.0.0.1:9000",
		Network:      "unix", // explicit override
		DocumentRoot: "/var/www",
	}
	h, err := New("test", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if h.network != "unix" {
		t.Errorf("network = %q, want %q", h.network, "unix")
	}
}

func TestNew_Defaults(t *testing.T) {
	cfg := config.FastCGIConfig{
		Address:      "127.0.0.1:9000",
		DocumentRoot: "/var/www",
	}
	h, err := New("test", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if h.index != "index.php" {
		t.Errorf("index = %q, want %q", h.index, "index.php")
	}
	if h.network != "tcp" {
		t.Errorf("network = %q, want %q", h.network, "tcp")
	}
}

func TestFastCGIByRoute(t *testing.T) {
	m := NewFastCGIByRoute()

	// Initially empty.
	if ids := m.RouteIDs(); len(ids) != 0 {
		t.Errorf("expected 0 route IDs, got %d", len(ids))
	}
	if h := m.GetHandler("nonexistent"); h != nil {
		t.Error("expected nil handler for nonexistent route")
	}

	// Add a route.
	err := m.AddRoute("route1", config.FastCGIConfig{
		Address:      "127.0.0.1:9000",
		DocumentRoot: "/var/www",
	})
	if err != nil {
		t.Fatal(err)
	}

	if h := m.GetHandler("route1"); h == nil {
		t.Error("expected handler for route1, got nil")
	}
	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("RouteIDs = %v, want [route1]", ids)
	}

	// Stats should include route1.
	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("stats missing route1")
	}
}

func TestHandler_ServeHTTP(t *testing.T) {
	// Start a mock FastCGI server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var mu sync.Mutex
	var receivedParams map[string]string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedParams = make(map[string]string)
		// In an fcgi server, CGI params come through environment / request.
		// The fcgi package sends response body normally.
		receivedParams["REQUEST_METHOD"] = r.Method
		receivedParams["REQUEST_URI"] = r.RequestURI
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from FastCGI"))
	})

	go fcgi.Serve(ln, handler)

	// Give server time to start.
	time.Sleep(50 * time.Millisecond)

	cfg := config.FastCGIConfig{
		Address:      ln.Addr().String(),
		DocumentRoot: "/var/www/html",
		ScriptName:   "/index.php",
		PoolSize:     2,
		ConnTimeout:  2 * time.Second,
		ReadTimeout:  5 * time.Second,
		Params:       map[string]string{"HTTPS": "on"},
	}

	h, err := New("test-fcgi", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/hello?foo=bar", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := string(body); got != "Hello from FastCGI" {
		t.Errorf("body = %q, want %q", got, "Hello from FastCGI")
	}
}

func TestHandler_Stats(t *testing.T) {
	// Start a mock FastCGI server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	time.Sleep(50 * time.Millisecond)

	cfg := config.FastCGIConfig{
		Address:      ln.Addr().String(),
		DocumentRoot: "/var/www/html",
		ScriptName:   "/index.php",
	}
	h, err := New("stats-route", cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Before any requests.
	stats := h.Stats()
	if stats["total_requests"].(int64) != 0 {
		t.Errorf("initial total_requests = %d, want 0", stats["total_requests"])
	}

	// Make a request.
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// After one request.
	stats = h.Stats()
	if stats["total_requests"].(int64) != 1 {
		t.Errorf("total_requests = %d, want 1", stats["total_requests"])
	}
	if stats["address"].(string) != ln.Addr().String() {
		t.Errorf("address = %q, want %q", stats["address"], ln.Addr().String())
	}
	if stats["document_root"].(string) != "/var/www/html" {
		t.Errorf("document_root = %q, want %q", stats["document_root"], "/var/www/html")
	}
	if stats["script_name"].(string) != "/index.php" {
		t.Errorf("script_name = %q, want %q", stats["script_name"], "/index.php")
	}
}

func TestHandler_FilesystemMode(t *testing.T) {
	// Start a mock FastCGI server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("filesystem mode"))
	}))
	time.Sleep(50 * time.Millisecond)

	// Filesystem mode (no ScriptName).
	cfg := config.FastCGIConfig{
		Address:      ln.Addr().String(),
		DocumentRoot: "/var/www/html",
	}
	h, err := New("fs-route", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if h.scriptName != "" {
		t.Errorf("scriptName = %q, want empty", h.scriptName)
	}

	req := httptest.NewRequest("GET", "/info.php", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Result().Body)
	if got := string(body); got != "filesystem mode" {
		t.Errorf("body = %q, want %q", got, "filesystem mode")
	}
}
