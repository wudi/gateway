package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-ldap/ldap/v3"
	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/variables"
)

type ldapCacheEntry struct {
	identity  *variables.Identity
	expiresAt time.Time
}

type pooledConn struct {
	conn      *ldap.Conn
	createdAt time.Time
}

// LDAPStats holds LDAP authentication statistics.
type LDAPStats struct {
	Attempts   uint64 `json:"attempts"`
	Successes  uint64 `json:"successes"`
	Failures   uint64 `json:"failures"`
	CacheHits  uint64 `json:"cache_hits"`
	CacheMisses uint64 `json:"cache_misses"`
	PoolSize   int    `json:"pool_size"`
}

// LDAPAuth provides LDAP/Active Directory authentication.
type LDAPAuth struct {
	url             string
	startTLS        bool
	bindDN          string
	bindPassword    string
	userSearchBase  string
	userSearchFilter string
	searchScope     int
	groupSearchBase  string
	groupSearchFilter string
	groupAttribute  string
	attrClientID    string
	attrEmail       string
	attrDisplayName string
	realm           string

	tlsConfig       *tls.Config
	connTimeout     time.Duration
	requestTimeout  time.Duration
	maxConnLifetime time.Duration

	pool  chan *pooledConn
	cache *lru.Cache[string, *ldapCacheEntry]
	cacheTTL time.Duration

	// Atomic stats
	attempts   atomic.Uint64
	successes  atomic.Uint64
	failures   atomic.Uint64
	cacheHits  atomic.Uint64
	cacheMisses atomic.Uint64
}

// NewLDAPAuth creates a new LDAP authenticator.
func NewLDAPAuth(cfg config.LDAPConfig) (*LDAPAuth, error) {
	// Defaults
	cacheTTL := cfg.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = 5 * time.Minute
	}
	connTimeout := cfg.ConnTimeout
	if connTimeout == 0 {
		connTimeout = 10 * time.Second
	}
	maxConnLifetime := cfg.MaxConnLifetime
	if maxConnLifetime == 0 {
		maxConnLifetime = 5 * time.Minute
	}
	poolSize := cfg.PoolSize
	if poolSize <= 0 {
		poolSize = 5
	}
	clientIDAttr := cfg.AttributeMapping.ClientID
	if clientIDAttr == "" {
		clientIDAttr = "uid"
	}
	groupAttr := cfg.GroupAttribute
	if groupAttr == "" {
		groupAttr = "cn"
	}
	realm := cfg.Realm
	if realm == "" {
		realm = "Restricted"
	}

	scope := ldap.ScopeWholeSubtree
	switch cfg.UserSearchScope {
	case "one":
		scope = ldap.ScopeSingleLevel
	case "base":
		scope = ldap.ScopeBaseObject
	}

	// TLS config
	var tlsCfg *tls.Config
	if cfg.TLS.SkipVerify || cfg.TLS.CAFile != "" || strings.HasPrefix(cfg.URL, "ldaps://") || cfg.StartTLS {
		tlsCfg = &tls.Config{
			InsecureSkipVerify: cfg.TLS.SkipVerify,
		}
		if cfg.TLS.CAFile != "" {
			caCert, err := os.ReadFile(cfg.TLS.CAFile)
			if err != nil {
				return nil, fmt.Errorf("ldap: failed to read CA file: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("ldap: failed to parse CA certificate")
			}
			tlsCfg.RootCAs = pool
		}
	}

	cache, err := lru.New[string, *ldapCacheEntry](10000)
	if err != nil {
		return nil, fmt.Errorf("ldap: failed to create cache: %w", err)
	}

	groupSearchFilter := cfg.GroupSearchFilter
	if groupSearchFilter == "" && cfg.GroupSearchBase != "" {
		groupSearchFilter = "(member={{dn}})"
	}

	return &LDAPAuth{
		url:              cfg.URL,
		startTLS:         cfg.StartTLS,
		bindDN:           cfg.BindDN,
		bindPassword:     cfg.BindPassword,
		userSearchBase:   cfg.UserSearchBase,
		userSearchFilter: cfg.UserSearchFilter,
		searchScope:      scope,
		groupSearchBase:  cfg.GroupSearchBase,
		groupSearchFilter: groupSearchFilter,
		groupAttribute:   groupAttr,
		attrClientID:     clientIDAttr,
		attrEmail:        cfg.AttributeMapping.Email,
		attrDisplayName:  cfg.AttributeMapping.DisplayName,
		realm:            realm,
		tlsConfig:        tlsCfg,
		connTimeout:      connTimeout,
		requestTimeout:   connTimeout,
		maxConnLifetime:  maxConnLifetime,
		pool:             make(chan *pooledConn, poolSize),
		cache:            cache,
		cacheTTL:         cacheTTL,
	}, nil
}

// Authenticate verifies Basic credentials against LDAP.
func (a *LDAPAuth) Authenticate(r *http.Request) (*variables.Identity, error) {
	username, password, ok := r.BasicAuth()
	if !ok {
		return nil, errors.ErrUnauthorized.WithDetails("Basic credentials not provided")
	}

	a.attempts.Add(1)

	// Check cache
	cacheKey := username + ":" + password
	if entry, ok := a.cache.Get(cacheKey); ok {
		if time.Now().Before(entry.expiresAt) {
			a.cacheHits.Add(1)
			return entry.identity, nil
		}
		a.cache.Remove(cacheKey)
	}
	a.cacheMisses.Add(1)

	identity, err := a.ldapAuthenticate(username, password)
	if err != nil {
		a.failures.Add(1)
		return nil, err
	}

	a.successes.Add(1)
	a.cache.Add(cacheKey, &ldapCacheEntry{
		identity:  identity,
		expiresAt: time.Now().Add(a.cacheTTL),
	})

	return identity, nil
}

func (a *LDAPAuth) ldapAuthenticate(username, password string) (*variables.Identity, error) {
	conn, err := a.getConn()
	if err != nil {
		return nil, errors.ErrBadGateway.WithDetails("LDAP connection failed: " + err.Error())
	}

	// 1. Bind as service account
	if err := conn.Bind(a.bindDN, a.bindPassword); err != nil {
		a.closeConn(conn)
		return nil, errors.ErrBadGateway.WithDetails("LDAP service bind failed")
	}

	// 2. Search user
	filter := strings.ReplaceAll(a.userSearchFilter, "{{username}}", ldap.EscapeFilter(username))
	attrs := []string{"dn", a.attrClientID}
	if a.attrEmail != "" {
		attrs = append(attrs, a.attrEmail)
	}
	if a.attrDisplayName != "" {
		attrs = append(attrs, a.attrDisplayName)
	}

	searchReq := ldap.NewSearchRequest(
		a.userSearchBase,
		a.searchScope,
		ldap.NeverDerefAliases,
		1,    // size limit
		int(a.requestTimeout.Seconds()),
		false,
		filter,
		attrs,
		nil,
	)

	sr, err := conn.Search(searchReq)
	if err != nil {
		a.closeConn(conn)
		return nil, errors.ErrBadGateway.WithDetails("LDAP user search failed")
	}
	if len(sr.Entries) == 0 {
		a.putConn(conn)
		return nil, errors.ErrUnauthorized.WithDetails("Invalid credentials")
	}

	entry := sr.Entries[0]
	userDN := entry.DN

	// 3. Bind as user to verify password
	if err := conn.Bind(userDN, password); err != nil {
		a.putConn(conn)
		return nil, errors.ErrUnauthorized.WithDetails("Invalid credentials")
	}

	// Build identity
	clientID := entry.GetAttributeValue(a.attrClientID)
	if clientID == "" {
		clientID = username
	}

	claims := map[string]interface{}{
		"username": username,
		"dn":       userDN,
	}
	if a.attrEmail != "" {
		if email := entry.GetAttributeValue(a.attrEmail); email != "" {
			claims["email"] = email
		}
	}
	if a.attrDisplayName != "" {
		if dn := entry.GetAttributeValue(a.attrDisplayName); dn != "" {
			claims["display_name"] = dn
		}
	}

	// 4. Search groups (optional)
	if a.groupSearchBase != "" {
		// Re-bind as service account to search groups
		if err := conn.Bind(a.bindDN, a.bindPassword); err != nil {
			a.closeConn(conn)
			return &variables.Identity{
				ClientID: clientID,
				AuthType: "ldap",
				Claims:   claims,
			}, nil
		}

		groupFilter := strings.ReplaceAll(a.groupSearchFilter, "{{dn}}", ldap.EscapeFilter(userDN))
		groupFilter = strings.ReplaceAll(groupFilter, "{{username}}", ldap.EscapeFilter(username))

		groupReq := ldap.NewSearchRequest(
			a.groupSearchBase,
			ldap.ScopeWholeSubtree,
			ldap.NeverDerefAliases,
			0, // no size limit for groups
			int(a.requestTimeout.Seconds()),
			false,
			groupFilter,
			[]string{a.groupAttribute},
			nil,
		)

		groupSR, err := conn.Search(groupReq)
		if err == nil && len(groupSR.Entries) > 0 {
			roles := make([]string, 0, len(groupSR.Entries))
			for _, ge := range groupSR.Entries {
				if cn := ge.GetAttributeValue(a.groupAttribute); cn != "" {
					roles = append(roles, cn)
				}
			}
			if len(roles) > 0 {
				claims["roles"] = roles
			}
		}
	}

	a.putConn(conn)

	return &variables.Identity{
		ClientID: clientID,
		AuthType: "ldap",
		Claims:   claims,
	}, nil
}

func (a *LDAPAuth) dial() (*ldap.Conn, error) {
	conn, err := ldap.DialURL(a.url, ldap.DialWithDialer(&net.Dialer{Timeout: a.connTimeout}), ldap.DialWithTLSConfig(a.tlsConfig))
	if err != nil {
		return nil, err
	}
	if a.startTLS {
		tlsCfg := a.tlsConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{}
		}
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, err
		}
	}
	conn.SetTimeout(a.requestTimeout)
	return conn, nil
}

func (a *LDAPAuth) getConn() (*ldap.Conn, error) {
	for {
		select {
		case pc := <-a.pool:
			if time.Since(pc.createdAt) > a.maxConnLifetime || pc.conn.IsClosing() {
				pc.conn.Close()
				continue
			}
			pc.conn.SetTimeout(a.requestTimeout)
			return pc.conn, nil
		default:
			return a.dial()
		}
	}
}

func (a *LDAPAuth) putConn(conn *ldap.Conn) {
	if conn.IsClosing() {
		return
	}
	select {
	case a.pool <- &pooledConn{conn: conn, createdAt: time.Now()}:
	default:
		conn.Close()
	}
}

func (a *LDAPAuth) closeConn(conn *ldap.Conn) {
	conn.Close()
}

// IsEnabled returns true if LDAP is configured.
func (a *LDAPAuth) IsEnabled() bool {
	return a.url != ""
}

// Realm returns the configured realm.
func (a *LDAPAuth) Realm() string {
	return a.realm
}

// Stats returns authentication statistics.
func (a *LDAPAuth) Stats() LDAPStats {
	return LDAPStats{
		Attempts:    a.attempts.Load(),
		Successes:   a.successes.Load(),
		Failures:    a.failures.Load(),
		CacheHits:   a.cacheHits.Load(),
		CacheMisses: a.cacheMisses.Load(),
		PoolSize:    len(a.pool),
	}
}

// Close drains the connection pool.
func (a *LDAPAuth) Close() {
	for {
		select {
		case pc := <-a.pool:
			pc.conn.Close()
		default:
			return
		}
	}
}
