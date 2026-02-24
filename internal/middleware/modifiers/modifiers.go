package modifiers

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
)

// Modifier is the interface for request/response modifiers.
type Modifier interface {
	ModifyRequest(r *http.Request)
	ModifyResponse(h http.Header)
}

// compiledModifier wraps a modifier with optional condition and else branch.
type compiledModifier struct {
	modifier  Modifier
	condition *compiledCondition
	elseMod   Modifier
	scope     string // "request", "response", "both" (default "both")
	priority  int
}

// compiledCondition evaluates whether a modifier should run.
type compiledCondition struct {
	condType string // "header", "cookie", "query", "path_regex"
	name     string
	regex    *regexp.Regexp // nil means existence check only
}

func (cc *compiledCondition) matches(r *http.Request) bool {
	var value string
	var exists bool

	switch cc.condType {
	case "header":
		value = r.Header.Get(cc.name)
		exists = value != ""
	case "cookie":
		c, err := r.Cookie(cc.name)
		if err == nil {
			value = c.Value
			exists = true
		}
	case "query":
		value = r.URL.Query().Get(cc.name)
		exists = value != ""
	case "path_regex":
		value = r.URL.Path
		exists = true
	default:
		return false
	}

	if !exists {
		return false
	}
	if cc.regex != nil {
		return cc.regex.MatchString(value)
	}
	return true
}

// ModifierChain is a pre-compiled, ordered chain of modifiers for a route.
type ModifierChain struct {
	modifiers []compiledModifier
	applied   atomic.Int64
}

// Middleware returns a middleware that applies request and response modifiers.
func (mc *ModifierChain) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Apply request-phase modifiers
			for _, cm := range mc.modifiers {
				if cm.scope == "response" {
					continue
				}
				mod := cm.modifier
				if cm.condition != nil {
					if !cm.condition.matches(r) {
						if cm.elseMod != nil {
							cm.elseMod.ModifyRequest(r)
						}
						continue
					}
				}
				mod.ModifyRequest(r)
			}
			mc.applied.Add(1)

			// Wrap response writer for response-phase modifiers
			hasResponseMods := false
			for _, cm := range mc.modifiers {
				if cm.scope != "request" {
					hasResponseMods = true
					break
				}
			}

			if hasResponseMods {
				rw := &modifierResponseWriter{ResponseWriter: w, chain: mc, req: r}
				next.ServeHTTP(rw, r)
				if !rw.headerWritten {
					rw.applyResponseModifiers()
					rw.ResponseWriter.WriteHeader(http.StatusOK)
				}
			} else {
				next.ServeHTTP(w, r)
			}
		})
	}
}

type modifierResponseWriter struct {
	http.ResponseWriter
	chain         *ModifierChain
	req           *http.Request
	headerWritten bool
}

func (mrw *modifierResponseWriter) WriteHeader(code int) {
	mrw.applyResponseModifiers()
	mrw.headerWritten = true
	mrw.ResponseWriter.WriteHeader(code)
}

func (mrw *modifierResponseWriter) Write(b []byte) (int, error) {
	if !mrw.headerWritten {
		mrw.applyResponseModifiers()
		mrw.headerWritten = true
	}
	return mrw.ResponseWriter.Write(b)
}

func (mrw *modifierResponseWriter) applyResponseModifiers() {
	for _, cm := range mrw.chain.modifiers {
		if cm.scope == "request" {
			continue
		}
		mod := cm.modifier
		if cm.condition != nil {
			if !cm.condition.matches(mrw.req) {
				if cm.elseMod != nil {
					cm.elseMod.ModifyResponse(mrw.ResponseWriter.Header())
				}
				continue
			}
		}
		mod.ModifyResponse(mrw.ResponseWriter.Header())
	}
}

// Stats returns modifier chain stats.
func (mc *ModifierChain) Stats() map[string]interface{} {
	return map[string]interface{}{
		"modifier_count": len(mc.modifiers),
		"applied":        mc.applied.Load(),
	}
}

// --- Modifier Implementations ---

// HeaderCopy copies a header value from one name to another.
type HeaderCopy struct {
	From string
	To   string
}

func (hc *HeaderCopy) ModifyRequest(r *http.Request) {
	if v := r.Header.Get(hc.From); v != "" {
		r.Header.Set(hc.To, v)
	}
}

func (hc *HeaderCopy) ModifyResponse(h http.Header) {
	if v := h.Get(hc.From); v != "" {
		h.Set(hc.To, v)
	}
}

// HeaderSet sets a header to a static value.
type HeaderSet struct {
	Name  string
	Value string
}

func (hs *HeaderSet) ModifyRequest(r *http.Request) {
	r.Header.Set(hs.Name, hs.Value)
}

func (hs *HeaderSet) ModifyResponse(h http.Header) {
	h.Set(hs.Name, hs.Value)
}

// CookieModifier adds or sets a cookie.
type CookieModifier struct {
	Cookie http.Cookie
}

func (cm *CookieModifier) ModifyRequest(r *http.Request) {
	r.AddCookie(&http.Cookie{Name: cm.Cookie.Name, Value: cm.Cookie.Value})
}

func (cm *CookieModifier) ModifyResponse(h http.Header) {
	c := cm.Cookie
	h.Add("Set-Cookie", (&c).String())
}

// QueryModifier adds/overrides query parameters.
type QueryModifier struct {
	Params map[string]string
}

func (qm *QueryModifier) ModifyRequest(r *http.Request) {
	q := r.URL.Query()
	for k, v := range qm.Params {
		q.Set(k, v)
	}
	r.URL.RawQuery = q.Encode()
}

func (qm *QueryModifier) ModifyResponse(_ http.Header) {}

// StashModifier saves the original URL in a header before rewriting.
type StashModifier struct {
	HeaderName string
}

func (sm *StashModifier) ModifyRequest(r *http.Request) {
	name := sm.HeaderName
	if name == "" {
		name = "X-Original-URL"
	}
	r.Header.Set(name, r.URL.String())
}

func (sm *StashModifier) ModifyResponse(_ http.Header) {}

// PortModifier overrides the port component of the request URL.
type PortModifier struct {
	Port int
}

func (pm *PortModifier) ModifyRequest(r *http.Request) {
	host := r.URL.Hostname()
	r.URL.Host = fmt.Sprintf("%s:%d", host, pm.Port)
	r.Host = r.URL.Host
}

func (pm *PortModifier) ModifyResponse(_ http.Header) {}

// --- Compilation ---

// Compile creates a ModifierChain from config.
func Compile(cfgs []config.ModifierConfig) (*ModifierChain, error) {
	compiled := make([]compiledModifier, 0, len(cfgs))
	for i, cfg := range cfgs {
		mod, err := buildModifier(cfg)
		if err != nil {
			return nil, fmt.Errorf("modifier %d (%s): %w", i, cfg.Type, err)
		}

		scope := cfg.Scope
		if scope == "" {
			scope = "both"
		}

		cm := compiledModifier{
			modifier: mod,
			scope:    scope,
			priority: cfg.Priority,
		}

		// Compile condition if present
		if cfg.Condition != nil {
			cond, err := compileCondition(cfg.Condition)
			if err != nil {
				return nil, fmt.Errorf("modifier %d condition: %w", i, err)
			}
			cm.condition = cond

			// Compile else modifier if present
			if cfg.Else != nil {
				elseMod, err := buildModifier(*cfg.Else)
				if err != nil {
					return nil, fmt.Errorf("modifier %d else: %w", i, err)
				}
				cm.elseMod = elseMod
			}
		}

		compiled = append(compiled, cm)
	}

	// Stable sort by priority (preserves declaration order within same priority)
	sort.SliceStable(compiled, func(i, j int) bool {
		return compiled[i].priority > compiled[j].priority
	})

	return &ModifierChain{modifiers: compiled}, nil
}

func buildModifier(cfg config.ModifierConfig) (Modifier, error) {
	switch cfg.Type {
	case "header_copy":
		if cfg.From == "" || cfg.To == "" {
			return nil, fmt.Errorf("header_copy requires from and to")
		}
		return &HeaderCopy{From: cfg.From, To: cfg.To}, nil

	case "header_set":
		if cfg.Name == "" {
			return nil, fmt.Errorf("header_set requires name")
		}
		return &HeaderSet{Name: cfg.Name, Value: cfg.Value}, nil

	case "cookie":
		if cfg.Name == "" {
			return nil, fmt.Errorf("cookie modifier requires name")
		}
		sameSite := http.SameSiteDefaultMode
		switch strings.ToLower(cfg.SameSite) {
		case "lax":
			sameSite = http.SameSiteLaxMode
		case "strict":
			sameSite = http.SameSiteStrictMode
		case "none":
			sameSite = http.SameSiteNoneMode
		}
		return &CookieModifier{Cookie: http.Cookie{
			Name:     cfg.Name,
			Value:    cfg.Value,
			Domain:   cfg.Domain,
			Path:     cfg.Path,
			MaxAge:   cfg.MaxAge,
			Secure:   cfg.Secure,
			HttpOnly: cfg.HttpOnly,
			SameSite: sameSite,
		}}, nil

	case "query":
		if len(cfg.Params) == 0 {
			return nil, fmt.Errorf("query modifier requires params")
		}
		return &QueryModifier{Params: cfg.Params}, nil

	case "stash":
		return &StashModifier{HeaderName: cfg.Name}, nil

	case "port":
		if cfg.Port == 0 {
			return nil, fmt.Errorf("port modifier requires port")
		}
		return &PortModifier{Port: cfg.Port}, nil

	default:
		return nil, fmt.Errorf("unknown modifier type: %s", cfg.Type)
	}
}

func compileCondition(cfg *config.ConditionConfig) (*compiledCondition, error) {
	cc := &compiledCondition{
		condType: cfg.Type,
		name:     cfg.Name,
	}

	switch cfg.Type {
	case "header", "cookie", "query":
		if cfg.Name == "" {
			return nil, fmt.Errorf("condition type %s requires name", cfg.Type)
		}
	case "path_regex":
		// name is not needed
	default:
		return nil, fmt.Errorf("unknown condition type: %s", cfg.Type)
	}

	if cfg.Value != "" {
		re, err := regexp.Compile(cfg.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid condition regex %q: %w", cfg.Value, err)
		}
		cc.regex = re
	}

	return cc, nil
}

// --- ByRoute Manager ---

// ModifiersByRoute manages per-route modifier chains.
type ModifiersByRoute struct {
	byroute.Manager[*ModifierChain]
}

// NewModifiersByRoute creates a new per-route modifier manager.
func NewModifiersByRoute() *ModifiersByRoute {
	return &ModifiersByRoute{}
}

// AddRoute compiles and adds a modifier chain for a route.
func (m *ModifiersByRoute) AddRoute(routeID string, cfgs []config.ModifierConfig) error {
	chain, err := Compile(cfgs)
	if err != nil {
		return err
	}
	m.Add(routeID, chain)
	return nil
}

// GetChain returns the modifier chain for a route.
func (m *ModifiersByRoute) GetChain(routeID string) *ModifierChain {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route modifier stats.
func (m *ModifiersByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(mc *ModifierChain) interface{} {
		return mc.Stats()
	})
}

// Ensure unused imports don't cause issues.
var (
	_ = time.Now
	_ = url.Parse
)
