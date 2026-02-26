package config

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// FileProvider resolves secret references by reading file contents.
type FileProvider struct {
	// AllowedPrefixes restricts readable paths to these directory prefixes
	// (defense-in-depth). If empty, all paths are allowed.
	AllowedPrefixes []string
}

func (p *FileProvider) Scheme() string { return "file" }

func (p *FileProvider) Resolve(_ context.Context, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("file path is empty")
	}
	if len(p.AllowedPrefixes) > 0 {
		allowed := false
		for _, prefix := range p.AllowedPrefixes {
			if strings.HasPrefix(ref, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("file path %q not under any allowed prefix", ref)
		}
	}
	data, err := os.ReadFile(ref)
	if err != nil {
		return "", fmt.Errorf("reading secret file %q: %w", ref, err)
	}
	// Trim trailing whitespace/newlines — secret files often have a trailing newline.
	return strings.TrimRight(string(data), " \t\r\n"), nil
}
