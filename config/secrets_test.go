package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
)

// --- EnvProvider ---

func TestEnvProvider_Resolve(t *testing.T) {
	t.Setenv("TEST_SECRET_VAL", "s3cret")

	p := &EnvProvider{}
	got, err := p.Resolve(context.Background(), "TEST_SECRET_VAL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "s3cret" {
		t.Fatalf("got %q, want %q", got, "s3cret")
	}
}

func TestEnvProvider_Missing(t *testing.T) {
	p := &EnvProvider{}
	_, err := p.Resolve(context.Background(), "DEFINITELY_NOT_SET_XYZ_42")
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
}

// --- FileProvider ---

func TestFileProvider_Resolve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	os.WriteFile(path, []byte("file-secret\n"), 0o600)

	p := &FileProvider{}
	got, err := p.Resolve(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file-secret" {
		t.Fatalf("got %q, want %q", got, "file-secret")
	}
}

func TestFileProvider_MissingFile(t *testing.T) {
	p := &FileProvider{}
	_, err := p.Resolve(context.Background(), "/nonexistent/path/secret.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFileProvider_AllowedPrefixes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	os.WriteFile(path, []byte("ok"), 0o600)

	p := &FileProvider{AllowedPrefixes: []string{dir}}
	_, err := p.Resolve(context.Background(), path)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}

	p2 := &FileProvider{AllowedPrefixes: []string{"/other/prefix"}}
	_, err = p2.Resolve(context.Background(), path)
	if err == nil {
		t.Fatal("expected error for disallowed prefix")
	}
}

func TestFileProvider_MultilineContent(t *testing.T) {
	dir := t.TempDir()
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIBogIBAAJBALRi...\n-----END RSA PRIVATE KEY-----\n"
	path := filepath.Join(dir, "key.pem")
	os.WriteFile(path, []byte(pem), 0o600)

	p := &FileProvider{}
	got, err := p.Resolve(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Trailing newline is trimmed
	want := "-----BEGIN RSA PRIVATE KEY-----\nMIIBogIBAAJBALRi...\n-----END RSA PRIVATE KEY-----"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// --- SecretRegistry ---

func TestSecretRegistry_ResolveAndClone(t *testing.T) {
	reg := NewSecretRegistry()
	reg.Register(&EnvProvider{})

	t.Setenv("REG_TEST", "value")

	got, err := reg.Resolve(context.Background(), "env", "REG_TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "value" {
		t.Fatalf("got %q, want %q", got, "value")
	}

	// Clone doesn't affect original
	clone := reg.Clone()
	clone.Register(&FileProvider{})
	if _, err := reg.Resolve(context.Background(), "file", "/tmp/x"); err == nil {
		t.Fatal("original should not have file provider")
	}
}

func TestSecretRegistry_UnknownScheme(t *testing.T) {
	reg := NewSecretRegistry()
	_, err := reg.Resolve(context.Background(), "vault", "secret/data/foo")
	if err == nil {
		t.Fatal("expected error for unknown scheme")
	}
}

// --- resolveSecretRefs ---

func TestResolveSecretRefs(t *testing.T) {
	t.Setenv("RESOLVE_JWT", "jwt-secret-value")
	t.Setenv("RESOLVE_REDIS", "redis-pass")

	dir := t.TempDir()
	path := filepath.Join(dir, "api-key.txt")
	os.WriteFile(path, []byte("my-api-key\n"), 0o600)

	cfg := &Config{}
	cfg.Authentication.JWT.Secret = "${env:RESOLVE_JWT}"
	cfg.Redis.Password = "${env:RESOLVE_REDIS}"
	cfg.Authentication.APIKey.Keys = []APIKeyEntry{
		{Key: "${file:" + path + "}", ClientID: "c1"},
	}
	// Non-ref strings should remain unchanged
	cfg.Authentication.JWT.Algorithm = "HS256"
	cfg.Registry.Consul.Address = "localhost:8500"

	reg := NewSecretRegistry()
	reg.Register(&EnvProvider{})
	reg.Register(&FileProvider{})

	if err := resolveSecretRefs(cfg, reg, context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Authentication.JWT.Secret != "jwt-secret-value" {
		t.Errorf("JWT secret: got %q", cfg.Authentication.JWT.Secret)
	}
	if cfg.Redis.Password != "redis-pass" {
		t.Errorf("Redis password: got %q", cfg.Redis.Password)
	}
	if cfg.Authentication.APIKey.Keys[0].Key != "my-api-key" {
		t.Errorf("API key: got %q", cfg.Authentication.APIKey.Keys[0].Key)
	}
	if cfg.Authentication.JWT.Algorithm != "HS256" {
		t.Errorf("Algorithm changed unexpectedly")
	}
}

func TestResolveSecretRefs_MissingStrict(t *testing.T) {
	cfg := &Config{}
	cfg.Authentication.JWT.Secret = "${env:DEFINITELY_UNSET_VAR_XYZ}"

	reg := NewSecretRegistry()
	reg.Register(&EnvProvider{})

	err := resolveSecretRefs(cfg, reg, context.Background())
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
}

func TestResolveSecretRefs_BareEnvUnchanged(t *testing.T) {
	cfg := &Config{}
	// Bare ${VAR} syntax — should NOT be touched by resolveSecretRefs
	cfg.Authentication.JWT.Secret = "${JWT_SECRET}"

	reg := NewSecretRegistry()
	reg.Register(&EnvProvider{})

	if err := resolveSecretRefs(cfg, reg, context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Authentication.JWT.Secret != "${JWT_SECRET}" {
		t.Errorf("bare env var was modified: got %q", cfg.Authentication.JWT.Secret)
	}
}

func TestResolveSecretRefs_Extensions(t *testing.T) {
	// Config with populated Extensions (map[string]yaml.RawMessage) should not panic
	cfg := &Config{
		Extensions: map[string]yaml.RawMessage{
			"plugin1": yaml.RawMessage(`{"key": "value"}`),
		},
	}

	reg := NewSecretRegistry()
	reg.Register(&EnvProvider{})

	if err := resolveSecretRefs(cfg, reg, context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSecretRefs_MapOfStructs(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "wh-sec")

	cfg := &Config{
		Webhooks: WebhooksConfig{
			Endpoints: []WebhookEndpoint{
				{Secret: "${env:WEBHOOK_SECRET}", URL: "https://example.com"},
			},
		},
	}

	reg := NewSecretRegistry()
	reg.Register(&EnvProvider{})

	if err := resolveSecretRefs(cfg, reg, context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Webhooks.Endpoints[0].Secret != "wh-sec" {
		t.Errorf("webhook secret: got %q", cfg.Webhooks.Endpoints[0].Secret)
	}
}

// --- Parse integration ---

func TestParseWithSecretRefs(t *testing.T) {
	t.Setenv("PARSE_JWT_SECRET", "my-jwt-secret")

	yamlData := `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
authentication:
  jwt:
    enabled: true
    secret: "${env:PARSE_JWT_SECRET}"
    algorithm: "HS256"
routes:
  - id: "test"
    path: "/test"
    backends:
      - url: "http://localhost:9001"
`
	loader := NewLoader()
	cfg, err := loader.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Authentication.JWT.Secret != "my-jwt-secret" {
		t.Errorf("JWT secret: got %q, want %q", cfg.Authentication.JWT.Secret, "my-jwt-secret")
	}
}

func TestParseWithMixedBareAndSchemeRefs(t *testing.T) {
	t.Setenv("BARE_VAR", "bare-value")
	t.Setenv("STRICT_VAR", "strict-value")

	yamlData := `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
authentication:
  jwt:
    enabled: true
    secret: "${env:STRICT_VAR}"
    algorithm: "HS256"
  ldap:
    bind_password: "${BARE_VAR}"
routes:
  - id: "test"
    path: "/test"
    backends:
      - url: "http://localhost:9001"
`
	loader := NewLoader()
	cfg, err := loader.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Authentication.JWT.Secret != "strict-value" {
		t.Errorf("strict ref: got %q, want %q", cfg.Authentication.JWT.Secret, "strict-value")
	}
	if cfg.Authentication.LDAP.BindPassword != "bare-value" {
		t.Errorf("bare ref: got %q, want %q", cfg.Authentication.LDAP.BindPassword, "bare-value")
	}
}

func TestParseWithFileRef(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "jwt-secret")
	os.WriteFile(secretPath, []byte("file-jwt-secret\n"), 0o600)

	yamlData := `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
authentication:
  jwt:
    enabled: true
    secret: "${file:` + secretPath + `}"
    algorithm: "HS256"
routes:
  - id: "test"
    path: "/test"
    backends:
      - url: "http://localhost:9001"
`
	loader := NewLoader()
	cfg, err := loader.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Authentication.JWT.Secret != "file-jwt-secret" {
		t.Errorf("got %q, want %q", cfg.Authentication.JWT.Secret, "file-jwt-secret")
	}
}

func TestParseWithMissingStrictRef(t *testing.T) {
	yamlData := `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
authentication:
  jwt:
    secret: "${env:DEFINITELY_UNSET_XYZ}"
routes:
  - id: "test"
    path: "/test"
    backends:
      - url: "http://localhost:9001"
`
	loader := NewLoader()
	_, err := loader.Parse([]byte(yamlData))
	if err == nil {
		t.Fatal("expected error for missing env var in strict ref")
	}
}
