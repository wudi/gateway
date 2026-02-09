package router

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/wudi/gateway/internal/config"
)

// CompiledMatcher evaluates domain, header, and query match criteria for a route.
type CompiledMatcher struct {
	domains []domainMatcher
	headers []headerMatcher
	queries []queryMatcher
	methods map[string]bool // nil = all methods allowed
}

type domainMatcher struct {
	exact    string // non-empty for exact match
	wildcard string // suffix like ".example.com" for *.example.com
}

type headerMatcher struct {
	name    string
	exact   string
	present *bool
	regex   *regexp.Regexp
}

type queryMatcher struct {
	name    string
	exact   string
	present *bool
	regex   *regexp.Regexp
}

// NewCompiledMatcher creates a CompiledMatcher from config and method list.
// Regexes are compiled once at creation time.
func NewCompiledMatcher(mc config.MatchConfig, methods []string) *CompiledMatcher {
	cm := &CompiledMatcher{}

	// Compile domain matchers
	for _, d := range mc.Domains {
		if strings.HasPrefix(d, "*.") {
			cm.domains = append(cm.domains, domainMatcher{wildcard: d[1:]}) // ".example.com"
		} else {
			cm.domains = append(cm.domains, domainMatcher{exact: d})
		}
	}

	// Compile header matchers
	for _, h := range mc.Headers {
		hm := headerMatcher{name: h.Name}
		if h.Value != "" {
			hm.exact = h.Value
		} else if h.Present != nil {
			hm.present = h.Present
		} else if h.Regex != "" {
			hm.regex = regexp.MustCompile(h.Regex) // already validated in loader
		}
		cm.headers = append(cm.headers, hm)
	}

	// Compile query matchers
	for _, q := range mc.Query {
		qm := queryMatcher{name: q.Name}
		if q.Value != "" {
			qm.exact = q.Value
		} else if q.Present != nil {
			qm.present = q.Present
		} else if q.Regex != "" {
			qm.regex = regexp.MustCompile(q.Regex) // already validated in loader
		}
		cm.queries = append(cm.queries, qm)
	}

	// Methods
	if len(methods) > 0 {
		cm.methods = make(map[string]bool, len(methods))
		for _, m := range methods {
			cm.methods[strings.ToUpper(m)] = true
		}
	}

	return cm
}

// Matches evaluates all criteria against the request.
func (cm *CompiledMatcher) Matches(r *http.Request) bool {
	// Method check
	if cm.methods != nil && !cm.methods[r.Method] {
		return false
	}

	// Domain check — at least one domain must match (OR within domains)
	if len(cm.domains) > 0 {
		host := r.Host
		// Strip port if present
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}
		matched := false
		for _, dm := range cm.domains {
			if dm.exact != "" && strings.EqualFold(host, dm.exact) {
				matched = true
				break
			}
			if dm.wildcard != "" && strings.HasSuffix(strings.ToLower(host), strings.ToLower(dm.wildcard)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Header checks — all must match (AND)
	for _, hm := range cm.headers {
		val := r.Header.Get(hm.name)
		if hm.present != nil {
			has := val != "" || r.Header.Get(hm.name) != "" // Get returns "" for missing
			// More precise: check if header key exists
			_, has = r.Header[http.CanonicalHeaderKey(hm.name)]
			if has != *hm.present {
				return false
			}
			continue
		}
		if hm.exact != "" {
			if val != hm.exact {
				return false
			}
			continue
		}
		if hm.regex != nil {
			if !hm.regex.MatchString(val) {
				return false
			}
			continue
		}
	}

	// Query checks — all must match (AND)
	query := r.URL.Query()
	for _, qm := range cm.queries {
		val := query.Get(qm.name)
		if qm.present != nil {
			has := query.Has(qm.name)
			if has != *qm.present {
				return false
			}
			continue
		}
		if qm.exact != "" {
			if val != qm.exact {
				return false
			}
			continue
		}
		if qm.regex != nil {
			if !qm.regex.MatchString(val) {
				return false
			}
			continue
		}
	}

	return true
}

// Specificity returns a score for ordering routes. Higher = more specific.
func (cm *CompiledMatcher) Specificity() int {
	score := 0
	for _, dm := range cm.domains {
		if dm.exact != "" {
			score += 150
		} else {
			score += 100
		}
	}
	score += len(cm.headers) * 10
	score += len(cm.queries) * 10
	if cm.methods != nil {
		score += 5
	}
	return score
}
