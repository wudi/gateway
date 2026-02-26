package config

import (
	"testing"
)

func TestRedactConfig_AllFields(t *testing.T) {
	cfg := DefaultConfig()

	// Set all sensitive fields
	cfg.Authentication.JWT.Secret = "jwt-secret"
	cfg.Authentication.OAuth.ClientSecret = "oauth-secret"
	cfg.Authentication.LDAP.BindPassword = "ldap-pass"
	cfg.Authentication.SAML.Session.SigningKey = "saml-key"
	cfg.Redis.Password = "redis-pass"
	cfg.Registry.Consul.Token = "consul-token"
	cfg.Registry.Etcd.Password = "etcd-pass"
	cfg.CSRF.Secret = "csrf-secret"
	cfg.BackendSigning.Secret = "signing-secret"
	cfg.BackendSigning.PrivateKey = "pem-key"
	cfg.InboundSigning.Secret = "inbound-secret"
	cfg.Webhooks.Endpoints = []WebhookEndpoint{{Secret: "wh-secret", URL: "https://example.com"}}

	redacted, err := RedactConfig(cfg)
	if err != nil {
		t.Fatalf("RedactConfig error: %v", err)
	}

	checks := []struct {
		name string
		got  string
	}{
		{"JWT.Secret", redacted.Authentication.JWT.Secret},
		{"OAuth.ClientSecret", redacted.Authentication.OAuth.ClientSecret},
		{"LDAP.BindPassword", redacted.Authentication.LDAP.BindPassword},
		{"SAML.Session.SigningKey", redacted.Authentication.SAML.Session.SigningKey},
		{"Redis.Password", redacted.Redis.Password},
		{"Consul.Token", redacted.Registry.Consul.Token},
		{"Etcd.Password", redacted.Registry.Etcd.Password},
		{"CSRF.Secret", redacted.CSRF.Secret},
		{"BackendSigning.Secret", redacted.BackendSigning.Secret},
		{"BackendSigning.PrivateKey", redacted.BackendSigning.PrivateKey},
		{"InboundSigning.Secret", redacted.InboundSigning.Secret},
		{"Webhook.Secret", redacted.Webhooks.Endpoints[0].Secret},
	}

	for _, c := range checks {
		if c.got != RedactedValue {
			t.Errorf("%s: got %q, want %q", c.name, c.got, RedactedValue)
		}
	}
}

func TestRedactConfig_EmptyStaysEmpty(t *testing.T) {
	cfg := DefaultConfig()
	// Leave JWT.Secret empty
	cfg.Authentication.JWT.Secret = ""
	cfg.Redis.Password = "notempty"

	redacted, err := RedactConfig(cfg)
	if err != nil {
		t.Fatalf("RedactConfig error: %v", err)
	}

	if redacted.Authentication.JWT.Secret != "" {
		t.Errorf("empty field got redacted: %q", redacted.Authentication.JWT.Secret)
	}
	if redacted.Redis.Password != RedactedValue {
		t.Errorf("non-empty field not redacted: %q", redacted.Redis.Password)
	}
}

func TestRedactConfig_OriginalUnchanged(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Authentication.JWT.Secret = "original-secret"

	_, err := RedactConfig(cfg)
	if err != nil {
		t.Fatalf("RedactConfig error: %v", err)
	}

	if cfg.Authentication.JWT.Secret != "original-secret" {
		t.Errorf("original was mutated: got %q", cfg.Authentication.JWT.Secret)
	}
}

func TestRedactConfig_PerRouteSecrets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Routes = []RouteConfig{
		{
			ID:   "test",
			Path: "/test",
			BackendAuth: BackendAuthConfig{
				ClientSecret: "ba-secret",
			},
			TokenExchange: TokenExchangeConfig{
				ClientSecret:  "te-cs",
				SigningKey:     "te-sk",
				SigningSecret:  "te-ss",
			},
			AI: AIConfig{
				APIKey: "ai-key",
			},
			ResponseSigning: ResponseSigningConfig{
				Secret: "rs-secret",
			},
			FieldEncryption: FieldEncryptionConfig{
				KeyBase64: "enc-key",
			},
		},
	}

	redacted, err := RedactConfig(cfg)
	if err != nil {
		t.Fatalf("RedactConfig error: %v", err)
	}

	r := redacted.Routes[0]
	checks := []struct {
		name string
		got  string
	}{
		{"BackendAuth.ClientSecret", r.BackendAuth.ClientSecret},
		{"TokenExchange.ClientSecret", r.TokenExchange.ClientSecret},
		{"TokenExchange.SigningKey", r.TokenExchange.SigningKey},
		{"TokenExchange.SigningSecret", r.TokenExchange.SigningSecret},
		{"AI.APIKey", r.AI.APIKey},
		{"ResponseSigning.Secret", r.ResponseSigning.Secret},
		{"FieldEncryption.KeyBase64", r.FieldEncryption.KeyBase64},
	}

	for _, c := range checks {
		if c.got != RedactedValue {
			t.Errorf("%s: got %q, want %q", c.name, c.got, RedactedValue)
		}
	}
}

func TestRedactConfig_APIKeys(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Authentication.APIKey.Keys = []APIKeyEntry{
		{Key: "key-1", ClientID: "c1"},
		{Key: "key-2", ClientID: "c2"},
		{Key: "", ClientID: "c3"}, // empty should stay empty
	}

	redacted, err := RedactConfig(cfg)
	if err != nil {
		t.Fatalf("RedactConfig error: %v", err)
	}

	keys := redacted.Authentication.APIKey.Keys
	if keys[0].Key != RedactedValue {
		t.Errorf("key-1: got %q", keys[0].Key)
	}
	if keys[1].Key != RedactedValue {
		t.Errorf("key-2: got %q", keys[1].Key)
	}
	if keys[2].Key != "" {
		t.Errorf("empty key: got %q", keys[2].Key)
	}
	// ClientIDs should be preserved
	if keys[0].ClientID != "c1" {
		t.Errorf("client_id changed: %q", keys[0].ClientID)
	}
}
