package auth

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/golang-jwt/jwt/v5"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/variables"
)

// SAMLStats holds SAML authentication statistics.
type SAMLStats struct {
	SSOAttempts      uint64 `json:"sso_attempts"`
	SSOSuccesses     uint64 `json:"sso_successes"`
	SSOFailures      uint64 `json:"sso_failures"`
	TokenValidations uint64 `json:"token_validations"`
	TokenSuccesses   uint64 `json:"token_successes"`
	TokenFailures    uint64 `json:"token_failures"`
	SessionAuths     uint64 `json:"session_auths"`
	LogoutRequests   uint64 `json:"logout_requests"`
}

// replayEntry tracks a consumed assertion ID with its expiry.
type replayEntry struct {
	expiresAt time.Time
}

// SAMLAuth provides SAML 2.0 SSO authentication.
type SAMLAuth struct {
	sp *saml.ServiceProvider

	pathPrefix      string
	assertionHeader string

	// Session cookie config
	sessionSignKey []byte
	cookieName     string
	cookieMaxAge   time.Duration
	cookieDomain   string
	cookieSecure   bool
	cookieSameSite http.SameSite

	// Attribute mapping
	attrClientID    string
	attrEmail       string
	attrDisplayName string
	attrRoles       string

	allowIDPInit bool

	// IdP metadata refresh
	metadataURL             string
	metadataRefreshInterval time.Duration
	metadataStopCh          chan struct{}

	// Assertion replay cache
	replayMu       sync.Mutex
	seenAssertions map[string]replayEntry
	replayMaxSize  int

	// Atomic stats
	ssoAttempts      atomic.Uint64
	ssoSuccesses     atomic.Uint64
	ssoFailures      atomic.Uint64
	tokenValidations atomic.Uint64
	tokenSuccesses   atomic.Uint64
	tokenFailures    atomic.Uint64
	sessionAuths     atomic.Uint64
	logoutRequests   atomic.Uint64
}

// NewSAMLAuth creates a new SAML authenticator.
func NewSAMLAuth(cfg config.SAMLConfig) (*SAMLAuth, error) {
	// Load SP keypair
	keyPair, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("saml: failed to load keypair: %w", err)
	}
	spCert := keyPair.Leaf
	if spCert == nil {
		// tls.LoadX509KeyPair doesn't always set Leaf; parse the raw cert
		var parseErr error
		spCert, parseErr = x509.ParseCertificate(keyPair.Certificate[0])
		if parseErr != nil {
			return nil, fmt.Errorf("saml: failed to parse certificate: %w", parseErr)
		}
	}

	// Load IdP metadata
	var idpMetadata *saml.EntityDescriptor
	if cfg.IDPMetadataURL != "" {
		metaURL, err := url.Parse(cfg.IDPMetadataURL)
		if err != nil {
			return nil, fmt.Errorf("saml: invalid idp_metadata_url: %w", err)
		}
		idpMetadata, err = samlsp.FetchMetadata(context.Background(), http.DefaultClient, *metaURL)
		if err != nil {
			return nil, fmt.Errorf("saml: failed to fetch IdP metadata: %w", err)
		}
	} else {
		data, err := os.ReadFile(cfg.IDPMetadataFile)
		if err != nil {
			return nil, fmt.Errorf("saml: failed to read idp_metadata_file: %w", err)
		}
		idpMetadata, err = samlsp.ParseMetadata(data)
		if err != nil {
			return nil, fmt.Errorf("saml: failed to parse IdP metadata: %w", err)
		}
	}

	// Apply defaults
	pathPrefix := cfg.PathPrefix
	if pathPrefix == "" {
		pathPrefix = "/saml/"
	}
	cookieName := cfg.Session.CookieName
	if cookieName == "" {
		cookieName = "gateway_saml"
	}
	cookieMaxAge := cfg.Session.MaxAge
	if cookieMaxAge == 0 {
		cookieMaxAge = 8 * time.Hour
	}
	assertionHeader := cfg.AssertionHeader
	if assertionHeader == "" {
		assertionHeader = "X-SAML-Assertion"
	}
	attrClientID := cfg.AttributeMapping.ClientID
	if attrClientID == "" {
		attrClientID = "uid"
	}

	cookieSecure := cfg.Session.Secure
	// Default secure=true when the zero value struct hasn't been modified.
	// Since we can't distinguish "not set" from "false" for bool, we default to true
	// unless the user explicitly sets it. The config validation ensures signing_key is set,
	// so this is a reasonable default for production.
	if !cfg.Session.Secure && cfg.Session.SameSite == "" && cfg.Session.Domain == "" {
		cookieSecure = true
	}

	sameSite := http.SameSiteLaxMode
	switch strings.ToLower(cfg.Session.SameSite) {
	case "strict":
		sameSite = http.SameSiteStrictMode
	case "none":
		sameSite = http.SameSiteNoneMode
	case "lax", "":
		sameSite = http.SameSiteLaxMode
	}

	signRequests := true
	if cfg.SignRequests != nil {
		signRequests = *cfg.SignRequests
	}

	// Map NameIDFormat
	nameIDFormat := mapNameIDFormat(cfg.NameIDFormat)

	// Build entity URLs
	entityID := cfg.EntityID
	metadataURL, _ := url.Parse(entityID + pathPrefix + "metadata")
	acsURL, _ := url.Parse(entityID + pathPrefix + "acs")
	sloURL, _ := url.Parse(entityID + pathPrefix + "slo")

	signer, ok := keyPair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("saml: private key does not implement crypto.Signer")
	}

	sp := &saml.ServiceProvider{
		EntityID:          entityID,
		Key:               signer,
		Certificate:       spCert,
		MetadataURL:       *metadataURL,
		AcsURL:            *acsURL,
		SloURL:            *sloURL,
		IDPMetadata:       idpMetadata,
		AuthnNameIDFormat: nameIDFormat,
		AllowIDPInitiated: cfg.AllowIDPInitiated,
	}

	if cfg.ForceAuthn {
		fa := true
		sp.ForceAuthn = &fa
	}

	if signRequests {
		sp.SignatureMethod = dsig.RSASHA256SignatureMethod
	}

	// Metadata refresh interval
	refreshInterval := cfg.MetadataRefreshInterval
	if refreshInterval == 0 && cfg.IDPMetadataURL != "" {
		refreshInterval = 24 * time.Hour
	}

	a := &SAMLAuth{
		sp:                      sp,
		pathPrefix:              pathPrefix,
		assertionHeader:         assertionHeader,
		sessionSignKey:          []byte(cfg.Session.SigningKey),
		cookieName:              cookieName,
		cookieMaxAge:            cookieMaxAge,
		cookieDomain:            cfg.Session.Domain,
		cookieSecure:            cookieSecure,
		cookieSameSite:          sameSite,
		attrClientID:            attrClientID,
		attrEmail:               cfg.AttributeMapping.Email,
		attrDisplayName:         cfg.AttributeMapping.DisplayName,
		attrRoles:               cfg.AttributeMapping.Roles,
		allowIDPInit:            cfg.AllowIDPInitiated,
		metadataURL:             cfg.IDPMetadataURL,
		metadataRefreshInterval: refreshInterval,
		metadataStopCh:          make(chan struct{}),
		seenAssertions:          make(map[string]replayEntry),
		replayMaxSize:           10000,
	}

	// Start metadata refresh goroutine
	if cfg.IDPMetadataURL != "" && refreshInterval > 0 {
		go a.refreshMetadataLoop()
	}

	return a, nil
}

// mapNameIDFormat maps short config names to SAML URNs.
func mapNameIDFormat(format string) saml.NameIDFormat {
	switch format {
	case "email":
		return saml.EmailAddressNameIDFormat
	case "persistent":
		return saml.PersistentNameIDFormat
	case "transient":
		return saml.TransientNameIDFormat
	case "unspecified":
		return saml.UnspecifiedNameIDFormat
	default:
		return saml.UnspecifiedNameIDFormat
	}
}

// Authenticate validates a SAML assertion from either a header or session cookie.
func (a *SAMLAuth) Authenticate(r *http.Request) (*variables.Identity, error) {
	// Mode 1: Check assertion header (stateless token validation)
	if headerVal := r.Header.Get(a.assertionHeader); headerVal != "" {
		return a.authenticateFromHeader(headerVal)
	}

	// Mode 2: Check session cookie
	if cookie, err := r.Cookie(a.cookieName); err == nil && cookie.Value != "" {
		return a.authenticateFromSession(cookie.Value)
	}

	return nil, errors.ErrUnauthorized.WithDetails("SAML credentials not provided")
}

// authenticateFromHeader validates a Base64-encoded SAML assertion from an HTTP header.
func (a *SAMLAuth) authenticateFromHeader(headerVal string) (*variables.Identity, error) {
	a.tokenValidations.Add(1)

	xmlData, err := base64.StdEncoding.DecodeString(headerVal)
	if err != nil {
		a.tokenFailures.Add(1)
		return nil, errors.ErrUnauthorized.WithDetails("invalid SAML assertion encoding")
	}

	var assertion saml.Assertion
	if err := xml.Unmarshal(xmlData, &assertion); err != nil {
		a.tokenFailures.Add(1)
		return nil, errors.ErrUnauthorized.WithDetails("invalid SAML assertion XML")
	}

	// Check time conditions
	now := time.Now()
	if assertion.Conditions != nil {
		if !assertion.Conditions.NotBefore.IsZero() && now.Add(saml.MaxClockSkew).Before(assertion.Conditions.NotBefore) {
			a.tokenFailures.Add(1)
			return nil, errors.ErrUnauthorized.WithDetails("SAML assertion not yet valid")
		}
		if !assertion.Conditions.NotOnOrAfter.IsZero() && now.Add(-saml.MaxClockSkew).After(assertion.Conditions.NotOnOrAfter) {
			a.tokenFailures.Add(1)
			return nil, errors.ErrUnauthorized.WithDetails("SAML assertion expired")
		}
	}

	// Replay protection
	if assertion.ID != "" {
		if !a.checkAndRecordAssertion(assertion.ID, a.cookieMaxAge) {
			a.tokenFailures.Add(1)
			return nil, errors.ErrUnauthorized.WithDetails("SAML assertion already consumed")
		}
	}

	nameID := ""
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		nameID = assertion.Subject.NameID.Value
	}

	identity := a.buildIdentity(nameID, &assertion)
	a.tokenSuccesses.Add(1)
	return identity, nil
}

// authenticateFromSession validates a signed session JWT cookie.
func (a *SAMLAuth) authenticateFromSession(cookieValue string) (*variables.Identity, error) {
	a.sessionAuths.Add(1)

	token, err := jwt.Parse(cookieValue, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.sessionSignKey, nil
	})
	if err != nil {
		return nil, errors.ErrUnauthorized.WithDetails("invalid SAML session")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.ErrUnauthorized.WithDetails("invalid SAML session claims")
	}

	clientID, _ := claims["sub"].(string)
	authType, _ := claims["auth_type"].(string)
	if authType == "" {
		authType = "saml"
	}

	identityClaims := make(map[string]interface{})
	if c, ok := claims["claims"].(map[string]interface{}); ok {
		identityClaims = c
	}

	return &variables.Identity{
		ClientID: clientID,
		AuthType: authType,
		Claims:   identityClaims,
	}, nil
}

// ServeHTTP dispatches SAML protocol endpoints.
func (a *SAMLAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, a.pathPrefix)

	switch {
	case suffix == "metadata" && r.Method == http.MethodGet:
		a.HandleMetadata(w, r)
	case suffix == "acs" && r.Method == http.MethodPost:
		a.HandleACS(w, r)
	case suffix == "login" && r.Method == http.MethodGet:
		a.HandleLogin(w, r)
	case suffix == "slo":
		a.HandleSLO(w, r)
	default:
		http.NotFound(w, r)
	}
}

// HandleMetadata serves the SP metadata XML.
func (a *SAMLAuth) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	md := a.sp.Metadata()
	data, err := xml.MarshalIndent(md, "", "  ")
	if err != nil {
		http.Error(w, "failed to marshal metadata", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	w.Write(data)
}

// HandleLogin initiates SP-initiated SSO by redirecting to the IdP.
func (a *SAMLAuth) HandleLogin(w http.ResponseWriter, r *http.Request) {
	a.ssoAttempts.Add(1)

	returnTo := r.URL.Query().Get("return_to")
	// Validate return_to is a relative path only (CSRF protection)
	if returnTo != "" {
		if !isRelativePath(returnTo) {
			a.ssoFailures.Add(1)
			http.Error(w, "return_to must be a relative path", http.StatusBadRequest)
			return
		}
	} else {
		returnTo = "/"
	}

	// HMAC-sign the relay state to prevent tampering
	relayState := a.signRelayState(returnTo)

	redirectURL, err := a.sp.MakeRedirectAuthenticationRequest(relayState)
	if err != nil {
		a.ssoFailures.Add(1)
		http.Error(w, "failed to create auth request", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// HandleACS processes the SAML response from the IdP (Assertion Consumer Service).
func (a *SAMLAuth) HandleACS(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.ssoFailures.Add(1)
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	// For IdP-initiated SSO, possibleRequestIDs is nil
	var possibleRequestIDs []string
	if !a.allowIDPInit {
		possibleRequestIDs = []string{} // empty slice = strict validation
	}

	assertion, err := a.sp.ParseResponse(r, possibleRequestIDs)
	if err != nil {
		a.ssoFailures.Add(1)
		if ire, ok := err.(*saml.InvalidResponseError); ok {
			log.Printf("saml: ACS parse error: %v (detail: %v)", err, ire.PrivateErr)
		} else {
			log.Printf("saml: ACS parse error: %v", err)
		}
		http.Error(w, "SAML authentication failed", http.StatusForbidden)
		return
	}

	// Replay protection on assertion ID
	if assertion.ID != "" {
		if !a.checkAndRecordAssertion(assertion.ID, a.cookieMaxAge) {
			a.ssoFailures.Add(1)
			http.Error(w, "SAML assertion already consumed", http.StatusForbidden)
			return
		}
	}

	nameID := ""
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		nameID = assertion.Subject.NameID.Value
	}

	identity := a.buildIdentity(nameID, assertion)

	// Mint session JWT
	sessionToken, err := a.mintSessionToken(identity)
	if err != nil {
		a.ssoFailures.Add(1)
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	// Set cookie
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    sessionToken,
		Path:     "/",
		Domain:   a.cookieDomain,
		MaxAge:   int(a.cookieMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: a.cookieSameSite,
	})

	// Validate relay state
	redirectTo := "/"
	relayState := r.FormValue("RelayState")
	if relayState != "" {
		if dest, ok := a.verifyRelayState(relayState); ok {
			redirectTo = dest
		}
	}

	a.ssoSuccesses.Add(1)
	http.Redirect(w, r, redirectTo, http.StatusFound)
}

// HandleSLO handles Single Logout (SP-initiated and IdP-initiated).
func (a *SAMLAuth) HandleSLO(w http.ResponseWriter, r *http.Request) {
	a.logoutRequests.Add(1)

	// Check if this is an IdP-initiated logout (has SAMLRequest parameter)
	if r.URL.Query().Get("SAMLRequest") != "" || (r.Method == http.MethodPost && r.FormValue("SAMLRequest") != "") {
		a.handleIDPInitiatedLogout(w, r)
		return
	}

	// SP-initiated logout: clear cookie and redirect to IdP
	a.clearSessionCookie(w)

	// Get nameID from session cookie (before clearing)
	nameID := ""
	if cookie, err := r.Cookie(a.cookieName); err == nil {
		if identity, err := a.authenticateFromSession(cookie.Value); err == nil {
			nameID = identity.ClientID
		}
	}

	if nameID != "" {
		redirectURL, err := a.sp.MakeRedirectLogoutRequest(nameID, "")
		if err == nil {
			http.Redirect(w, r, redirectURL.String(), http.StatusFound)
			return
		}
	}

	// Fallback: just clear session and redirect to root
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleIDPInitiatedLogout processes a logout request from the IdP.
func (a *SAMLAuth) handleIDPInitiatedLogout(w http.ResponseWriter, r *http.Request) {
	a.clearSessionCookie(w)

	// Parse the LogoutRequest to get the request ID for the response
	var logoutReqID string
	if samlReq := r.URL.Query().Get("SAMLRequest"); samlReq != "" {
		if reqData, err := base64.StdEncoding.DecodeString(samlReq); err == nil {
			var lr saml.LogoutRequest
			if err := xml.Unmarshal(reqData, &lr); err == nil {
				logoutReqID = lr.ID
			}
		}
	}

	relayState := r.URL.Query().Get("RelayState")

	// Send LogoutResponse back to IdP
	if logoutReqID != "" {
		redirectURL, err := a.sp.MakeRedirectLogoutResponse(logoutReqID, relayState)
		if err == nil {
			http.Redirect(w, r, redirectURL.String(), http.StatusFound)
			return
		}
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

// MatchesPath returns true if the request path is under the SAML path prefix.
func (a *SAMLAuth) MatchesPath(path string) bool {
	return strings.HasPrefix(path, a.pathPrefix)
}

// IsEnabled returns true if the SAML provider is configured.
func (a *SAMLAuth) IsEnabled() bool {
	return a.sp != nil
}

// PathPrefix returns the configured SAML path prefix.
func (a *SAMLAuth) PathPrefix() string {
	return a.pathPrefix
}

// Stats returns SAML authentication statistics.
func (a *SAMLAuth) Stats() SAMLStats {
	return SAMLStats{
		SSOAttempts:      a.ssoAttempts.Load(),
		SSOSuccesses:     a.ssoSuccesses.Load(),
		SSOFailures:      a.ssoFailures.Load(),
		TokenValidations: a.tokenValidations.Load(),
		TokenSuccesses:   a.tokenSuccesses.Load(),
		TokenFailures:    a.tokenFailures.Load(),
		SessionAuths:     a.sessionAuths.Load(),
		LogoutRequests:   a.logoutRequests.Load(),
	}
}

// Close stops the metadata refresh goroutine.
func (a *SAMLAuth) Close() {
	close(a.metadataStopCh)
}

// buildIdentity creates an Identity from a SAML assertion.
func (a *SAMLAuth) buildIdentity(nameID string, assertion *saml.Assertion) *variables.Identity {
	attrs := extractAttributes(assertion)

	clientID := nameID
	if a.attrClientID != "" {
		if v, ok := attrs[a.attrClientID]; ok && len(v) > 0 {
			clientID = v[0]
		}
	}

	claims := make(map[string]interface{})
	claims["name_id"] = nameID

	if a.attrEmail != "" {
		if v, ok := attrs[a.attrEmail]; ok && len(v) > 0 {
			claims["email"] = v[0]
		}
	}
	if a.attrDisplayName != "" {
		if v, ok := attrs[a.attrDisplayName]; ok && len(v) > 0 {
			claims["display_name"] = v[0]
		}
	}
	if a.attrRoles != "" {
		if v, ok := attrs[a.attrRoles]; ok && len(v) > 0 {
			claims["roles"] = v
		}
	}

	return &variables.Identity{
		ClientID: clientID,
		AuthType: "saml",
		Claims:   claims,
	}
}

// extractAttributes flattens assertion attribute statements into a map of multi-valued attributes.
func extractAttributes(assertion *saml.Assertion) map[string][]string {
	attrs := make(map[string][]string)
	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			key := attr.Name
			if key == "" {
				key = attr.FriendlyName
			}
			var values []string
			for _, v := range attr.Values {
				values = append(values, v.Value)
			}
			attrs[key] = values
		}
	}
	return attrs
}

// mintSessionToken creates a signed JWT for the session cookie.
func (a *SAMLAuth) mintSessionToken(identity *variables.Identity) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":       identity.ClientID,
		"auth_type": "saml",
		"iat":       now.Unix(),
		"exp":       now.Add(a.cookieMaxAge).Unix(),
		"claims":    identity.Claims,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.sessionSignKey)
}

// signRelayState creates an HMAC-signed relay state value.
func (a *SAMLAuth) signRelayState(returnTo string) string {
	mac := hmac.New(sha256.New, a.sessionSignKey)
	mac.Write([]byte(returnTo))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.URLEncoding.EncodeToString([]byte(returnTo + "|" + sig))
}

// verifyRelayState validates and extracts the return URL from a signed relay state.
func (a *SAMLAuth) verifyRelayState(relayState string) (string, bool) {
	data, err := base64.URLEncoding.DecodeString(relayState)
	if err != nil {
		return "", false
	}

	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return "", false
	}

	returnTo := parts[0]
	sig := parts[1]

	mac := hmac.New(sha256.New, a.sessionSignKey)
	mac.Write([]byte(returnTo))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return "", false
	}

	if !isRelativePath(returnTo) {
		return "", false
	}

	return returnTo, true
}

// isRelativePath checks that a URL is a relative path (no scheme, no host).
func isRelativePath(s string) bool {
	if s == "" {
		return false
	}
	if !strings.HasPrefix(s, "/") {
		return false
	}
	// Reject protocol-relative URLs like //evil.com
	if strings.HasPrefix(s, "//") {
		return false
	}
	parsed, err := url.Parse(s)
	if err != nil {
		return false
	}
	return parsed.Scheme == "" && parsed.Host == ""
}

// clearSessionCookie clears the SAML session cookie.
func (a *SAMLAuth) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    "",
		Path:     "/",
		Domain:   a.cookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: a.cookieSameSite,
	})
}

// checkAndRecordAssertion checks if an assertion ID has been seen before.
// Returns true if the assertion is new (not a replay), false if already consumed.
func (a *SAMLAuth) checkAndRecordAssertion(assertionID string, ttl time.Duration) bool {
	a.replayMu.Lock()
	defer a.replayMu.Unlock()

	now := time.Now()

	// Check if already seen
	if entry, exists := a.seenAssertions[assertionID]; exists {
		if now.Before(entry.expiresAt) {
			return false // replay
		}
		// Expired entry, allow reuse
	}

	// Evict expired entries periodically (when cache is large)
	if len(a.seenAssertions) >= a.replayMaxSize {
		for id, entry := range a.seenAssertions {
			if now.After(entry.expiresAt) {
				delete(a.seenAssertions, id)
			}
		}
	}

	a.seenAssertions[assertionID] = replayEntry{
		expiresAt: now.Add(ttl),
	}
	return true
}

// refreshMetadataLoop periodically re-fetches IdP metadata.
func (a *SAMLAuth) refreshMetadataLoop() {
	ticker := time.NewTicker(a.metadataRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.refreshMetadata()
		case <-a.metadataStopCh:
			return
		}
	}
}

// refreshMetadata fetches fresh IdP metadata and updates the SP.
func (a *SAMLAuth) refreshMetadata() {
	metaURL, err := url.Parse(a.metadataURL)
	if err != nil {
		log.Printf("saml: failed to parse metadata URL for refresh: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	metadata, err := samlsp.FetchMetadata(ctx, http.DefaultClient, *metaURL)
	if err != nil {
		log.Printf("saml: failed to refresh IdP metadata: %v", err)
		return
	}

	a.sp.IDPMetadata = metadata
}
