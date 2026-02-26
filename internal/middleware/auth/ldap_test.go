package auth

import (
	"net/http"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/variables"
)

func newTestLDAPAuth(t *testing.T) *LDAPAuth {
	t.Helper()
	a, err := NewLDAPAuth(config.LDAPConfig{
		Enabled:          true,
		URL:              "ldap://localhost:389",
		BindDN:           "cn=admin,dc=example,dc=org",
		BindPassword:     "admin",
		UserSearchBase:   "dc=example,dc=org",
		UserSearchFilter: "(uid={{username}})",
	})
	if err != nil {
		t.Fatalf("NewLDAPAuth failed: %v", err)
	}
	return a
}

func TestLDAPAuth_Defaults(t *testing.T) {
	a := newTestLDAPAuth(t)
	defer a.Close()

	if a.connTimeout != 10*time.Second {
		t.Errorf("expected connTimeout 10s, got %v", a.connTimeout)
	}
	if a.cacheTTL != 5*time.Minute {
		t.Errorf("expected cacheTTL 5m, got %v", a.cacheTTL)
	}
	if a.maxConnLifetime != 5*time.Minute {
		t.Errorf("expected maxConnLifetime 5m, got %v", a.maxConnLifetime)
	}
	if cap(a.pool) != 5 {
		t.Errorf("expected pool size 5, got %d", cap(a.pool))
	}
	if a.attrClientID != "uid" {
		t.Errorf("expected clientID attr uid, got %s", a.attrClientID)
	}
	if a.groupAttribute != "cn" {
		t.Errorf("expected group attr cn, got %s", a.groupAttribute)
	}
	if a.searchScope != 2 { // ldap.ScopeWholeSubtree
		t.Errorf("expected search scope 2 (sub), got %d", a.searchScope)
	}
}

func TestLDAPAuth_CustomDefaults(t *testing.T) {
	a, err := NewLDAPAuth(config.LDAPConfig{
		Enabled:          true,
		URL:              "ldap://localhost:389",
		BindDN:           "cn=admin,dc=example,dc=org",
		BindPassword:     "admin",
		UserSearchBase:   "dc=example,dc=org",
		UserSearchFilter: "(uid={{username}})",
		UserSearchScope:  "one",
		PoolSize:         10,
		CacheTTL:         10 * time.Minute,
		ConnTimeout:      5 * time.Second,
		MaxConnLifetime:  2 * time.Minute,
		GroupAttribute:   "memberOf",
		AttributeMapping: config.LDAPAttributeMapping{
			ClientID: "sAMAccountName",
			Email:    "mail",
		},
		Realm: "MyOrg",
	})
	if err != nil {
		t.Fatalf("NewLDAPAuth failed: %v", err)
	}
	defer a.Close()

	if cap(a.pool) != 10 {
		t.Errorf("expected pool size 10, got %d", cap(a.pool))
	}
	if a.cacheTTL != 10*time.Minute {
		t.Errorf("expected cacheTTL 10m, got %v", a.cacheTTL)
	}
	if a.attrClientID != "sAMAccountName" {
		t.Errorf("expected sAMAccountName, got %s", a.attrClientID)
	}
	if a.searchScope != 1 { // ldap.ScopeSingleLevel
		t.Errorf("expected scope 1 (one), got %d", a.searchScope)
	}
	if a.Realm() != "MyOrg" {
		t.Errorf("expected realm MyOrg, got %s", a.Realm())
	}
}

func TestLDAPAuth_IsEnabled(t *testing.T) {
	a := newTestLDAPAuth(t)
	defer a.Close()
	if !a.IsEnabled() {
		t.Error("expected IsEnabled=true")
	}

	empty, _ := NewLDAPAuth(config.LDAPConfig{URL: ""})
	if empty.IsEnabled() {
		t.Error("expected IsEnabled=false with empty URL")
	}
}

func TestLDAPAuth_Stats(t *testing.T) {
	a := newTestLDAPAuth(t)
	defer a.Close()

	stats := a.Stats()
	if stats.Attempts != 0 || stats.Successes != 0 || stats.Failures != 0 {
		t.Errorf("expected zero stats, got %+v", stats)
	}
}

func TestLDAPAuth_MissingBasicAuth(t *testing.T) {
	a := newTestLDAPAuth(t)
	defer a.Close()

	req, _ := http.NewRequest("GET", "/", nil)
	_, err := a.Authenticate(req)
	if err == nil {
		t.Fatal("expected error for missing basic auth header")
	}
}

func TestLDAPAuth_CacheHitMiss(t *testing.T) {
	a := newTestLDAPAuth(t)
	defer a.Close()

	// Manually populate cache
	a.cache.Add("testuser:testpass", &ldapCacheEntry{
		identity: &variables.Identity{
			ClientID: "cached-client",
			AuthType: "ldap",
			Claims:   map[string]interface{}{"username": "testuser"},
		},
		expiresAt: time.Now().Add(5 * time.Minute),
	})

	req, _ := http.NewRequest("GET", "/", nil)
	req.SetBasicAuth("testuser", "testpass")

	identity, err := a.Authenticate(req)
	if err != nil {
		t.Fatalf("expected cache hit, got error: %v", err)
	}
	if identity.ClientID != "cached-client" {
		t.Errorf("expected cached-client, got %s", identity.ClientID)
	}

	stats := a.Stats()
	if stats.CacheHits != 1 {
		t.Errorf("expected 1 cache hit, got %d", stats.CacheHits)
	}
	if stats.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", stats.Attempts)
	}
}

func TestLDAPAuth_CacheExpiry(t *testing.T) {
	a := newTestLDAPAuth(t)
	defer a.Close()

	// Add an expired entry
	a.cache.Add("expired:pass", &ldapCacheEntry{
		identity: &variables.Identity{
			ClientID: "old",
			AuthType: "ldap",
		},
		expiresAt: time.Now().Add(-1 * time.Minute),
	})

	req, _ := http.NewRequest("GET", "/", nil)
	req.SetBasicAuth("expired", "pass")

	// Will fail because no real LDAP server, but should be a cache miss
	_, _ = a.Authenticate(req)

	stats := a.Stats()
	if stats.CacheMisses != 1 {
		t.Errorf("expected 1 cache miss for expired entry, got %d", stats.CacheMisses)
	}
	if stats.CacheHits != 0 {
		t.Errorf("expected 0 cache hits, got %d", stats.CacheHits)
	}
}

func TestLDAPAuth_DefaultRealm(t *testing.T) {
	a := newTestLDAPAuth(t)
	defer a.Close()
	if a.Realm() != "Restricted" {
		t.Errorf("expected default realm Restricted, got %s", a.Realm())
	}
}

func TestLDAPAuth_Close(t *testing.T) {
	a := newTestLDAPAuth(t)
	// Close without panic even with empty pool
	a.Close()
}

func TestLDAPAuth_TLSConfig(t *testing.T) {
	a, err := NewLDAPAuth(config.LDAPConfig{
		Enabled:          true,
		URL:              "ldaps://localhost:636",
		BindDN:           "cn=admin,dc=example,dc=org",
		BindPassword:     "admin",
		UserSearchBase:   "dc=example,dc=org",
		UserSearchFilter: "(uid={{username}})",
		TLS: config.LDAPTLSConfig{
			SkipVerify: true,
		},
	})
	if err != nil {
		t.Fatalf("NewLDAPAuth failed: %v", err)
	}
	defer a.Close()

	if a.tlsConfig == nil {
		t.Fatal("expected TLS config to be set")
	}
	if !a.tlsConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true")
	}
}

func TestLDAPAuth_InvalidCAFile(t *testing.T) {
	_, err := NewLDAPAuth(config.LDAPConfig{
		Enabled:          true,
		URL:              "ldaps://localhost:636",
		BindDN:           "cn=admin,dc=example,dc=org",
		BindPassword:     "admin",
		UserSearchBase:   "dc=example,dc=org",
		UserSearchFilter: "(uid={{username}})",
		TLS: config.LDAPTLSConfig{
			CAFile: "/nonexistent/ca.pem",
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid CA file")
	}
}
