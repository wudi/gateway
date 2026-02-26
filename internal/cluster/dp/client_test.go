package dp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/cluster/clusterpb"
	"go.uber.org/zap"
)

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	data := []byte("hello world")

	if err := atomicWrite(path, data); err != nil {
		t.Fatalf("atomicWrite failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}

	// Temp file should not exist
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file should be cleaned up")
	}
}

func TestAtomicWriteNestedDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "test.txt")

	if err := atomicWrite(path, []byte("test")); err != nil {
		t.Fatalf("atomicWrite with nested dir failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(got) != "test" {
		t.Errorf("got %q, want %q", got, "test")
	}
}

func TestClientHasConfig(t *testing.T) {
	c := NewClient(ClientConfig{
		Logger: zap.NewNop(),
	})
	if c.HasConfig() {
		t.Error("new client should not have config")
	}
	c.hasConfig.Store(true)
	if !c.HasConfig() {
		t.Error("HasConfig should be true after storing")
	}
}

func TestClientNodeIDPersistence(t *testing.T) {
	dir := t.TempDir()
	c := NewClient(ClientConfig{
		CacheDir: dir,
		Logger:   zap.NewNop(),
	})
	c.initNodeID()

	if c.nodeID == "" {
		t.Fatal("nodeID should be generated")
	}

	// Persisted?
	idPath := filepath.Join(dir, "node_id")
	data, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("node_id file should exist: %v", err)
	}
	if string(data) != c.nodeID {
		t.Errorf("persisted nodeID %q != %q", data, c.nodeID)
	}

	// New client should pick up persisted ID
	c2 := NewClient(ClientConfig{
		CacheDir: dir,
		Logger:   zap.NewNop(),
	})
	c2.initNodeID()
	if c2.nodeID != c.nodeID {
		t.Errorf("second client got different nodeID: %q vs %q", c2.nodeID, c.nodeID)
	}
}

func TestClientExplicitNodeID(t *testing.T) {
	c := NewClient(ClientConfig{
		NodeID:   "my-node",
		CacheDir: t.TempDir(),
		Logger:   zap.NewNop(),
	})
	c.initNodeID()
	if c.nodeID != "my-node" {
		t.Errorf("expected explicit nodeID, got %q", c.nodeID)
	}
}

func TestHandleConfigUpdateHashMismatch(t *testing.T) {
	called := false
	c := NewClient(ClientConfig{
		CacheDir: t.TempDir(),
		DPCluster: config.ClusterConfig{
			Role: "data_plane",
		},
		ReloadFn: func(cfg *config.Config) ReloadResult {
			called = true
			return ReloadResult{Success: true}
		},
		Logger: zap.NewNop(),
	})

	// Send update with wrong hash
	c.handleConfigUpdate(&clusterpb.ConfigUpdate{
		Version:    1,
		ConfigYaml: []byte("test: true"),
		ConfigHash: 99999, // wrong hash
	})

	if called {
		t.Error("reloadFn should NOT be called when hash mismatches")
	}
}

func TestHandleConfigUpdateSuccess(t *testing.T) {
	dir := t.TempDir()
	var reloadedCfg *config.Config

	yamlData := []byte("listeners:\n  - id: default-http\n    address: \":8080\"\n    protocol: http\nroutes:\n  - id: test\n    path: /test\n    backends:\n      - url: http://localhost:9090\n")
	hash := xxhash.Sum64(yamlData)

	c := NewClient(ClientConfig{
		CacheDir: dir,
		DPCluster: config.ClusterConfig{
			Role: "data_plane",
			DataPlane: config.DataPlaneConfig{
				Address: "cp:9443",
				TLS:     config.TLSConfig{Enabled: true, CertFile: "c.pem", KeyFile: "k.pem", CAFile: "ca.pem"},
			},
		},
		ReloadFn: func(cfg *config.Config) ReloadResult {
			reloadedCfg = cfg
			return ReloadResult{Success: true}
		},
		Logger: zap.NewNop(),
	})

	c.handleConfigUpdate(&clusterpb.ConfigUpdate{
		Version:    5,
		ConfigYaml: yamlData,
		ConfigHash: hash,
		Source:     "test",
	})

	if reloadedCfg == nil {
		t.Fatal("reloadFn should have been called")
	}

	// DP's cluster config should be overlaid
	if reloadedCfg.Cluster.Role != "data_plane" {
		t.Errorf("cluster role should be overlaid, got %q", reloadedCfg.Cluster.Role)
	}

	if c.ConfigVersion() != 5 {
		t.Errorf("expected version 5, got %d", c.ConfigVersion())
	}
	if c.ConfigHash() != hash {
		t.Errorf("expected hash %d, got %d", hash, c.ConfigHash())
	}
	if !c.HasConfig() {
		t.Error("should have config after successful update")
	}
	if c.LastReloadError() != "" {
		t.Errorf("expected no error, got %q", c.LastReloadError())
	}

	// Check disk cache
	cached, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("cache file should exist: %v", err)
	}
	if string(cached) != string(yamlData) {
		t.Error("cached data should match original")
	}
}

func TestHandleConfigUpdateReloadFailure(t *testing.T) {
	dir := t.TempDir()
	yamlData := []byte("listeners:\n  - id: default-http\n    address: \":8080\"\n    protocol: http\nroutes:\n  - id: test\n    path: /test\n    backends:\n      - url: http://localhost:9090\n")
	hash := xxhash.Sum64(yamlData)

	c := NewClient(ClientConfig{
		CacheDir: dir,
		DPCluster: config.ClusterConfig{
			Role: "data_plane",
			DataPlane: config.DataPlaneConfig{
				Address: "cp:9443",
				TLS:     config.TLSConfig{Enabled: true, CertFile: "c.pem", KeyFile: "k.pem", CAFile: "ca.pem"},
			},
		},
		ReloadFn: func(cfg *config.Config) ReloadResult {
			return ReloadResult{Success: false, Error: "test failure"}
		},
		Logger: zap.NewNop(),
	})

	c.handleConfigUpdate(&clusterpb.ConfigUpdate{
		Version:    1,
		ConfigYaml: yamlData,
		ConfigHash: hash,
	})

	// Should NOT be cached to disk on failure
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(err) {
		t.Error("failed config should NOT be cached to disk")
	}

	if c.LastReloadError() != "test failure" {
		t.Errorf("expected reload error, got %q", c.LastReloadError())
	}
}

func TestLoadCachedConfig(t *testing.T) {
	dir := t.TempDir()
	yamlData := []byte("listeners:\n  - id: default-http\n    address: \":8080\"\n    protocol: http\nroutes:\n  - id: test\n    path: /test\n    backends:\n      - url: http://localhost:9090\n")

	// Write a cached config
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), yamlData, 0o644); err != nil {
		t.Fatal(err)
	}

	var called bool
	c := NewClient(ClientConfig{
		CacheDir: dir,
		DPCluster: config.ClusterConfig{
			Role: "data_plane",
			DataPlane: config.DataPlaneConfig{
				Address: "cp:9443",
				TLS:     config.TLSConfig{Enabled: true, CertFile: "c.pem", KeyFile: "k.pem", CAFile: "ca.pem"},
			},
		},
		ReloadFn: func(cfg *config.Config) ReloadResult {
			called = true
			return ReloadResult{Success: true}
		},
		Logger: zap.NewNop(),
	})

	c.loadCachedConfig()

	if !called {
		t.Error("reloadFn should be called with cached config")
	}
	if !c.HasConfig() {
		t.Error("should have config after loading from cache")
	}
}
