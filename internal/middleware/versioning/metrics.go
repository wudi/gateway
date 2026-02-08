package versioning

// VersioningSnapshot represents the admin API snapshot for a versioner.
type VersioningSnapshot struct {
	Source         string                  `json:"source"`
	DefaultVersion string                 `json:"default_version"`
	Versions       map[string]VersionStats `json:"versions"`
	UnknownCount   int64                   `json:"unknown_count"`
}

// VersionStats contains per-version metrics.
type VersionStats struct {
	Requests   int64  `json:"requests"`
	Deprecated bool   `json:"deprecated"`
	Sunset     string `json:"sunset,omitempty"`
}
