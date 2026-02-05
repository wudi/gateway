package rules

import (
	"net/http"

	"github.com/example/gateway/internal/config"
)

// ExecuteTerminatingAction writes the appropriate response for a terminating action.
func ExecuteTerminatingAction(w http.ResponseWriter, r *http.Request, action Action) {
	switch action.Type {
	case "block":
		status := action.StatusCode
		if status == 0 {
			status = http.StatusForbidden
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		if action.Body != "" {
			w.Write([]byte(action.Body))
		} else {
			w.Write([]byte(http.StatusText(status)))
		}

	case "custom_response":
		status := action.StatusCode
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		if action.Body != "" {
			w.Write([]byte(action.Body))
		}

	case "redirect":
		status := action.StatusCode
		if status == 0 {
			status = http.StatusFound
		}
		http.Redirect(w, r, action.RedirectURL, status)
	}
}

// ExecuteRequestHeaders modifies request headers in-place (non-terminating).
func ExecuteRequestHeaders(r *http.Request, headers config.HeaderTransform) {
	for k, v := range headers.Add {
		r.Header.Add(k, v)
	}
	for k, v := range headers.Set {
		r.Header.Set(k, v)
	}
	for _, k := range headers.Remove {
		r.Header.Del(k)
	}
}

// ExecuteResponseHeaders modifies response headers (non-terminating).
func ExecuteResponseHeaders(w http.ResponseWriter, headers config.HeaderTransform) {
	for k, v := range headers.Add {
		w.Header().Add(k, v)
	}
	for k, v := range headers.Set {
		w.Header().Set(k, v)
	}
	for _, k := range headers.Remove {
		w.Header().Del(k)
	}
}
