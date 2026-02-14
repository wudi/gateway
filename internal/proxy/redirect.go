package proxy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// RedirectTransport wraps an http.RoundTripper and follows 3xx redirects
// up to a configurable maximum.
type RedirectTransport struct {
	inner        http.RoundTripper
	maxRedirects int

	followed    atomic.Int64
	maxExceeded atomic.Int64
}

// NewRedirectTransport creates a transport that follows 3xx redirects.
// maxRedirects defaults to 10 if <= 0.
func NewRedirectTransport(inner http.RoundTripper, maxRedirects int) *RedirectTransport {
	if maxRedirects <= 0 {
		maxRedirects = 10
	}
	return &RedirectTransport{
		inner:        inner,
		maxRedirects: maxRedirects,
	}
}

// RoundTrip implements http.RoundTripper with redirect following.
func (rt *RedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var redirectCount int
	current := req

	for {
		resp, err := rt.inner.RoundTrip(current)
		if err != nil {
			return nil, err
		}

		if !isRedirect(resp.StatusCode) {
			return resp, nil
		}

		redirectCount++
		if redirectCount > rt.maxRedirects {
			rt.maxExceeded.Add(1)
			return resp, nil
		}

		rt.followed.Add(1)

		loc := resp.Header.Get("Location")
		if loc == "" {
			return resp, nil
		}

		// Drain and close the redirect response body
		resp.Body.Close()

		nextURL, err := resolveRedirectURL(current.URL, loc)
		if err != nil {
			return nil, fmt.Errorf("invalid redirect location %q: %w", loc, err)
		}

		// Build next request
		method := current.Method
		if resp.StatusCode == http.StatusSeeOther {
			method = http.MethodGet
		}

		next, err := http.NewRequestWithContext(current.Context(), method, nextURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create redirect request: %w", err)
		}

		// Copy original headers
		for k, vv := range req.Header {
			for _, v := range vv {
				next.Header.Add(k, v)
			}
		}

		// Don't send body on GET/HEAD
		if method == http.MethodGet || method == http.MethodHead {
			next.Body = nil
			next.ContentLength = 0
		}

		current = next
	}
}

// Stats returns redirect statistics.
func (rt *RedirectTransport) Stats() map[string]interface{} {
	return map[string]interface{}{
		"redirects_followed": rt.followed.Load(),
		"max_exceeded":       rt.maxExceeded.Load(),
		"max_redirects":      rt.maxRedirects,
	}
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, // 301
		http.StatusFound,         // 302
		http.StatusSeeOther,      // 303
		http.StatusTemporaryRedirect, // 307
		http.StatusPermanentRedirect: // 308
		return true
	}
	return false
}

func resolveRedirectURL(base *url.URL, location string) (*url.URL, error) {
	loc, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	if loc.IsAbs() {
		return loc, nil
	}
	// Handle protocol-relative URLs
	if strings.HasPrefix(location, "//") {
		loc.Scheme = base.Scheme
		return loc, nil
	}
	return base.ResolveReference(loc), nil
}
