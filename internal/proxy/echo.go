package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

const echoMaxBodySize = 1 << 20 // 1MB

// echoHandler returns request details as JSON without proxying to a backend.
type echoHandler struct {
	routeID string
}

// NewEchoHandler creates an http.Handler that echoes back request details as JSON.
// Created once per route during initialization.
func NewEchoHandler(routeID string) http.Handler {
	return &echoHandler{routeID: routeID}
}

type echoResponse struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Host       string            `json:"host"`
	RemoteAddr string            `json:"remote_addr"`
	Query      map[string]string `json:"query"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body,omitempty"`
	Timestamp  string            `json:"timestamp"`
	RouteID    string            `json:"route_id"`
}

func (h *echoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Flatten query params to first value
	query := make(map[string]string, len(r.URL.Query()))
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}

	// Flatten headers to first value
	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	// Read body with size cap
	var body string
	if r.Body != nil {
		data, _ := io.ReadAll(io.LimitReader(r.Body, echoMaxBodySize))
		body = string(data)
	}

	resp := echoResponse{
		Method:     r.Method,
		Path:       r.URL.Path,
		Host:       r.Host,
		RemoteAddr: r.RemoteAddr,
		Query:      query,
		Headers:    headers,
		Body:       body,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		RouteID:    h.routeID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
