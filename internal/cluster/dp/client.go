package dp

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/cespare/xxhash/v2"
	"github.com/goccy/go-yaml"
	"github.com/google/uuid"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/cluster/clusterpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReloadResult mirrors the gateway ReloadResult for the reloadFn callback.
type ReloadResult struct {
	Success   bool
	Timestamp time.Time
	Error     string
}

// Client connects to the control plane and receives config updates.
type Client struct {
	address   string
	nodeID    string
	version   string
	hostname  string
	tlsCfg    *tls.Config
	cacheDir  string
	dpCluster config.ClusterConfig // DP's own cluster config to overlay on received configs

	retryInterval     time.Duration
	heartbeatInterval time.Duration

	reloadFn func(cfg *config.Config) ReloadResult

	connected       atomic.Bool
	hasConfig       atomic.Bool
	currentHash     atomic.Uint64
	currentVer      atomic.Uint64
	lastReloadError atomic.Value // string

	logger *zap.Logger
}

// ClientConfig holds configuration for creating a new DP client.
type ClientConfig struct {
	Address           string
	NodeID            string
	Version           string
	Hostname          string
	TLSConfig         *tls.Config
	CacheDir          string
	RetryInterval     time.Duration
	HeartbeatInterval time.Duration
	DPCluster         config.ClusterConfig
	ReloadFn          func(cfg *config.Config) ReloadResult
	Logger            *zap.Logger
}

// NewClient creates a new data plane client.
func NewClient(cfg ClientConfig) *Client {
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = 5 * time.Second
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = "/var/lib/gateway/cluster"
	}

	c := &Client{
		address:           cfg.Address,
		nodeID:            cfg.NodeID,
		version:           cfg.Version,
		hostname:          cfg.Hostname,
		tlsCfg:            cfg.TLSConfig,
		cacheDir:          cfg.CacheDir,
		dpCluster:         cfg.DPCluster,
		retryInterval:     cfg.RetryInterval,
		heartbeatInterval: cfg.HeartbeatInterval,
		reloadFn:          cfg.ReloadFn,
		logger:            cfg.Logger,
	}
	c.lastReloadError.Store("")
	return c
}

// HasConfig returns true if the DP has a valid config (from CP or cache).
func (c *Client) HasConfig() bool {
	return c.hasConfig.Load()
}

// Connected returns true if the DP is currently connected to the CP.
func (c *Client) Connected() bool {
	return c.connected.Load()
}

// ConfigVersion returns the current config version.
func (c *Client) ConfigVersion() uint64 {
	return c.currentVer.Load()
}

// ConfigHash returns the current config hash.
func (c *Client) ConfigHash() uint64 {
	return c.currentHash.Load()
}

// NodeID returns the node ID.
func (c *Client) NodeID() string {
	return c.nodeID
}

// LastReloadError returns the last reload error (empty on success).
func (c *Client) LastReloadError() string {
	v, _ := c.lastReloadError.Load().(string)
	return v
}

// Run starts the DP client loop. It blocks until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	// Ensure cache directory exists
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		c.logger.Error("Failed to create cache directory", zap.String("dir", c.cacheDir), zap.Error(err))
	}

	// Auto-generate node ID if empty
	c.initNodeID()

	// Try loading cached config for static stability
	c.loadCachedConfig()

	// Connection loop with exponential backoff
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = c.retryInterval
	bo.MaxInterval = 60 * time.Second
	bo.MaxElapsedTime = 0 // never give up

	for {
		err := c.connectAndStream(ctx)
		if ctx.Err() != nil {
			return // context cancelled, shut down
		}

		c.connected.Store(false)
		c.logger.Warn("Disconnected from control plane, reconnecting...",
			zap.Error(err),
		)

		wait := bo.NextBackOff()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) initNodeID() {
	if c.nodeID != "" {
		return
	}

	// Try to load persisted node ID
	idPath := filepath.Join(c.cacheDir, "node_id")
	if data, err := os.ReadFile(idPath); err == nil && len(data) > 0 {
		c.nodeID = string(data)
		return
	}

	// Generate new UUID
	c.nodeID = uuid.New().String()
	_ = atomicWrite(idPath, []byte(c.nodeID))
	c.logger.Info("Generated node ID", zap.String("node_id", c.nodeID))
}

func (c *Client) loadCachedConfig() {
	cachePath := filepath.Join(c.cacheDir, "config.yaml")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return // no cache, that's fine
	}

	cfg := config.DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		c.logger.Warn("Failed to parse cached config", zap.Error(err))
		return
	}

	// Overlay DP's own cluster config
	cfg.Cluster = c.dpCluster

	if err := config.Validate(cfg); err != nil {
		c.logger.Warn("Cached config validation failed", zap.Error(err))
		return
	}

	result := c.reloadFn(cfg)
	if result.Success {
		c.hasConfig.Store(true)
		c.currentHash.Store(xxhash.Sum64(data))
		c.logger.Info("Loaded cached config for static stability")
	} else {
		c.logger.Warn("Failed to apply cached config", zap.String("error", result.Error))
	}
}

func (c *Client) connectAndStream(ctx context.Context) error {
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(c.tlsCfg)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	conn, err := grpc.NewClient(c.address, dialOpts...)
	if err != nil {
		return fmt.Errorf("dial CP: %w", err)
	}
	defer conn.Close()

	client := clusterpb.NewClusterServiceClient(conn)
	stream, err := client.ConfigStream(ctx)
	if err != nil {
		return fmt.Errorf("open config stream: %w", err)
	}

	// Send connect request
	if err := stream.Send(&clusterpb.NodeMessage{
		Msg: &clusterpb.NodeMessage_Connect{
			Connect: &clusterpb.ConnectRequest{
				NodeId:     c.nodeID,
				Version:    c.version,
				Hostname:   c.hostname,
				ConfigHash: c.currentHash.Load(),
			},
		},
	}); err != nil {
		return fmt.Errorf("send connect: %w", err)
	}

	c.connected.Store(true)
	c.logger.Info("Connected to control plane", zap.String("address", c.address))

	// Two-goroutine pattern: recv goroutine pushes ConfigUpdate, send goroutine sends heartbeats
	type recvResult struct {
		update *clusterpb.ConfigUpdate
		err    error
	}
	recvCh := make(chan recvResult, 1)

	go func() {
		for {
			update, err := stream.Recv()
			if err != nil {
				recvCh <- recvResult{err: err}
				return
			}
			recvCh <- recvResult{update: update}
		}
	}()

	heartbeatTicker := time.NewTicker(c.heartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case result := <-recvCh:
			if result.err != nil {
				return fmt.Errorf("recv: %w", result.err)
			}
			c.handleConfigUpdate(result.update)

		case <-heartbeatTicker.C:
			reloadErr := c.LastReloadError()
			if err := stream.Send(&clusterpb.NodeMessage{
				Msg: &clusterpb.NodeMessage_Heartbeat{
					Heartbeat: &clusterpb.HeartbeatRequest{
						NodeId:        c.nodeID,
						ConfigVersion: c.currentVer.Load(),
						ConfigHash:    c.currentHash.Load(),
						Status: &clusterpb.NodeStatus{
							GatewayVersion: c.version,
							LastReloadError: reloadErr,
							LastSuccessfulVersion: c.currentVer.Load(),
						},
						Timestamp: timestamppb.Now(),
					},
				},
			}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) handleConfigUpdate(update *clusterpb.ConfigUpdate) {
	// Verify hash integrity
	computedHash := xxhash.Sum64(update.ConfigYaml)
	if computedHash != update.ConfigHash {
		c.logger.Error("Config hash mismatch, rejecting update",
			zap.Uint64("expected", update.ConfigHash),
			zap.Uint64("computed", computedHash),
		)
		return
	}

	// Parse config
	cfg := config.DefaultConfig()
	if err := yaml.Unmarshal(update.ConfigYaml, cfg); err != nil {
		errMsg := fmt.Sprintf("unmarshal config: %v", err)
		c.logger.Error("Failed to parse config from CP", zap.Error(err))
		c.lastReloadError.Store(errMsg)
		return
	}

	// Overlay DP's own cluster config
	cfg.Cluster = c.dpCluster

	// Validate
	if err := config.Validate(cfg); err != nil {
		errMsg := fmt.Sprintf("validate config: %v", err)
		c.logger.Error("Config validation failed", zap.Error(err))
		c.lastReloadError.Store(errMsg)
		return
	}

	// Apply config
	result := c.reloadFn(cfg)
	if result.Success {
		c.currentVer.Store(update.Version)
		c.currentHash.Store(update.ConfigHash)
		c.hasConfig.Store(true)
		c.lastReloadError.Store("")

		// Write to disk cache (atomic write)
		cachePath := filepath.Join(c.cacheDir, "config.yaml")
		if err := atomicWrite(cachePath, update.ConfigYaml); err != nil {
			c.logger.Error("Failed to cache config to disk", zap.Error(err))
		}

		c.logger.Info("Applied config from control plane",
			zap.Uint64("version", update.Version),
			zap.String("source", update.Source),
		)
	} else {
		c.lastReloadError.Store(result.Error)
		c.logger.Error("Failed to apply config from CP",
			zap.Uint64("version", update.Version),
			zap.String("error", result.Error),
		)
		// Do NOT write failed config to disk cache
	}
}

// atomicWrite writes data to a file atomically using tmp+fsync+rename.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, path)
}
