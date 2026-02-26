package cp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/wudi/runway/internal/cluster/clusterpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConfigEnvelope wraps a config snapshot for distribution to data planes.
type ConfigEnvelope struct {
	Version   uint64
	Hash      uint64
	YAML      []byte
	Timestamp time.Time
	Source    string // "file", "admin-api"
	Config    any    // *config.Config — stored as any to avoid import cycle
}

// ConnectedNode tracks a connected data plane node.
type ConnectedNode struct {
	NodeID        string    `json:"node_id"`
	Hostname      string    `json:"hostname"`
	Version       string    `json:"version"`
	ConfigVersion uint64    `json:"config_version"`
	ConfigHash    uint64    `json:"config_hash"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Status        string    `json:"status"` // "connected", "stale"
	NodeStatus    *clusterpb.NodeStatus `json:"node_status,omitempty"`
}

// Server implements the ClusterService gRPC server for the control plane.
type Server struct {
	clusterpb.UnimplementedClusterServiceServer

	mu        sync.RWMutex
	nodes     map[string]*connectedNode
	current   ConfigEnvelope
	broadcast chan struct{} // closed on push to wake all streams

	grpcServer *grpc.Server
	address    string
	version    string // runway binary version for DP compatibility check
	logger     *zap.Logger

	heartbeatInterval time.Duration // expected DP heartbeat interval for stale detection
	cancel            context.CancelFunc
}

type connectedNode struct {
	ConnectedNode
	lastSentVersion uint64
}

// NewServer creates a new control plane gRPC server.
func NewServer(address string, tlsCfg *tls.Config, version string, logger *zap.Logger) *Server {
	s := &Server{
		nodes:             make(map[string]*connectedNode),
		broadcast:         make(chan struct{}),
		address:           address,
		version:           version,
		logger:            logger,
		heartbeatInterval: 10 * time.Second,
	}

	grpcOpts := []grpc.ServerOption{
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    15 * time.Second,
			Timeout: 5 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	s.grpcServer = grpc.NewServer(grpcOpts...)
	clusterpb.RegisterClusterServiceServer(s.grpcServer, s)

	return s
}

// PushConfig stores a new config envelope and wakes all connected streams.
func (s *Server) PushConfig(env ConfigEnvelope) {
	s.mu.Lock()
	if env.Version == 0 {
		env.Version = s.current.Version + 1
	}
	s.current = env
	old := s.broadcast
	s.broadcast = make(chan struct{})
	s.mu.Unlock()

	close(old) // wake all waiting streams
	s.logger.Info("Config pushed to cluster",
		zap.Uint64("version", env.Version),
		zap.Uint64("hash", env.Hash),
		zap.String("source", env.Source),
	)
}

// CurrentConfig returns the current config envelope.
func (s *Server) CurrentConfig() ConfigEnvelope {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// ConnectedNodes returns a snapshot of all connected nodes.
func (s *Server) ConnectedNodes() []ConnectedNode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ConnectedNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		result = append(result, n.ConnectedNode)
	}
	return result
}

// ConfigStream implements the bidirectional streaming RPC.
func (s *Server) ConfigStream(stream grpc.BidiStreamingServer[clusterpb.NodeMessage, clusterpb.ConfigUpdate]) error {
	// First message must be ConnectRequest
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	req := msg.GetConnect()
	if req == nil {
		return status.Error(codes.InvalidArgument, "first message must be ConnectRequest")
	}

	// Version compatibility check (major.minor must match)
	if !compatibleVersion(s.version, req.Version) {
		s.logger.Warn("Rejecting DP with incompatible version",
			zap.String("node_id", req.NodeId),
			zap.String("dp_version", req.Version),
			zap.String("cp_version", s.version),
		)
		return status.Errorf(codes.FailedPrecondition,
			"version mismatch: CP=%s DP=%s (major.minor must match)", s.version, req.Version)
	}

	nodeID := req.NodeId
	s.logger.Info("Data plane connected",
		zap.String("node_id", nodeID),
		zap.String("hostname", req.Hostname),
		zap.String("version", req.Version),
	)

	// Register node
	s.mu.Lock()
	node := &connectedNode{
		ConnectedNode: ConnectedNode{
			NodeID:        nodeID,
			Hostname:      req.Hostname,
			Version:       req.Version,
			ConfigHash:    req.ConfigHash,
			LastHeartbeat: time.Now(),
			Status:        "connected",
		},
	}
	s.nodes[nodeID] = node

	// Send current config immediately if hash differs
	current := s.current
	bcast := s.broadcast
	s.mu.Unlock()

	if len(current.YAML) > 0 && current.Hash != req.ConfigHash {
		if err := stream.Send(&clusterpb.ConfigUpdate{
			Version:    current.Version,
			ConfigYaml: current.YAML,
			ConfigHash: current.Hash,
			Timestamp:  timestamppb.New(current.Timestamp),
			Source:     current.Source,
		}); err != nil {
			s.removeNode(nodeID)
			return err
		}
		s.mu.Lock()
		node.lastSentVersion = current.Version
		s.mu.Unlock()
	}

	// Two-goroutine pattern: recv goroutine + main select loop
	type recvResult struct {
		hb  *clusterpb.HeartbeatRequest
		err error
	}
	recvCh := make(chan recvResult, 1)

	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				recvCh <- recvResult{err: err}
				return
			}
			hb := msg.GetHeartbeat()
			if hb != nil {
				recvCh <- recvResult{hb: hb}
			}
			// Ignore unexpected message types
		}
	}()

	for {
		select {
		case <-bcast:
			// New config available
			s.mu.RLock()
			current = s.current
			bcast = s.broadcast
			lastSent := node.lastSentVersion
			s.mu.RUnlock()

			if current.Version > lastSent {
				if err := stream.Send(&clusterpb.ConfigUpdate{
					Version:    current.Version,
					ConfigYaml: current.YAML,
					ConfigHash: current.Hash,
					Timestamp:  timestamppb.New(current.Timestamp),
					Source:     current.Source,
				}); err != nil {
					s.removeNode(nodeID)
					return err
				}
				s.mu.Lock()
				node.lastSentVersion = current.Version
				s.mu.Unlock()
			}

		case result := <-recvCh:
			if result.err != nil {
				s.removeNode(nodeID)
				s.logger.Info("Data plane disconnected",
					zap.String("node_id", nodeID),
					zap.Error(result.err),
				)
				return nil // Don't propagate client disconnect as error
			}
			// Update heartbeat
			s.mu.Lock()
			node.LastHeartbeat = time.Now()
			node.ConfigVersion = result.hb.ConfigVersion
			node.ConfigHash = result.hb.ConfigHash
			node.NodeStatus = result.hb.Status
			node.Status = "connected"
			s.mu.Unlock()

		case <-stream.Context().Done():
			s.removeNode(nodeID)
			s.logger.Info("Data plane stream ended",
				zap.String("node_id", nodeID),
			)
			return nil
		}
	}
}

func (s *Server) removeNode(nodeID string) {
	s.mu.Lock()
	delete(s.nodes, nodeID)
	s.mu.Unlock()
}

// Start begins listening and serving gRPC requests.
func (s *Server) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("cluster CP listen: %w", err)
	}

	ctx, s.cancel = context.WithCancel(ctx)

	// Start stale node cleanup
	go s.staleNodeCleanup(ctx)

	s.logger.Info("Control plane gRPC server starting",
		zap.String("address", s.address),
	)
	return s.grpcServer.Serve(lis)
}

// Stop gracefully stops the gRPC server.
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.grpcServer.GracefulStop()
	s.logger.Info("Control plane gRPC server stopped")
}

// staleNodeCleanup marks nodes as stale if they haven't sent a heartbeat recently.
func (s *Server) staleNodeCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			staleThreshold := 3 * s.heartbeatInterval
			for _, node := range s.nodes {
				if time.Since(node.LastHeartbeat) > staleThreshold {
					node.Status = "stale"
				}
			}
			s.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

// compatibleVersion checks that two version strings share the same major.minor.
func compatibleVersion(cpVersion, dpVersion string) bool {
	return majorMinor(cpVersion) == majorMinor(dpVersion)
}

func majorMinor(v string) string {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}
