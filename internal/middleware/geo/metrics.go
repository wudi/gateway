package geo

import "sync/atomic"

// GeoMetrics tracks geo filtering outcomes using atomic counters.
type GeoMetrics struct {
	TotalRequests atomic.Int64
	Allowed       atomic.Int64
	Denied        atomic.Int64
	LookupErrors  atomic.Int64
}

// GeoSnapshot is the admin API representation of a geo filter's state.
type GeoSnapshot struct {
	RouteID        string         `json:"route_id"`
	Enabled        bool           `json:"enabled"`
	AllowCountries []string       `json:"allow_countries,omitempty"`
	DenyCountries  []string       `json:"deny_countries,omitempty"`
	AllowCities    []string       `json:"allow_cities,omitempty"`
	DenyCities     []string       `json:"deny_cities,omitempty"`
	Order          string         `json:"order"`
	ShadowMode     bool           `json:"shadow_mode"`
	InjectHeaders  bool           `json:"inject_headers"`
	Metrics        map[string]int64 `json:"metrics"`
}
