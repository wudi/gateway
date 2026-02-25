package config

import (
	"testing"
)

func TestClusterValidation(t *testing.T) {
	base := func() *Config {
		cfg := DefaultConfig()
		cfg.Routes = []RouteConfig{{
			ID:       "test",
			Path:     "/test",
			Backends: []BackendConfig{{URL: "http://localhost:8080"}},
		}}
		return cfg
	}

	t.Run("standalone_is_default", func(t *testing.T) {
		cfg := base()
		if err := Validate(cfg); err != nil {
			t.Fatalf("standalone config should be valid: %v", err)
		}
	})

	t.Run("explicit_standalone", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "standalone"
		if err := Validate(cfg); err != nil {
			t.Fatalf("explicit standalone should be valid: %v", err)
		}
	})

	t.Run("invalid_role", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "leader"
		if err := Validate(cfg); err == nil {
			t.Fatal("expected error for invalid role")
		}
	})

	t.Run("cp_missing_address", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "control_plane"
		cfg.Cluster.ControlPlane.TLS.Enabled = true
		cfg.Cluster.ControlPlane.TLS.CertFile = "cert.pem"
		cfg.Cluster.ControlPlane.TLS.KeyFile = "key.pem"
		cfg.Cluster.ControlPlane.TLS.ClientCAFile = "ca.pem"
		if err := Validate(cfg); err == nil {
			t.Fatal("expected error for missing CP address")
		}
	})

	t.Run("cp_missing_tls", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "control_plane"
		cfg.Cluster.ControlPlane.Address = ":9443"
		if err := Validate(cfg); err == nil {
			t.Fatal("expected error for missing TLS")
		}
	})

	t.Run("cp_missing_client_ca", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "control_plane"
		cfg.Cluster.ControlPlane.Address = ":9443"
		cfg.Cluster.ControlPlane.TLS.Enabled = true
		cfg.Cluster.ControlPlane.TLS.CertFile = "cert.pem"
		cfg.Cluster.ControlPlane.TLS.KeyFile = "key.pem"
		if err := Validate(cfg); err == nil {
			t.Fatal("expected error for missing client_ca_file")
		}
	})

	t.Run("cp_valid", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "control_plane"
		cfg.Cluster.ControlPlane.Address = ":9443"
		cfg.Cluster.ControlPlane.TLS.Enabled = true
		cfg.Cluster.ControlPlane.TLS.CertFile = "cert.pem"
		cfg.Cluster.ControlPlane.TLS.KeyFile = "key.pem"
		cfg.Cluster.ControlPlane.TLS.ClientCAFile = "ca.pem"
		if err := Validate(cfg); err != nil {
			t.Fatalf("valid CP config should pass: %v", err)
		}
	})

	t.Run("dp_missing_address", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "data_plane"
		cfg.Cluster.DataPlane.TLS.Enabled = true
		cfg.Cluster.DataPlane.TLS.CertFile = "cert.pem"
		cfg.Cluster.DataPlane.TLS.KeyFile = "key.pem"
		cfg.Cluster.DataPlane.TLS.CAFile = "ca.pem"
		if err := Validate(cfg); err == nil {
			t.Fatal("expected error for missing DP address")
		}
	})

	t.Run("dp_missing_tls", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "data_plane"
		cfg.Cluster.DataPlane.Address = "cp:9443"
		if err := Validate(cfg); err == nil {
			t.Fatal("expected error for missing TLS")
		}
	})

	t.Run("dp_missing_ca_file", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "data_plane"
		cfg.Cluster.DataPlane.Address = "cp:9443"
		cfg.Cluster.DataPlane.TLS.Enabled = true
		cfg.Cluster.DataPlane.TLS.CertFile = "cert.pem"
		cfg.Cluster.DataPlane.TLS.KeyFile = "key.pem"
		if err := Validate(cfg); err == nil {
			t.Fatal("expected error for missing ca_file")
		}
	})

	t.Run("dp_valid", func(t *testing.T) {
		cfg := base()
		cfg.Cluster.Role = "data_plane"
		cfg.Cluster.DataPlane.Address = "cp:9443"
		cfg.Cluster.DataPlane.TLS.Enabled = true
		cfg.Cluster.DataPlane.TLS.CertFile = "cert.pem"
		cfg.Cluster.DataPlane.TLS.KeyFile = "key.pem"
		cfg.Cluster.DataPlane.TLS.CAFile = "ca.pem"
		if err := Validate(cfg); err != nil {
			t.Fatalf("valid DP config should pass: %v", err)
		}
	})

	t.Run("dp_empty_routes_ok", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Cluster.Role = "data_plane"
		cfg.Cluster.DataPlane.Address = "cp:9443"
		cfg.Cluster.DataPlane.TLS.Enabled = true
		cfg.Cluster.DataPlane.TLS.CertFile = "cert.pem"
		cfg.Cluster.DataPlane.TLS.KeyFile = "key.pem"
		cfg.Cluster.DataPlane.TLS.CAFile = "ca.pem"
		// No routes — DP gets them from CP. Should still pass validation.
		if err := Validate(cfg); err != nil {
			t.Fatalf("DP with empty routes should be valid: %v", err)
		}
	})
}
