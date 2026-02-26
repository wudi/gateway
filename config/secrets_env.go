package config

import (
	"context"
	"fmt"
	"os"
)

// EnvProvider resolves secret references from environment variables.
type EnvProvider struct{}

func (p *EnvProvider) Scheme() string { return "env" }

func (p *EnvProvider) Resolve(_ context.Context, ref string) (string, error) {
	val, ok := os.LookupEnv(ref)
	if !ok {
		return "", fmt.Errorf("environment variable %q not set", ref)
	}
	return val, nil
}
