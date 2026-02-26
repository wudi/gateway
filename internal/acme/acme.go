package acme

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/wudi/runway/config"
)

// CertInfo holds metadata about the current TLS certificate.
type CertInfo struct {
	Domains  []string  `json:"domains"`
	Issuer   string    `json:"issuer"`
	NotBefore time.Time `json:"not_before"`
	NotAfter time.Time `json:"not_after"`
	DaysLeft int       `json:"days_left"`
	Serial   string    `json:"serial"`
}

// Manager wraps autocert.Manager with certificate expiry monitoring.
type Manager struct {
	autocertMgr   *autocert.Manager
	challengeType string
	httpAddress   string
	domains       []string
	httpServer    *http.Server

	mu       sync.RWMutex
	certInfo *CertInfo
}

// New creates a new ACME Manager from config.
func New(cfg config.ACMEConfig) (*Manager, error) {
	if len(cfg.Domains) == 0 {
		return nil, fmt.Errorf("acme: at least one domain is required")
	}

	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = "/var/lib/runway/acme"
	}

	directoryURL := cfg.DirectoryURL
	if directoryURL == "" {
		directoryURL = autocert.DefaultACMEDirectory
	}

	challengeType := cfg.ChallengeType
	if challengeType == "" {
		challengeType = "tls-alpn-01"
	}

	httpAddress := cfg.HTTPAddress
	if httpAddress == "" {
		httpAddress = ":80"
	}

	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(cacheDir),
		HostPolicy: autocert.HostWhitelist(cfg.Domains...),
		Email:      cfg.Email,
	}

	// Set custom ACME directory if not the default
	if directoryURL != autocert.DefaultACMEDirectory {
		m.Client = &acme.Client{DirectoryURL: directoryURL}
	}

	return &Manager{
		autocertMgr:   m,
		challengeType: challengeType,
		httpAddress:   httpAddress,
		domains:       cfg.Domains,
		certInfo:      &CertInfo{Domains: cfg.Domains, DaysLeft: -1},
	}, nil
}

// TLSConfig returns a *tls.Config configured for ACME certificate provisioning.
// The GetCertificate callback wraps autocert's to track certificate metadata.
func (m *Manager) TLSConfig() *tls.Config {
	tlsCfg := m.autocertMgr.TLSConfig()
	tlsCfg.MinVersion = tls.VersionTLS12

	// Wrap GetCertificate to track cert info on each handshake
	origGetCert := tlsCfg.GetCertificate
	tlsCfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, err := origGetCert(hello)
		if err != nil {
			return nil, err
		}
		if cert != nil && cert.Leaf != nil {
			m.updateCertInfo(cert.Leaf)
		} else if cert != nil && len(cert.Certificate) > 0 {
			// Parse leaf if not already parsed
			leaf, parseErr := x509.ParseCertificate(cert.Certificate[0])
			if parseErr == nil {
				m.updateCertInfo(leaf)
			}
		}
		return cert, nil
	}

	return tlsCfg
}

// StartHTTPChallenge starts an HTTP server for HTTP-01 challenges.
// This is a no-op if the challenge type is tls-alpn-01.
func (m *Manager) StartHTTPChallenge(ctx context.Context) error {
	if m.challengeType != "http-01" {
		return nil
	}

	m.httpServer = &http.Server{
		Addr:    m.httpAddress,
		Handler: m.autocertMgr.HTTPHandler(nil),
	}

	go func() {
		if err := m.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Log error but don't block — the main server handles TLS
		}
	}()

	return nil
}

// Stop shuts down the HTTP-01 challenge server if running.
func (m *Manager) Stop(ctx context.Context) error {
	if m.httpServer != nil {
		return m.httpServer.Shutdown(ctx)
	}
	return nil
}

// CertStatus returns the current certificate metadata (thread-safe).
func (m *Manager) CertStatus() *CertInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info := *m.certInfo // shallow copy
	return &info
}

// Domains returns the configured domain list.
func (m *Manager) Domains() []string {
	return m.domains
}

// updateCertInfo extracts metadata from a leaf certificate and caches it.
func (m *Manager) updateCertInfo(leaf *x509.Certificate) {
	info := &CertInfo{
		Domains:   leaf.DNSNames,
		Issuer:    leaf.Issuer.CommonName,
		NotBefore: leaf.NotBefore,
		NotAfter:  leaf.NotAfter,
		DaysLeft:  int(time.Until(leaf.NotAfter).Hours() / 24),
		Serial:    formatSerial(leaf.SerialNumber),
	}

	m.mu.Lock()
	m.certInfo = info
	m.mu.Unlock()
}

// formatSerial formats a certificate serial number as a hex string.
func formatSerial(serial *big.Int) string {
	if serial == nil {
		return ""
	}
	return fmt.Sprintf("%X", serial)
}
