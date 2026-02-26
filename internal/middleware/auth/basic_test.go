package auth

import (
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/wudi/runway/config"
)

func mustHash(password string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}

func TestBasicAuth_ValidCredentials(t *testing.T) {
	hash := mustHash("secret123")
	ba := NewBasicAuth(config.BasicAuthConfig{
		Enabled: true,
		Users: []config.BasicAuthUser{
			{Username: "alice", PasswordHash: hash, ClientID: "client-alice", Roles: []string{"admin"}},
		},
	})

	req, _ := http.NewRequest("GET", "/", nil)
	req.SetBasicAuth("alice", "secret123")

	identity, err := ba.Authenticate(req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if identity.ClientID != "client-alice" {
		t.Errorf("expected client-alice, got %s", identity.ClientID)
	}
	if identity.AuthType != "basic" {
		t.Errorf("expected auth type basic, got %s", identity.AuthType)
	}
	if identity.Claims["username"] != "alice" {
		t.Errorf("expected username alice in claims, got %v", identity.Claims["username"])
	}
	roles, ok := identity.Claims["roles"].([]string)
	if !ok || len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("expected roles [admin], got %v", identity.Claims["roles"])
	}
}

func TestBasicAuth_WrongPassword(t *testing.T) {
	hash := mustHash("correct")
	ba := NewBasicAuth(config.BasicAuthConfig{
		Enabled: true,
		Users: []config.BasicAuthUser{
			{Username: "bob", PasswordHash: hash, ClientID: "client-bob"},
		},
	})

	req, _ := http.NewRequest("GET", "/", nil)
	req.SetBasicAuth("bob", "wrong")

	_, err := ba.Authenticate(req)
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestBasicAuth_UnknownUser(t *testing.T) {
	hash := mustHash("pass")
	ba := NewBasicAuth(config.BasicAuthConfig{
		Enabled: true,
		Users: []config.BasicAuthUser{
			{Username: "exists", PasswordHash: hash, ClientID: "c1"},
		},
	})

	req, _ := http.NewRequest("GET", "/", nil)
	req.SetBasicAuth("noone", "pass")

	_, err := ba.Authenticate(req)
	if err == nil {
		t.Fatal("expected error for unknown user")
	}
}

func TestBasicAuth_MissingHeader(t *testing.T) {
	hash := mustHash("pass")
	ba := NewBasicAuth(config.BasicAuthConfig{
		Enabled: true,
		Users: []config.BasicAuthUser{
			{Username: "user", PasswordHash: hash, ClientID: "c1"},
		},
	})

	req, _ := http.NewRequest("GET", "/", nil)
	// no BasicAuth header set

	_, err := ba.Authenticate(req)
	if err == nil {
		t.Fatal("expected error for missing header")
	}
}

func TestBasicAuth_NoRoles(t *testing.T) {
	hash := mustHash("pass")
	ba := NewBasicAuth(config.BasicAuthConfig{
		Enabled: true,
		Users: []config.BasicAuthUser{
			{Username: "user", PasswordHash: hash, ClientID: "c1"},
		},
	})

	req, _ := http.NewRequest("GET", "/", nil)
	req.SetBasicAuth("user", "pass")

	identity, err := ba.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := identity.Claims["roles"]; ok {
		t.Error("expected no roles key in claims when roles are empty")
	}
}

func TestBasicAuth_IsEnabled(t *testing.T) {
	ba := NewBasicAuth(config.BasicAuthConfig{
		Enabled: true,
		Users:   []config.BasicAuthUser{},
	})
	if ba.IsEnabled() {
		t.Error("expected IsEnabled=false with no users")
	}

	ba2 := NewBasicAuth(config.BasicAuthConfig{
		Enabled: true,
		Users: []config.BasicAuthUser{
			{Username: "u", PasswordHash: mustHash("p"), ClientID: "c"},
		},
	})
	if !ba2.IsEnabled() {
		t.Error("expected IsEnabled=true with users")
	}
}

func TestBasicAuth_DefaultRealm(t *testing.T) {
	ba := NewBasicAuth(config.BasicAuthConfig{})
	if ba.Realm() != "Restricted" {
		t.Errorf("expected default realm Restricted, got %s", ba.Realm())
	}

	ba2 := NewBasicAuth(config.BasicAuthConfig{Realm: "MyApp"})
	if ba2.Realm() != "MyApp" {
		t.Errorf("expected realm MyApp, got %s", ba2.Realm())
	}
}
