package catalog

import (
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/router"
)

// CatalogEntry represents a single API route in the catalog.
type CatalogEntry struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Methods     []string `json:"methods,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Backends    int      `json:"backends"`
	SpecID      string   `json:"spec_id,omitempty"`
	Auth        bool     `json:"auth"`
	GRPC        bool     `json:"grpc,omitempty"`
	WebSocket   bool     `json:"websocket,omitempty"`
	GraphQL     bool     `json:"graphql,omitempty"`
}

// Spec represents a discovered OpenAPI spec.
type Spec struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Version string `json:"version"`
	RouteID string `json:"route_id"`
}

// CatalogStats holds catalog summary statistics.
type CatalogStats struct {
	TotalRoutes   int `json:"total_routes"`
	TotalSpecs    int `json:"total_specs"`
	TotalBackends int `json:"total_backends"`
}

// Catalog holds the assembled API catalog data.
type Catalog struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Stats       CatalogStats   `json:"stats"`
	Entries     []CatalogEntry `json:"entries"`
	Specs       []Spec         `json:"specs"`
}

// SpecProvider gives the catalog access to OpenAPI spec documents.
type SpecProvider interface {
	GetSpecDocs() map[string]*openapi3.T
}

// Builder builds the API catalog from gateway configuration.
type Builder struct {
	cfg          config.CatalogConfig
	routeConfigs []config.RouteConfig
	getRoutes    func() []*router.Route
	specProvider SpecProvider
}

// NewBuilder creates a new catalog builder.
func NewBuilder(cfg config.CatalogConfig, routeConfigs []config.RouteConfig, getRoutes func() []*router.Route, specProvider SpecProvider) *Builder {
	return &Builder{
		cfg:          cfg,
		routeConfigs: routeConfigs,
		getRoutes:    getRoutes,
		specProvider: specProvider,
	}
}

// Build assembles the catalog from current state.
func (b *Builder) Build() *Catalog {
	title := b.cfg.Title
	if title == "" {
		title = "API Gateway"
	}

	// Build route config lookup
	rcByID := make(map[string]config.RouteConfig, len(b.routeConfigs))
	for _, rc := range b.routeConfigs {
		rcByID[rc.ID] = rc
	}

	// Build spec lookup from OpenAPI route configs
	specByRouteID := make(map[string]string) // route ID → spec file
	for _, rc := range b.routeConfigs {
		if rc.OpenAPI.SpecFile != "" {
			specByRouteID[rc.ID] = rc.OpenAPI.SpecFile
		}
	}

	// Get actual spec docs
	specDocs := make(map[string]*openapi3.T)
	if b.specProvider != nil {
		specDocs = b.specProvider.GetSpecDocs()
	}

	var entries []CatalogEntry
	var specs []Spec
	seenSpecs := make(map[string]bool)
	totalBackends := 0

	routes := b.getRoutes()
	for _, route := range routes {
		rc := rcByID[route.ID]

		methods := make([]string, 0, len(route.Methods))
		for m := range route.Methods {
			methods = append(methods, m)
		}
		sort.Strings(methods)

		entry := CatalogEntry{
			ID:        route.ID,
			Path:      route.Path,
			Methods:   methods,
			Backends:  len(route.Backends),
			Auth:      rc.Auth.Required,
			GRPC:      rc.GRPC.Enabled,
			WebSocket: rc.WebSocket.Enabled,
			GraphQL:   rc.GraphQL.Enabled || rc.GraphQLFederation.Enabled,
		}

		totalBackends += len(route.Backends)

		// Try to get description from OpenAPI spec
		if specFile, ok := specByRouteID[route.ID]; ok {
			entry.SpecID = sanitizeSpecID(specFile)
			if doc, ok := specDocs[specFile]; ok {
				if doc.Info != nil {
					entry.Description = doc.Info.Description
				}
				if !seenSpecs[specFile] {
					seenSpecs[specFile] = true
					sp := Spec{
						ID:      sanitizeSpecID(specFile),
						RouteID: route.ID,
					}
					if doc.Info != nil {
						sp.Title = doc.Info.Title
						sp.Version = doc.Info.Version
					}
					specs = append(specs, sp)
				}
			}
		}

		// Collect tags from features
		if rc.GRPC.Enabled {
			entry.Tags = append(entry.Tags, "gRPC")
		}
		if rc.WebSocket.Enabled {
			entry.Tags = append(entry.Tags, "WebSocket")
		}
		if rc.GraphQL.Enabled {
			entry.Tags = append(entry.Tags, "GraphQL")
		}
		if rc.GraphQLFederation.Enabled {
			entry.Tags = append(entry.Tags, "GraphQL Federation")
		}
		if rc.Cache.Enabled {
			entry.Tags = append(entry.Tags, "Cached")
		}
		if rc.Auth.Required {
			entry.Tags = append(entry.Tags, "Auth Required")
		}

		entries = append(entries, entry)
	}

	return &Catalog{
		Title:       title,
		Description: b.cfg.Description,
		Stats: CatalogStats{
			TotalRoutes:   len(entries),
			TotalSpecs:    len(specs),
			TotalBackends: totalBackends,
		},
		Entries: entries,
		Specs:   specs,
	}
}

// GetSpecDoc returns a specific OpenAPI spec document by its sanitized ID.
func (b *Builder) GetSpecDoc(specID string) *openapi3.T {
	if b.specProvider == nil {
		return nil
	}
	docs := b.specProvider.GetSpecDocs()
	for path, doc := range docs {
		if sanitizeSpecID(path) == specID {
			return doc
		}
	}
	return nil
}

// sanitizeSpecID converts a file path to a URL-safe spec identifier.
func sanitizeSpecID(path string) string {
	// Replace slashes, dots with dashes
	result := make([]byte, 0, len(path))
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			result = append(result, c)
		default:
			result = append(result, '-')
		}
	}
	return string(result)
}
