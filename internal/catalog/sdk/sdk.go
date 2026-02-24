package sdk

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/wudi/gateway/internal/config"
)

// SpecProvider gives the SDK generator access to OpenAPI spec documents.
type SpecProvider interface {
	GetSpecDocs() map[string]*openapi3.T
}

type cachedSDK struct {
	data      []byte
	hash      string
	createdAt time.Time
}

// Generator generates SDK client code from OpenAPI specs.
type Generator struct {
	specProvider SpecProvider
	languages    map[string]bool
	cacheTTL     time.Duration
	templates    map[string]*template.Template

	mu    sync.RWMutex
	cache map[string]*cachedSDK // key: specID:language:hash
}

// NewGenerator creates a new SDK generator.
func NewGenerator(cfg config.SDKConfig, specProvider SpecProvider) *Generator {
	langs := make(map[string]bool)
	for _, l := range cfg.Languages {
		langs[l] = true
	}
	if len(langs) == 0 {
		langs = map[string]bool{"go": true, "python": true, "typescript": true}
	}

	ttl := cfg.CacheTTL
	if ttl == 0 {
		ttl = time.Hour
	}

	funcMap := template.FuncMap{
		"FuncName":         FuncName,
		"SnakeCase":        SnakeCase,
		"GoParamType":      GoParamType,
		"PythonParamType":  PythonParamType,
		"TSParamType":      TSParamType,
		"FormatGoPath":     FormatGoPath,
		"FormatPythonPath": FormatPythonPath,
		"FormatTSPath":     FormatTSPath,
		"SprintfArgs":      SprintfArgs,
		"SprintfFmt":       SprintfFmt,
		"HasBodyStr":       HasBodyStr,
		"AllParams":        AllParams,
	}

	templates := map[string]*template.Template{
		"go":         template.Must(template.New("go").Funcs(funcMap).Parse(goTemplate)),
		"python":     template.Must(template.New("python").Funcs(funcMap).Parse(pythonTemplate)),
		"typescript": template.Must(template.New("typescript").Funcs(funcMap).Parse(typescriptTemplate)),
	}

	return &Generator{
		specProvider: specProvider,
		languages:    langs,
		cacheTTL:     ttl,
		templates:    templates,
		cache:        make(map[string]*cachedSDK),
	}
}

// RegisterRoutes registers SDK endpoints on the given mux.
func (g *Generator) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/catalog/sdk", g.handleList)
	mux.HandleFunc("/catalog/sdk/", g.handleSDK)
}

// handleList returns available specs and languages.
func (g *Generator) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	docs := g.specProvider.GetSpecDocs()
	var langs []string
	for l := range g.languages {
		langs = append(langs, l)
	}

	type specInfo struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Version   string   `json:"version"`
		Languages []string `json:"languages"`
	}

	var specs []specInfo
	for path, doc := range docs {
		info := specInfo{
			ID:        sanitizeSpecID(path),
			Languages: langs,
		}
		if doc.Info != nil {
			info.Title = doc.Info.Title
			info.Version = doc.Info.Version
		}
		specs = append(specs, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"specs":     specs,
		"languages": langs,
	})
}

// handleSDK handles GET /catalog/sdk/{specID}/{language}.
func (g *Generator) handleSDK(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse /catalog/sdk/{specID} or /catalog/sdk/{specID}/{language}
	path := strings.TrimPrefix(r.URL.Path, "/catalog/sdk/")
	parts := strings.SplitN(path, "/", 2)

	specID := parts[0]
	if specID == "" {
		g.handleList(w, r)
		return
	}

	if len(parts) == 1 {
		// List languages for this spec
		g.handleSpecLanguages(w, r, specID)
		return
	}

	language := parts[1]
	if !g.languages[language] {
		http.Error(w, fmt.Sprintf(`{"error":"unsupported language %q"}`, language), http.StatusBadRequest)
		return
	}

	doc := g.findSpec(specID)
	if doc == nil {
		http.NotFound(w, r)
		return
	}

	data, err := g.generateSDK(specID, language, doc)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("%s-%s-sdk.zip", specID, language)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Write(data)
}

func (g *Generator) handleSpecLanguages(w http.ResponseWriter, r *http.Request, specID string) {
	doc := g.findSpec(specID)
	if doc == nil {
		http.NotFound(w, r)
		return
	}

	var langs []string
	for l := range g.languages {
		langs = append(langs, l)
	}

	info := map[string]any{
		"spec_id":   specID,
		"languages": langs,
	}
	if doc.Info != nil {
		info["title"] = doc.Info.Title
		info["version"] = doc.Info.Version
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (g *Generator) findSpec(specID string) *openapi3.T {
	docs := g.specProvider.GetSpecDocs()
	for path, doc := range docs {
		if sanitizeSpecID(path) == specID {
			return doc
		}
	}
	return nil
}

func (g *Generator) generateSDK(specID, language string, doc *openapi3.T) ([]byte, error) {
	// Compute spec hash for cache key
	specJSON, _ := doc.MarshalJSON()
	hash := fmt.Sprintf("%x", sha256.Sum256(specJSON))

	cacheKey := specID + ":" + language + ":" + hash

	// Check cache
	g.mu.RLock()
	if cached, ok := g.cache[cacheKey]; ok && time.Since(cached.createdAt) < g.cacheTTL {
		g.mu.RUnlock()
		return cached.data, nil
	}
	g.mu.RUnlock()

	// Generate
	tmpl, ok := g.templates[language]
	if !ok {
		return nil, fmt.Errorf("no template for language %q", language)
	}

	specData := WalkSpec(doc)

	var codeBuf bytes.Buffer
	if err := tmpl.Execute(&codeBuf, specData); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	// Package into zip
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	filename := sdkFilename(language, specData.PackageName)
	fw, err := zw.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("create zip entry: %w", err)
	}
	if _, err := fw.Write(codeBuf.Bytes()); err != nil {
		return nil, fmt.Errorf("write zip entry: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}

	data := zipBuf.Bytes()

	// Store in cache
	g.mu.Lock()
	g.cache[cacheKey] = &cachedSDK{
		data:      data,
		hash:      hash,
		createdAt: time.Now(),
	}
	g.mu.Unlock()

	return data, nil
}

func sdkFilename(language, packageName string) string {
	switch language {
	case "go":
		return packageName + "/client.go"
	case "python":
		return packageName + "/client.py"
	case "typescript":
		return packageName + "/client.ts"
	default:
		return "client.txt"
	}
}

func sanitizeSpecID(path string) string {
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
