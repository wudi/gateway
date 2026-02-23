package catalog

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
)

// Handler serves the API catalog endpoints.
type Handler struct {
	builder      *Builder
	catalogTpl   *template.Template
	specTpl      *template.Template
}

// NewHandler creates a new catalog HTTP handler.
func NewHandler(builder *Builder) *Handler {
	catalogTpl := template.Must(template.New("catalog").Parse(catalogHTML))
	specTpl := template.Must(template.New("spec").Parse(specHTML))
	return &Handler{
		builder:    builder,
		catalogTpl: catalogTpl,
		specTpl:    specTpl,
	}
}

// RegisterRoutes registers catalog endpoints on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/catalog", h.handleCatalogJSON)
	mux.HandleFunc("/catalog/specs", h.handleSpecsList)
	mux.HandleFunc("/catalog/specs/", h.handleSpecByID)
	mux.HandleFunc("/catalog/ui", h.handleUI)
	mux.HandleFunc("/catalog/ui/", h.handleSpecUI)
}

// handleCatalogJSON returns the catalog as JSON.
func (h *Handler) handleCatalogJSON(w http.ResponseWriter, r *http.Request) {
	catalog := h.builder.Build()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(catalog)
}

// handleSpecsList returns all discovered OpenAPI specs as JSON.
func (h *Handler) handleSpecsList(w http.ResponseWriter, r *http.Request) {
	catalog := h.builder.Build()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(catalog.Specs)
}

// handleSpecByID returns a raw OpenAPI spec by its ID.
func (h *Handler) handleSpecByID(w http.ResponseWriter, r *http.Request) {
	specID := strings.TrimPrefix(r.URL.Path, "/catalog/specs/")
	if specID == "" {
		h.handleSpecsList(w, r)
		return
	}

	doc := h.builder.GetSpecDoc(specID)
	if doc == nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

// handleUI serves the catalog HTML page.
func (h *Handler) handleUI(w http.ResponseWriter, r *http.Request) {
	catalog := h.builder.Build()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.catalogTpl.Execute(w, catalog)
}

// handleSpecUI serves the Redoc viewer for a specific spec.
func (h *Handler) handleSpecUI(w http.ResponseWriter, r *http.Request) {
	specID := strings.TrimPrefix(r.URL.Path, "/catalog/ui/")
	if specID == "" {
		h.handleUI(w, r)
		return
	}

	doc := h.builder.GetSpecDoc(specID)
	if doc == nil {
		http.NotFound(w, r)
		return
	}

	data := struct {
		Title  string
		SpecID string
	}{
		Title:  h.builder.cfg.Title,
		SpecID: specID,
	}
	if data.Title == "" {
		data.Title = "API Gateway"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.specTpl.Execute(w, data)
}
