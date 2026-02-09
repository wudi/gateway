package csrf

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/logging"
	"go.uber.org/zap"
)

// CompiledCSRF is a compiled per-route CSRF protector created once during route setup.
type CompiledCSRF struct {
	secret          []byte
	cookieName      string
	headerName      string
	tokenTTL        time.Duration
	safeMethods     map[string]bool
	allowedOrigins  map[string]bool
	allowedPatterns []*regexp.Regexp
	cookiePath      string
	cookieDomain    string
	cookieSecure    bool
	cookieSameSite  http.SameSite
	cookieHTTPOnly  bool
	injectToken     bool
	shadowMode      bool
	exemptPaths     []string
	metrics         *CSRFMetrics
	routeID         string
}

// New creates a new CompiledCSRF from config.
func New(routeID string, cfg config.CSRFConfig) (*CompiledCSRF, error) {
	cookieName := cfg.CookieName
	if cookieName == "" {
		cookieName = "_csrf"
	}
	headerName := cfg.HeaderName
	if headerName == "" {
		headerName = "X-CSRF-Token"
	}
	tokenTTL := cfg.TokenTTL
	if tokenTTL == 0 {
		tokenTTL = time.Hour
	}
	cookiePath := cfg.CookiePath
	if cookiePath == "" {
		cookiePath = "/"
	}

	safeMethods := make(map[string]bool)
	if len(cfg.SafeMethods) > 0 {
		for _, m := range cfg.SafeMethods {
			safeMethods[strings.ToUpper(m)] = true
		}
	} else {
		safeMethods["GET"] = true
		safeMethods["HEAD"] = true
		safeMethods["OPTIONS"] = true
		safeMethods["TRACE"] = true
	}

	sameSite := http.SameSiteLaxMode
	switch strings.ToLower(cfg.CookieSameSite) {
	case "strict":
		sameSite = http.SameSiteStrictMode
	case "none":
		sameSite = http.SameSiteNoneMode
	case "lax", "":
		sameSite = http.SameSiteLaxMode
	}

	allowedOrigins := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowedOrigins[o] = true
	}

	var patterns []*regexp.Regexp
	for _, p := range cfg.AllowedOriginPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("csrf: invalid origin pattern %q: %w", p, err)
		}
		patterns = append(patterns, re)
	}

	return &CompiledCSRF{
		secret:          []byte(cfg.Secret),
		cookieName:      cookieName,
		headerName:      headerName,
		tokenTTL:        tokenTTL,
		safeMethods:     safeMethods,
		allowedOrigins:  allowedOrigins,
		allowedPatterns: patterns,
		cookiePath:      cookiePath,
		cookieDomain:    cfg.CookieDomain,
		cookieSecure:    cfg.CookieSecure,
		cookieSameSite:  sameSite,
		cookieHTTPOnly:  cfg.CookieHTTPOnly,
		injectToken:     cfg.InjectToken,
		shadowMode:      cfg.ShadowMode,
		exemptPaths:     cfg.ExemptPaths,
		metrics:         &CSRFMetrics{},
		routeID:         routeID,
	}, nil
}

// Check validates CSRF protection for the request.
// It returns whether the request is allowed, and if not, the status code and message.
func (c *CompiledCSRF) Check(w http.ResponseWriter, r *http.Request) (allowed bool, statusCode int, message string) {
	c.metrics.TotalRequests.Add(1)

	// Check exempt paths
	if c.isExemptPath(r.URL.Path) {
		return true, 0, ""
	}

	// Safe methods: optionally inject token cookie, then pass
	if c.safeMethods[r.Method] {
		if c.injectToken {
			c.setTokenCookie(w)
		}
		return true, 0, ""
	}

	// State-changing method: validate

	// 1. Origin validation (if configured)
	if len(c.allowedOrigins) > 0 || len(c.allowedPatterns) > 0 {
		if ok, reason := c.checkOrigin(r); !ok {
			c.metrics.OriginCheckFailed.Add(1)
			c.metrics.ValidationFailed.Add(1)
			if c.shadowMode {
				logging.Warn("CSRF origin check failed (shadow mode)",
					zap.String("route", c.routeID),
					zap.String("reason", reason),
				)
				return true, 0, ""
			}
			return false, http.StatusForbidden, reason
		}
	}

	// 2. Double-submit cookie validation
	cookieToken := ""
	if cookie, err := r.Cookie(c.cookieName); err == nil {
		cookieToken = cookie.Value
	}

	headerToken := r.Header.Get(c.headerName)

	if cookieToken == "" && headerToken == "" {
		c.metrics.MissingToken.Add(1)
		c.metrics.ValidationFailed.Add(1)
		if c.shadowMode {
			logging.Warn("CSRF token missing (shadow mode)",
				zap.String("route", c.routeID),
			)
			return true, 0, ""
		}
		return false, http.StatusForbidden, "CSRF token missing"
	}

	if cookieToken == "" || headerToken == "" {
		c.metrics.MissingToken.Add(1)
		c.metrics.ValidationFailed.Add(1)
		if c.shadowMode {
			logging.Warn("CSRF token missing in cookie or header (shadow mode)",
				zap.String("route", c.routeID),
			)
			return true, 0, ""
		}
		return false, http.StatusForbidden, "CSRF token missing in cookie or header"
	}

	if cookieToken != headerToken {
		c.metrics.InvalidSignature.Add(1)
		c.metrics.ValidationFailed.Add(1)
		if c.shadowMode {
			logging.Warn("CSRF cookie/header mismatch (shadow mode)",
				zap.String("route", c.routeID),
			)
			return true, 0, ""
		}
		return false, http.StatusForbidden, "CSRF token mismatch"
	}

	// Validate the token's HMAC and expiry
	if valid, reason := c.validateToken(cookieToken); !valid {
		c.metrics.ValidationFailed.Add(1)
		if c.shadowMode {
			logging.Warn("CSRF token validation failed (shadow mode)",
				zap.String("route", c.routeID),
				zap.String("reason", reason),
			)
			return true, 0, ""
		}
		return false, http.StatusForbidden, reason
	}

	c.metrics.ValidationSuccess.Add(1)
	return true, 0, ""
}

// Status returns the admin status snapshot.
func (c *CompiledCSRF) Status() CSRFStatus {
	return CSRFStatus{
		CookieName:        c.cookieName,
		HeaderName:        c.headerName,
		TokenTTL:          c.tokenTTL.String(),
		ShadowMode:        c.shadowMode,
		InjectToken:       c.injectToken,
		TotalRequests:     c.metrics.TotalRequests.Load(),
		TokenGenerated:    c.metrics.TokenGenerated.Load(),
		ValidationSuccess: c.metrics.ValidationSuccess.Load(),
		ValidationFailed:  c.metrics.ValidationFailed.Load(),
		OriginCheckFailed: c.metrics.OriginCheckFailed.Load(),
		MissingToken:      c.metrics.MissingToken.Load(),
		ExpiredToken:      c.metrics.ExpiredToken.Load(),
		InvalidSignature:  c.metrics.InvalidSignature.Load(),
	}
}

// generateToken creates a signed CSRF token: base64(timestamp.hmac-hex).
func (c *CompiledCSRF) generateToken() string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(ts))
	sig := hex.EncodeToString(mac.Sum(nil))
	raw := ts + "." + sig
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// validateToken verifies the token's HMAC signature and expiry.
func (c *CompiledCSRF) validateToken(token string) (bool, string) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		c.metrics.InvalidSignature.Add(1)
		return false, "CSRF token malformed"
	}

	parts := strings.SplitN(string(raw), ".", 2)
	if len(parts) != 2 {
		c.metrics.InvalidSignature.Add(1)
		return false, "CSRF token malformed"
	}

	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		c.metrics.InvalidSignature.Add(1)
		return false, "CSRF token malformed"
	}

	// Check expiry
	tokenTime := time.Unix(ts, 0)
	if time.Since(tokenTime) > c.tokenTTL {
		c.metrics.ExpiredToken.Add(1)
		return false, "CSRF token expired"
	}

	// Verify HMAC
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(parts[0]))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(expectedSig)) {
		c.metrics.InvalidSignature.Add(1)
		return false, "CSRF token invalid signature"
	}

	return true, ""
}

// checkOrigin validates the Origin or Referer header against allowed origins.
func (c *CompiledCSRF) checkOrigin(r *http.Request) (bool, string) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Fallback to Referer
		referer := r.Header.Get("Referer")
		if referer != "" {
			if u, err := url.Parse(referer); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}

	if origin == "" {
		return false, "CSRF origin not provided"
	}

	// Exact match
	if c.allowedOrigins[origin] {
		return true, ""
	}

	// Regex match
	for _, re := range c.allowedPatterns {
		if re.MatchString(origin) {
			return true, ""
		}
	}

	return false, "CSRF origin not allowed"
}

// isExemptPath checks if the request path matches any exempt pattern.
func (c *CompiledCSRF) isExemptPath(path string) bool {
	for _, pattern := range c.exemptPaths {
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
	}
	return false
}

// setTokenCookie generates a new token and sets it as a cookie on the response.
func (c *CompiledCSRF) setTokenCookie(w http.ResponseWriter) {
	token := c.generateToken()
	c.metrics.TokenGenerated.Add(1)
	http.SetCookie(w, &http.Cookie{
		Name:     c.cookieName,
		Value:    token,
		Path:     c.cookiePath,
		Domain:   c.cookieDomain,
		Secure:   c.cookieSecure,
		HttpOnly: c.cookieHTTPOnly,
		SameSite: c.cookieSameSite,
		MaxAge:   int(c.tokenTTL.Seconds()),
	})
}

// MergeCSRFConfig merges per-route overrides onto global config.
// Per-route non-zero values override global values.
func MergeCSRFConfig(perRoute, global config.CSRFConfig) config.CSRFConfig {
	merged := global

	// Always take per-route enabled state
	merged.Enabled = perRoute.Enabled

	if perRoute.CookieName != "" {
		merged.CookieName = perRoute.CookieName
	}
	if perRoute.HeaderName != "" {
		merged.HeaderName = perRoute.HeaderName
	}
	if perRoute.Secret != "" {
		merged.Secret = perRoute.Secret
	}
	if perRoute.TokenTTL > 0 {
		merged.TokenTTL = perRoute.TokenTTL
	}
	if len(perRoute.SafeMethods) > 0 {
		merged.SafeMethods = perRoute.SafeMethods
	}
	if len(perRoute.AllowedOrigins) > 0 {
		merged.AllowedOrigins = perRoute.AllowedOrigins
	}
	if len(perRoute.AllowedOriginPatterns) > 0 {
		merged.AllowedOriginPatterns = perRoute.AllowedOriginPatterns
	}
	if perRoute.CookiePath != "" {
		merged.CookiePath = perRoute.CookiePath
	}
	if perRoute.CookieDomain != "" {
		merged.CookieDomain = perRoute.CookieDomain
	}
	if perRoute.CookieSecure {
		merged.CookieSecure = perRoute.CookieSecure
	}
	if perRoute.CookieSameSite != "" {
		merged.CookieSameSite = perRoute.CookieSameSite
	}
	if perRoute.CookieHTTPOnly {
		merged.CookieHTTPOnly = perRoute.CookieHTTPOnly
	}
	if perRoute.InjectToken {
		merged.InjectToken = perRoute.InjectToken
	}
	if perRoute.ShadowMode {
		merged.ShadowMode = perRoute.ShadowMode
	}
	if len(perRoute.ExemptPaths) > 0 {
		merged.ExemptPaths = perRoute.ExemptPaths
	}

	return merged
}
