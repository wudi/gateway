package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/wudi/runway/config"
)

// BuildCPTLSConfig builds a TLS config for the control plane gRPC server.
// It requires mTLS: the server presents its own cert and verifies DP client certs
// against the client CA pool.
func BuildCPTLSConfig(cfg config.ControlPlaneConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load CP cert/key: %w", err)
	}

	caCert, err := os.ReadFile(cfg.TLS.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read client CA file: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse client CA certificate")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// BuildDPTLSConfig builds a TLS config for the data plane gRPC client.
// The DP presents its own cert and verifies the CP's certificate against the CA pool.
func BuildDPTLSConfig(cfg config.DataPlaneConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load DP cert/key: %w", err)
	}

	caCert, err := os.ReadFile(cfg.TLS.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}
