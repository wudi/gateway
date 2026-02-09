package versioning

import (
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/wudi/gateway/internal/config"
)

// versionInfo holds compiled per-version metadata.
type versionInfo struct {
	deprecated bool
	sunset     string
	requests   int64
}

// Versioner detects API version from requests and manages version metadata.
type Versioner struct {
	source         string
	headerName     string
	queryParam     string
	pathPrefix     string
	stripPrefix    bool
	defaultVersion string
	versions       map[string]*versionInfo
	acceptRegex    *regexp.Regexp
	unknownCount   int64
}

// New creates a new Versioner from config.
func New(cfg config.VersioningConfig) (*Versioner, error) {
	v := &Versioner{
		source:         cfg.Source,
		headerName:     cfg.HeaderName,
		queryParam:     cfg.QueryParam,
		pathPrefix:     cfg.PathPrefix,
		stripPrefix:    cfg.StripPrefix,
		defaultVersion: cfg.DefaultVersion,
		versions:       make(map[string]*versionInfo, len(cfg.Versions)),
	}

	// Defaults
	if v.headerName == "" {
		v.headerName = "X-API-Version"
	}
	if v.queryParam == "" {
		v.queryParam = "version"
	}
	if v.pathPrefix == "" {
		v.pathPrefix = "/v"
	}

	// Build version info map
	for ver, vcfg := range cfg.Versions {
		v.versions[ver] = &versionInfo{
			deprecated: vcfg.Deprecated,
			sunset:     vcfg.Sunset,
		}
	}

	// Compile accept regex for "accept" source
	if cfg.Source == "accept" {
		v.acceptRegex = regexp.MustCompile(`application/vnd\.[^.]+\.v(\d+)`)
	}

	return v, nil
}

// DetectVersion extracts the API version from the request based on the configured source.
func (v *Versioner) DetectVersion(r *http.Request) string {
	var version string

	switch v.source {
	case "path":
		version = v.detectFromPath(r)
	case "header":
		version = r.Header.Get(v.headerName)
	case "accept":
		version = v.detectFromAccept(r)
	case "query":
		version = r.URL.Query().Get(v.queryParam)
	}

	if version == "" {
		version = v.defaultVersion
	}

	// Record metrics
	if info, ok := v.versions[version]; ok {
		atomic.AddInt64(&info.requests, 1)
	} else {
		atomic.AddInt64(&v.unknownCount, 1)
		version = v.defaultVersion
		if info, ok := v.versions[version]; ok {
			atomic.AddInt64(&info.requests, 1)
		}
	}

	return version
}

// detectFromPath extracts version from the URL path prefix (e.g., /v2/users -> "2").
func (v *Versioner) detectFromPath(r *http.Request) string {
	path := r.URL.Path
	if !strings.HasPrefix(path, v.pathPrefix) {
		return ""
	}
	rest := path[len(v.pathPrefix):]
	if rest == "" {
		return ""
	}
	// Extract the version segment (up to next "/" or end)
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return rest
	}
	return rest[:idx]
}

// detectFromAccept extracts version from Accept header using vnd pattern.
func (v *Versioner) detectFromAccept(r *http.Request) string {
	accept := r.Header.Get("Accept")
	if accept == "" || v.acceptRegex == nil {
		return ""
	}
	matches := v.acceptRegex.FindStringSubmatch(accept)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// StripVersionPrefix removes the version prefix from the request path if configured.
func (v *Versioner) StripVersionPrefix(r *http.Request, version string) {
	if !v.stripPrefix || v.source != "path" {
		return
	}
	prefix := v.pathPrefix + version
	if strings.HasPrefix(r.URL.Path, prefix) {
		newPath := r.URL.Path[len(prefix):]
		if newPath == "" {
			newPath = "/"
		}
		r.URL.Path = newPath
		if r.URL.RawPath != "" {
			if strings.HasPrefix(r.URL.RawPath, prefix) {
				r.URL.RawPath = r.URL.RawPath[len(prefix):]
				if r.URL.RawPath == "" {
					r.URL.RawPath = "/"
				}
			}
		}
	}
}

// InjectDeprecationHeaders sets Deprecation and Sunset headers if the version is deprecated.
func (v *Versioner) InjectDeprecationHeaders(w http.ResponseWriter, version string) {
	info, ok := v.versions[version]
	if !ok {
		return
	}
	if info.deprecated {
		w.Header().Set("Deprecation", "true")
	}
	if info.sunset != "" {
		w.Header().Set("Sunset", info.sunset)
	}
}

// Snapshot returns metrics for the admin API.
func (v *Versioner) Snapshot() VersioningSnapshot {
	snap := VersioningSnapshot{
		Source:         v.source,
		DefaultVersion: v.defaultVersion,
		Versions:       make(map[string]VersionStats, len(v.versions)),
		UnknownCount:   atomic.LoadInt64(&v.unknownCount),
	}
	for ver, info := range v.versions {
		snap.Versions[ver] = VersionStats{
			Requests:   atomic.LoadInt64(&info.requests),
			Deprecated: info.deprecated,
			Sunset:     info.sunset,
		}
	}
	return snap
}
