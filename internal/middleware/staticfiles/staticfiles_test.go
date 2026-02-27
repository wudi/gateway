package staticfiles

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create test files.
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>index</html>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "page.html"), []byte("<html>page</html>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "index.html"), []byte("<html>sub index</html>"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStaticFileHandler_ServeFile(t *testing.T) {
	dir := setupTestDir(t)
	h, err := New("test", dir, "", false, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/style.css", nil)
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "body{}" {
		t.Errorf("expected body 'body{}', got %q", w.Body.String())
	}
	if h.served.Load() != 1 {
		t.Errorf("expected served=1, got %d", h.served.Load())
	}
}

func TestStaticFileHandler_Index(t *testing.T) {
	dir := setupTestDir(t)
	h, err := New("test", dir, "", false, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "<html>index</html>" {
		t.Errorf("expected index content, got %q", w.Body.String())
	}
}

func TestStaticFileHandler_SubdirIndex(t *testing.T) {
	dir := setupTestDir(t)
	h, err := New("test", dir, "", false, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Request to /sub/ should serve /sub/index.html
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/sub/", nil)
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestStaticFileHandler_DirNoBrowse(t *testing.T) {
	dir := t.TempDir()
	// Create a subdir with no index file.
	if err := os.MkdirAll(filepath.Join(dir, "noindex"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "noindex", "data.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := New("test", dir, "", false, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/noindex/", nil)
	h.ServeHTTP(w, r)

	if w.Code != 403 {
		t.Errorf("expected 403 for dir without index and browse=false, got %d", w.Code)
	}
}

func TestStaticFileHandler_DirBrowse(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := New("test", dir, "", true, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/files/", nil)
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200 for dir with browse=true, got %d", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("expected directory listing body")
	}
}

func TestStaticFileHandler_PathTraversal(t *testing.T) {
	dir := setupTestDir(t)
	h, err := New("test", dir, "", false, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/../etc/passwd", nil)
	h.ServeHTTP(w, r)

	if w.Code != 403 {
		t.Errorf("expected 403 for path traversal, got %d", w.Code)
	}
}

func TestStaticFileHandler_NotFound(t *testing.T) {
	dir := setupTestDir(t)
	h, err := New("test", dir, "", false, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/nonexistent.txt", nil)
	h.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestStaticFileHandler_CacheControl(t *testing.T) {
	dir := setupTestDir(t)
	h, err := New("test", dir, "", false, "public, max-age=3600")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/style.css", nil)
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("expected Cache-Control header, got %q", got)
	}
}

func TestStaticFileHandler_InvalidRoot(t *testing.T) {
	_, err := New("test", "/nonexistent/path/xyz", "", false, "")
	if err == nil {
		t.Fatal("expected error for nonexistent root")
	}
}

func TestStaticFileHandler_RootNotDir(t *testing.T) {
	f, err := os.CreateTemp("", "statictest")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	_, err = New("test", f.Name(), "", false, "")
	if err == nil {
		t.Fatal("expected error for file root")
	}
}

func TestStaticByRoute(t *testing.T) {
	dir := setupTestDir(t)
	m := NewStaticByRoute()

	err := m.AddRoute("r1", dir, "", false, "")
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}

	if h := m.Lookup("r1"); h == nil {
		t.Fatal("expected handler for r1")
	}
	if h := m.Lookup("nonexistent"); h != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "r1" {
		t.Errorf("expected [r1], got %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["r1"]; !ok {
		t.Error("expected stats for r1")
	}
}

func TestStaticFileHandler_Stats(t *testing.T) {
	dir := setupTestDir(t)
	h, err := New("test", dir, "", true, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stats := h.Stats()
	if stats["served"].(int64) != 0 {
		t.Errorf("expected 0 served, got %v", stats["served"])
	}
	if stats["browse"].(bool) != true {
		t.Errorf("expected browse=true, got %v", stats["browse"])
	}
}
