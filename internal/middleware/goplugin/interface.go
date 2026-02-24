package goplugin

import "net/http"

// PluginRequest represents the request data passed to plugin OnRequest/OnResponse.
type PluginRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body,omitempty"`
}

// PluginResponse represents the response data returned by the plugin.
type PluginResponse struct {
	Action     string            `json:"action"` // "continue", "send_response"
	StatusCode int               `json:"status_code,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
}

// GatewayPlugin is the interface that Go plugins must implement.
type GatewayPlugin interface {
	// Init is called once when the plugin starts, with arbitrary config.
	Init(config map[string]string) error

	// OnRequest is called for each incoming request (request phase).
	OnRequest(req PluginRequest) PluginResponse

	// OnResponse is called after the backend responds (response phase).
	OnResponse(req PluginRequest, statusCode int, respHeaders map[string]string, respBody []byte) PluginResponse
}

// extractHeaders extracts headers from an HTTP request into a flat map.
func extractHeaders(h http.Header) map[string]string {
	result := make(map[string]string, len(h))
	for k := range h {
		result[k] = h.Get(k)
	}
	return result
}
