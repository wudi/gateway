package mirror

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/wudi/runway/config"
)

// Conditions determines whether a request should be mirrored.
type Conditions struct {
	methods   map[string]bool
	headers   map[string]string
	pathRegex *regexp.Regexp
}

// NewConditions creates Conditions from config, compiling regexes at init time.
func NewConditions(cfg config.MirrorConditionsConfig) (*Conditions, error) {
	c := &Conditions{}

	if len(cfg.Methods) > 0 {
		c.methods = make(map[string]bool, len(cfg.Methods))
		for _, m := range cfg.Methods {
			c.methods[strings.ToUpper(m)] = true
		}
	}

	if len(cfg.Headers) > 0 {
		c.headers = cfg.Headers
	}

	if cfg.PathRegex != "" {
		re, err := regexp.Compile(cfg.PathRegex)
		if err != nil {
			return nil, err
		}
		c.pathRegex = re
	}

	return c, nil
}

// IsEmpty returns true if no conditions are configured.
func (c *Conditions) IsEmpty() bool {
	return c == nil || (len(c.methods) == 0 && len(c.headers) == 0 && c.pathRegex == nil)
}

// Match returns true if the request matches all configured conditions (AND logic).
func (c *Conditions) Match(r *http.Request) bool {
	if c == nil {
		return true
	}

	if len(c.methods) > 0 && !c.methods[r.Method] {
		return false
	}

	for key, val := range c.headers {
		if !strings.EqualFold(r.Header.Get(key), val) {
			return false
		}
	}

	if c.pathRegex != nil && !c.pathRegex.MatchString(r.URL.Path) {
		return false
	}

	return true
}
