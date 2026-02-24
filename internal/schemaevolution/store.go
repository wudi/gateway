package schemaevolution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// storedSpec wraps a spec with metadata for persistence.
type storedSpec struct {
	Version   string    `json:"version"`
	Timestamp time.Time `json:"timestamp"`
	Data      []byte    `json:"data"`
}

// SpecStore persists previous spec versions on the filesystem.
type SpecStore struct {
	dir         string
	maxVersions int
}

// NewSpecStore creates a new filesystem-backed spec store.
func NewSpecStore(dir string, maxVersions int) (*SpecStore, error) {
	if maxVersions <= 0 {
		maxVersions = 10
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create spec store dir: %w", err)
	}
	return &SpecStore{dir: dir, maxVersions: maxVersions}, nil
}

// Store saves a spec version for a given spec ID.
func (s *SpecStore) Store(specID string, doc *openapi3.T) error {
	version := ""
	if doc.Info != nil {
		version = doc.Info.Version
	}

	data, err := doc.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal spec: %w", err)
	}

	stored := storedSpec{
		Version:   version,
		Timestamp: time.Now(),
		Data:      data,
	}

	filename := fmt.Sprintf("%s_%d.json", sanitizeID(specID), stored.Timestamp.UnixNano())
	path := filepath.Join(s.dir, filename)

	raw, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("marshal stored spec: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write spec file: %w", err)
	}

	return s.pruneOldVersions(specID)
}

// GetPrevious returns the most recently stored spec for a given ID.
func (s *SpecStore) GetPrevious(specID string) (*openapi3.T, string, error) {
	entries, err := s.getEntries(specID)
	if err != nil || len(entries) == 0 {
		return nil, "", err
	}

	// Most recent entry
	latest := entries[len(entries)-1]
	path := filepath.Join(s.dir, latest)

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read spec file: %w", err)
	}

	var stored storedSpec
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, "", fmt.Errorf("unmarshal stored spec: %w", err)
	}

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(stored.Data)
	if err != nil {
		return nil, "", fmt.Errorf("load stored spec: %w", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		return nil, stored.Version, fmt.Errorf("validate stored spec: %w", err)
	}

	return doc, stored.Version, nil
}

func (s *SpecStore) getEntries(specID string) ([]string, error) {
	prefix := sanitizeID(specID) + "_"
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read store dir: %w", err)
	}

	var matching []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".json") {
			matching = append(matching, e.Name())
		}
	}
	sort.Strings(matching)
	return matching, nil
}

func (s *SpecStore) pruneOldVersions(specID string) error {
	entries, err := s.getEntries(specID)
	if err != nil {
		return err
	}

	if len(entries) <= s.maxVersions {
		return nil
	}

	toRemove := entries[:len(entries)-s.maxVersions]
	for _, name := range toRemove {
		os.Remove(filepath.Join(s.dir, name))
	}
	return nil
}

func sanitizeID(id string) string {
	var sb strings.Builder
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			sb.WriteRune(c)
		} else {
			sb.WriteByte('_')
		}
	}
	return sb.String()
}
