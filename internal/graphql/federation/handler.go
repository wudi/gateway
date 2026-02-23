package federation

import (
	"encoding/json"
	"io"
	"net/http"
)

// Handler serves GraphQL federation requests as an http.Handler.
type Handler struct {
	stitcher *Stitcher
}

// NewHandler creates a new federation HTTP handler.
func NewHandler(stitcher *Stitcher) *Handler {
	return &Handler{stitcher: stitcher}
}

// ServeHTTP handles GraphQL requests by routing them through the federation stitcher.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"errors":[{"message":"method not allowed"}]}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeGraphQLError(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req GraphQLRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeGraphQLError(w, "invalid JSON request body", http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		writeGraphQLError(w, "query is required", http.StatusBadRequest)
		return
	}

	resp, err := h.stitcher.HandleQuery(r.Context(), req)
	if err != nil {
		writeGraphQLError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// writeGraphQLError writes a GraphQL error response.
func writeGraphQLError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := GraphQLResponse{
		Errors: []GraphQLError{{Message: msg}},
	}
	json.NewEncoder(w).Encode(resp)
}
