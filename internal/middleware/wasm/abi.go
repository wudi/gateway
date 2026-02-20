package wasm

// Action constants returned by on_request / on_response guest exports.
const (
	ActionContinue     = 0 // proceed to next plugin / next middleware
	ActionPause        = 1 // reserved for future body buffering
	ActionSendResponse = 2 // guest called host_send_response; terminate request
)

// MapType constants for host_get_header / host_set_header / host_remove_header.
const (
	MapTypeRequestHeaders  = 0
	MapTypeResponseHeaders = 1
)

// Log level constants for host_log.
const (
	LogLevelTrace = 0
	LogLevelDebug = 1
	LogLevelInfo  = 2
	LogLevelWarn  = 3
	LogLevelError = 4
)

// RequestContext is serialized as JSON and written to guest memory for on_request.
type RequestContext struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Host     string            `json:"host"`
	Scheme   string            `json:"scheme"`
	RouteID  string            `json:"route_id"`
	BodySize int               `json:"body_size"`
	Headers  map[string]string `json:"headers"`
	Config   map[string]string `json:"config,omitempty"`
}

// ResponseContext is serialized as JSON and written to guest memory for on_response.
type ResponseContext struct {
	StatusCode int               `json:"status_code"`
	BodySize   int               `json:"body_size"`
	RouteID    string            `json:"route_id"`
	Headers    map[string]string `json:"headers"`
	Config     map[string]string `json:"config,omitempty"`
}

// EarlyResponse captures a guest-initiated response via host_send_response.
type EarlyResponse struct {
	StatusCode int
	Body       []byte
}
