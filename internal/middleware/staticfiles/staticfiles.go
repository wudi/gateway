package staticfiles

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// StaticFileHandler serves static files from a directory.
type StaticFileHandler struct {
	routeID      string
	root         string
	index        string
	browse       bool
	cacheControl string
	fileServer   http.Handler
	served       atomic.Int64
}

// New creates a StaticFileHandler from config.
func New(routeID, root, index string, browse bool, cacheControl string) (*StaticFileHandler, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root path: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("root directory %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root %q is not a directory", absRoot)
	}
	if index == "" {
		index = "index.html"
	}
	return &StaticFileHandler{
		routeID:      routeID,
		root:         absRoot,
		index:        index,
		browse:       browse,
		cacheControl: cacheControl,
		fileServer:   http.FileServer(http.Dir(absRoot)),
	}, nil
}

// ServeHTTP serves static files.
func (h *StaticFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Reject path traversal attempts.
	if strings.Contains(r.URL.Path, "..") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Clean the path.
	cleanPath := filepath.Clean(r.URL.Path)
	if cleanPath == "." {
		cleanPath = "/"
	}
	fullPath := filepath.Join(h.root, cleanPath)

	// Check if path exists.
	info, err := os.Stat(fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Handle directory access.
	if info.IsDir() {
		// Try serving index file.
		indexPath := filepath.Join(fullPath, h.index)
		if _, err := os.Stat(indexPath); err == nil {
			// Index exists — let FileServer handle it.
		} else if !h.browse {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		// If browse is enabled, let FileServer show directory listing.
	}

	if h.cacheControl != "" {
		w.Header().Set("Cache-Control", h.cacheControl)
	}
	h.served.Add(1)
	h.fileServer.ServeHTTP(w, r)
}

// Stats returns file serving statistics.
func (h *StaticFileHandler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"root":   h.root,
		"served": h.served.Load(),
		"browse": h.browse,
	}
}

// StaticByRoute manages per-route static file handlers.
type StaticByRoute struct {
	handlers map[string]*StaticFileHandler
	mu       sync.RWMutex
}

// NewStaticByRoute creates a new per-route static file manager.
func NewStaticByRoute() *StaticByRoute {
	return &StaticByRoute{}
}

// AddRoute adds a static file handler for a route.
func (m *StaticByRoute) AddRoute(routeID string, root, index string, browse bool, cacheControl string) error {
	h, err := New(routeID, root, index, browse, cacheControl)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.handlers == nil {
		m.handlers = make(map[string]*StaticFileHandler)
	}
	m.handlers[routeID] = h
	m.mu.Unlock()
	return nil
}

// GetHandler returns the static file handler for a route.
func (m *StaticByRoute) GetHandler(routeID string) *StaticFileHandler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handlers[routeID]
}

// RouteIDs returns all route IDs with static file serving configured.
func (m *StaticByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.handlers))
	for id := range m.handlers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route static file stats.
func (m *StaticByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := make(map[string]interface{}, len(m.handlers))
	for id, h := range m.handlers {
		stats[id] = h.Stats()
	}
	return stats
}
