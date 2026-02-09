package geo

import (
	"fmt"
	"path/filepath"
	"strings"
)

// GeoResult holds the geolocation lookup result.
type GeoResult struct {
	CountryCode string // ISO 3166-1 alpha-2 (e.g. "US")
	CountryName string
	City        string
}

// Provider performs IP-to-location lookups.
type Provider interface {
	Lookup(ip string) (*GeoResult, error)
	Close() error
}

// NewProvider auto-detects the database format from the file extension
// and returns the appropriate Provider implementation.
func NewProvider(path string) (Provider, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mmdb":
		return newMMDBProvider(path)
	case ".ipdb":
		return newIPDBProvider(path)
	default:
		return nil, fmt.Errorf("unsupported geo database format: %s (expected .mmdb or .ipdb)", ext)
	}
}
