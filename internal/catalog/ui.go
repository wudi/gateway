package catalog

import "embed"

//go:embed templates/catalog.html
var catalogHTML string

//go:embed templates/spec.html
var specHTML string

// Ensure embed is used.
var _ embed.FS
