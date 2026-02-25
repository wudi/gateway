package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crewjam/saml"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/variables"
)

// testSAMLFixture holds temp files for test SAML setup.
type testSAMLFixture struct {
	dir             string
	certFile        string
	keyFile         string
	idpMetadataFile string
	idpCert         *x509.Certificate
	idpKey          *rsa.PrivateKey
}

func newTestSAMLFixture(t *testing.T) *testSAMLFixture {
	t.Helper()
	dir := t.TempDir()

	// Generate SP keypair
	spKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate SP key: %v", err)
	}
	spTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-sp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	spCertDER, err := x509.CreateCertificate(rand.Reader, spTemplate, spTemplate, &spKey.PublicKey, spKey)
	if err != nil {
		t.Fatalf("failed to create SP cert: %v", err)
	}

	certFile := filepath.Join(dir, "sp.cert")
	keyFile := filepath.Join(dir, "sp.key")

	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: spCertDER}), 0o600); err != nil {
		t.Fatalf("failed to write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(spKey)}), 0o600); err != nil {
		t.Fatalf("failed to write key: %v", err)
	}

	// Generate IdP keypair and metadata
	idpKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate IdP key: %v", err)
	}
	idpTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-idp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	idpCertDER, err := x509.CreateCertificate(rand.Reader, idpTemplate, idpTemplate, &idpKey.PublicKey, idpKey)
	if err != nil {
		t.Fatalf("failed to create IdP cert: %v", err)
	}
	idpCert, err := x509.ParseCertificate(idpCertDER)
	if err != nil {
		t.Fatalf("failed to parse IdP cert: %v", err)
	}

	idpCertB64 := base64.StdEncoding.EncodeToString(idpCertDER)

	// Create minimal IdP metadata
	idpMeta := fmt.Sprintf(`<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example.com">
  <IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <KeyDescriptor use="signing">
      <KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#">
        <X509Data>
          <X509Certificate>%s</X509Certificate>
        </X509Data>
      </KeyInfo>
    </KeyDescriptor>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example.com/sso"/>
    <SingleLogoutService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example.com/slo"/>
  </IDPSSODescriptor>
</EntityDescriptor>`, idpCertB64)

	idpMetadataFile := filepath.Join(dir, "idp-metadata.xml")
	if err := os.WriteFile(idpMetadataFile, []byte(idpMeta), 0o600); err != nil {
		t.Fatalf("failed to write IdP metadata: %v", err)
	}

	return &testSAMLFixture{
		dir:             dir,
		certFile:        certFile,
		keyFile:         keyFile,
		idpMetadataFile: idpMetadataFile,
		idpCert:         idpCert,
		idpKey:          idpKey,
	}
}

func newTestSAMLConfig(fix *testSAMLFixture) config.SAMLConfig {
	signReq := true
	return config.SAMLConfig{
		Enabled:         true,
		EntityID:        "https://sp.example.com",
		CertFile:        fix.certFile,
		KeyFile:         fix.keyFile,
		IDPMetadataFile: fix.idpMetadataFile,
		SignRequests:    &signReq,
		Session: config.SAMLSessionConfig{
			SigningKey: "this-is-a-test-signing-key-32bytes!",
			Secure:    false,
		},
	}
}

func newTestSAMLAuth(t *testing.T) (*SAMLAuth, *testSAMLFixture) {
	t.Helper()
	fix := newTestSAMLFixture(t)
	cfg := newTestSAMLConfig(fix)
	a, err := NewSAMLAuth(cfg)
	if err != nil {
		t.Fatalf("NewSAMLAuth failed: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a, fix
}

func TestSAMLAuth_Defaults(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	if a.pathPrefix != "/saml/" {
		t.Errorf("expected pathPrefix /saml/, got %s", a.pathPrefix)
	}
	if a.cookieName != "gateway_saml" {
		t.Errorf("expected cookieName gateway_saml, got %s", a.cookieName)
	}
	if a.cookieMaxAge != 8*time.Hour {
		t.Errorf("expected cookieMaxAge 8h, got %v", a.cookieMaxAge)
	}
	if a.assertionHeader != "X-SAML-Assertion" {
		t.Errorf("expected assertionHeader X-SAML-Assertion, got %s", a.assertionHeader)
	}
	if a.attrClientID != "uid" {
		t.Errorf("expected attrClientID uid, got %s", a.attrClientID)
	}
}

func TestSAMLAuth_ConstructorValidation(t *testing.T) {
	fix := newTestSAMLFixture(t)

	// Missing cert file
	cfg := newTestSAMLConfig(fix)
	cfg.CertFile = "/nonexistent/cert.pem"
	_, err := NewSAMLAuth(cfg)
	if err == nil {
		t.Error("expected error for missing cert file")
	}

	// Missing key file
	cfg = newTestSAMLConfig(fix)
	cfg.KeyFile = "/nonexistent/key.pem"
	_, err = NewSAMLAuth(cfg)
	if err == nil {
		t.Error("expected error for missing key file")
	}

	// Missing IdP metadata
	cfg = newTestSAMLConfig(fix)
	cfg.IDPMetadataFile = "/nonexistent/metadata.xml"
	_, err = NewSAMLAuth(cfg)
	if err == nil {
		t.Error("expected error for missing IdP metadata file")
	}
}

func TestSAMLAuth_AuthenticateNoCredentials(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	r := httptest.NewRequest("GET", "/api/test", nil)
	_, err := a.Authenticate(r)
	if err == nil {
		t.Error("expected error for no credentials")
	}
}

func TestSAMLAuth_AuthenticateFromSession(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// Mint a valid session token
	identity := &variables.Identity{
		ClientID: "testuser",
		AuthType: "saml",
		Claims:   map[string]interface{}{"email": "test@example.com"},
	}
	token, err := a.mintSessionToken(identity)
	if err != nil {
		t.Fatalf("failed to mint token: %v", err)
	}

	r := httptest.NewRequest("GET", "/api/test", nil)
	r.AddCookie(&http.Cookie{Name: a.cookieName, Value: token})

	id, err := a.Authenticate(r)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if id.ClientID != "testuser" {
		t.Errorf("expected clientID testuser, got %s", id.ClientID)
	}
	if id.AuthType != "saml" {
		t.Errorf("expected authType saml, got %s", id.AuthType)
	}
}

func TestSAMLAuth_AuthenticateExpiredSession(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// Mint with very short max age then wait
	origMaxAge := a.cookieMaxAge
	a.cookieMaxAge = 1 * time.Millisecond
	identity := &variables.Identity{
		ClientID: "testuser",
		AuthType: "saml",
		Claims:   map[string]interface{}{},
	}
	token, err := a.mintSessionToken(identity)
	if err != nil {
		t.Fatalf("failed to mint token: %v", err)
	}
	a.cookieMaxAge = origMaxAge

	time.Sleep(10 * time.Millisecond)

	r := httptest.NewRequest("GET", "/api/test", nil)
	r.AddCookie(&http.Cookie{Name: a.cookieName, Value: token})

	_, err = a.Authenticate(r)
	if err == nil {
		t.Error("expected error for expired session")
	}
}

func TestSAMLAuth_AuthenticateFromHeader(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// Build a minimal SAML assertion XML
	now := time.Now()
	assertion := saml.Assertion{
		ID:           "test-assertion-id-1",
		IssueInstant: now,
		Version:      "2.0",
		Subject: &saml.Subject{
			NameID: &saml.NameID{
				Value: "user@example.com",
			},
		},
		Conditions: &saml.Conditions{
			NotBefore:    now.Add(-time.Minute),
			NotOnOrAfter: now.Add(5 * time.Minute),
		},
		AttributeStatements: []saml.AttributeStatement{
			{
				Attributes: []saml.Attribute{
					{Name: "uid", Values: []saml.AttributeValue{{Value: "testuid"}}},
					{Name: "email", Values: []saml.AttributeValue{{Value: "user@example.com"}}},
				},
			},
		},
	}

	xmlData, err := xml.Marshal(assertion)
	if err != nil {
		t.Fatalf("failed to marshal assertion: %v", err)
	}

	r := httptest.NewRequest("GET", "/api/test", nil)
	r.Header.Set("X-SAML-Assertion", base64.StdEncoding.EncodeToString(xmlData))

	id, err := a.Authenticate(r)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if id.ClientID != "testuid" {
		t.Errorf("expected clientID testuid, got %s", id.ClientID)
	}
	if id.AuthType != "saml" {
		t.Errorf("expected authType saml, got %s", id.AuthType)
	}
}

func TestSAMLAuth_AuthenticateInvalidHeader(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	r := httptest.NewRequest("GET", "/api/test", nil)
	r.Header.Set("X-SAML-Assertion", "not-valid-base64!!!!")

	_, err := a.Authenticate(r)
	if err == nil {
		t.Error("expected error for invalid header")
	}
}

func TestSAMLAuth_AssertionReplayRejection(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	now := time.Now()
	assertion := saml.Assertion{
		ID:           "replay-test-id",
		IssueInstant: now,
		Version:      "2.0",
		Subject: &saml.Subject{
			NameID: &saml.NameID{Value: "user@example.com"},
		},
		Conditions: &saml.Conditions{
			NotBefore:    now.Add(-time.Minute),
			NotOnOrAfter: now.Add(5 * time.Minute),
		},
	}

	xmlData, _ := xml.Marshal(assertion)
	headerVal := base64.StdEncoding.EncodeToString(xmlData)

	// First attempt should succeed
	r1 := httptest.NewRequest("GET", "/api/test", nil)
	r1.Header.Set("X-SAML-Assertion", headerVal)
	_, err := a.Authenticate(r1)
	if err != nil {
		t.Fatalf("first Authenticate should succeed: %v", err)
	}

	// Second attempt with same assertion ID should fail (replay)
	r2 := httptest.NewRequest("GET", "/api/test", nil)
	r2.Header.Set("X-SAML-Assertion", headerVal)
	_, err = a.Authenticate(r2)
	if err == nil {
		t.Error("expected replay rejection for same assertion ID")
	}
}

func TestSAMLAuth_HandleMetadata(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	r := httptest.NewRequest("GET", "/saml/metadata", nil)
	w := httptest.NewRecorder()

	a.HandleMetadata(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/samlmetadata+xml" {
		t.Errorf("expected content-type application/samlmetadata+xml, got %s", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "https://sp.example.com") {
		t.Error("metadata should contain entity ID")
	}
	if !strings.Contains(body, "/saml/acs") {
		t.Error("metadata should contain ACS URL")
	}
	// SLO URL is only included when LogoutBindings is configured on the SP.
	// The metadata should at least contain the EntityDescriptor with ACS.
}

func TestSAMLAuth_HandleLoginRelativePathOnly(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// Absolute URL should be rejected
	r := httptest.NewRequest("GET", "/saml/login?return_to=https://evil.com/steal", nil)
	w := httptest.NewRecorder()

	a.HandleLogin(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for absolute return_to, got %d", w.Code)
	}

	// Relative path should be accepted (redirects to IdP)
	r = httptest.NewRequest("GET", "/saml/login?return_to=/dashboard", nil)
	w = httptest.NewRecorder()

	a.HandleLogin(w, r)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d", w.Code)
	}
	location := w.Header().Get("Location")
	if !strings.Contains(location, "idp.example.com") {
		t.Errorf("expected redirect to IdP, got %s", location)
	}
}

func TestSAMLAuth_HandleLoginProtocolRelativeRejected(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	r := httptest.NewRequest("GET", "/saml/login?return_to=//evil.com/steal", nil)
	w := httptest.NewRecorder()

	a.HandleLogin(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for protocol-relative return_to, got %d", w.Code)
	}
}

func TestSAMLAuth_IDPInitiatedRejected(t *testing.T) {
	fix := newTestSAMLFixture(t)
	cfg := newTestSAMLConfig(fix)
	cfg.AllowIDPInitiated = false

	a, err := NewSAMLAuth(cfg)
	if err != nil {
		t.Fatalf("NewSAMLAuth failed: %v", err)
	}
	defer a.Close()

	if a.allowIDPInit {
		t.Error("expected allowIDPInit to be false")
	}
}

func TestSAMLAuth_MatchesPath(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	tests := []struct {
		path    string
		matches bool
	}{
		{"/saml/metadata", true},
		{"/saml/acs", true},
		{"/saml/login", true},
		{"/saml/slo", true},
		{"/saml/", true},
		{"/api/test", false},
		{"/other", false},
	}

	for _, tt := range tests {
		if got := a.MatchesPath(tt.path); got != tt.matches {
			t.Errorf("MatchesPath(%q) = %v, want %v", tt.path, got, tt.matches)
		}
	}
}

func TestSAMLAuth_Stats(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	a.ssoAttempts.Add(5)
	a.ssoSuccesses.Add(3)
	a.ssoFailures.Add(2)
	a.tokenValidations.Add(10)
	a.tokenSuccesses.Add(8)
	a.tokenFailures.Add(2)
	a.sessionAuths.Add(15)
	a.logoutRequests.Add(1)

	stats := a.Stats()
	if stats.SSOAttempts != 5 {
		t.Errorf("expected ssoAttempts 5, got %d", stats.SSOAttempts)
	}
	if stats.SSOSuccesses != 3 {
		t.Errorf("expected ssoSuccesses 3, got %d", stats.SSOSuccesses)
	}
	if stats.TokenValidations != 10 {
		t.Errorf("expected tokenValidations 10, got %d", stats.TokenValidations)
	}
	if stats.SessionAuths != 15 {
		t.Errorf("expected sessionAuths 15, got %d", stats.SessionAuths)
	}
	if stats.LogoutRequests != 1 {
		t.Errorf("expected logoutRequests 1, got %d", stats.LogoutRequests)
	}
}

func TestSAMLAuth_NameIDFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected saml.NameIDFormat
	}{
		{"email", saml.EmailAddressNameIDFormat},
		{"persistent", saml.PersistentNameIDFormat},
		{"transient", saml.TransientNameIDFormat},
		{"unspecified", saml.UnspecifiedNameIDFormat},
		{"", saml.UnspecifiedNameIDFormat},
	}

	for _, tt := range tests {
		got := mapNameIDFormat(tt.input)
		if got != tt.expected {
			t.Errorf("mapNameIDFormat(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestSAMLAuth_RelayStateSignVerify(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	returnTo := "/dashboard/settings"
	signed := a.signRelayState(returnTo)

	// Valid relay state
	dest, ok := a.verifyRelayState(signed)
	if !ok {
		t.Error("expected relay state verification to succeed")
	}
	if dest != returnTo {
		t.Errorf("expected %s, got %s", returnTo, dest)
	}

	// Tampered relay state
	_, ok = a.verifyRelayState(signed + "tampered")
	if ok {
		t.Error("expected tampered relay state to fail verification")
	}
}

func TestSAMLAuth_IsRelativePath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/dashboard", true},
		{"/", true},
		{"/api/v1/test", true},
		{"https://evil.com", false},
		{"http://evil.com", false},
		{"//evil.com", false},
		{"", false},
		{"relative", false},
	}

	for _, tt := range tests {
		got := isRelativePath(tt.path)
		if got != tt.expected {
			t.Errorf("isRelativePath(%q) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}

func TestSAMLAuth_ServeHTTP_Dispatch(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// Metadata
	r := httptest.NewRequest("GET", "/saml/metadata", nil)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET /saml/metadata: expected 200, got %d", w.Code)
	}

	// Login
	r = httptest.NewRequest("GET", "/saml/login", nil)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Errorf("GET /saml/login: expected 302, got %d", w.Code)
	}

	// Unknown path
	r = httptest.NewRequest("GET", "/saml/unknown", nil)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /saml/unknown: expected 404, got %d", w.Code)
	}
}

func TestSAMLAuth_CustomPathPrefix(t *testing.T) {
	fix := newTestSAMLFixture(t)
	cfg := newTestSAMLConfig(fix)
	cfg.PathPrefix = "/auth/saml/"

	a, err := NewSAMLAuth(cfg)
	if err != nil {
		t.Fatalf("NewSAMLAuth failed: %v", err)
	}
	defer a.Close()

	if a.pathPrefix != "/auth/saml/" {
		t.Errorf("expected pathPrefix /auth/saml/, got %s", a.pathPrefix)
	}

	if !a.MatchesPath("/auth/saml/metadata") {
		t.Error("expected /auth/saml/metadata to match")
	}
	if a.MatchesPath("/saml/metadata") {
		t.Error("expected /saml/metadata to not match custom prefix")
	}
}

func TestSAMLAuth_MetadataRefreshURL(t *testing.T) {
	// Start a test HTTP server that serves IdP metadata
	fix := newTestSAMLFixture(t)
	metadataContent, err := os.ReadFile(fix.idpMetadataFile)
	if err != nil {
		t.Fatalf("failed to read metadata: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write(metadataContent)
	}))
	defer srv.Close()

	cfg := newTestSAMLConfig(fix)
	cfg.IDPMetadataFile = ""
	cfg.IDPMetadataURL = srv.URL + "/metadata"
	cfg.MetadataRefreshInterval = 0 // disable auto-refresh for test

	a, err := NewSAMLAuth(cfg)
	if err != nil {
		t.Fatalf("NewSAMLAuth with URL failed: %v", err)
	}
	defer a.Close()

	if a.sp.IDPMetadata == nil {
		t.Error("expected IdP metadata to be loaded from URL")
	}
}

func TestSAMLAuth_HandleSLO_SPInitiated(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// Create a session cookie first
	identity := &variables.Identity{
		ClientID: "testuser",
		AuthType: "saml",
		Claims:   map[string]interface{}{},
	}
	token, _ := a.mintSessionToken(identity)

	r := httptest.NewRequest("GET", "/saml/slo", nil)
	r.AddCookie(&http.Cookie{Name: a.cookieName, Value: token})
	w := httptest.NewRecorder()

	a.HandleSLO(w, r)

	// Should redirect (either to IdP or to /)
	if w.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d", w.Code)
	}

	// Should have set a clearing cookie
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == a.cookieName && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected session cookie to be cleared")
	}
}

func TestSAMLAuth_HandleACS_FormParseFail(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// POST to ACS with invalid form data
	r := httptest.NewRequest("POST", "/saml/acs", strings.NewReader("SAMLResponse=invalid"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	a.HandleACS(w, r)

	// Should fail (invalid SAML response)
	if w.Code == http.StatusOK || w.Code == http.StatusFound {
		t.Errorf("expected error status for invalid SAML response, got %d", w.Code)
	}
}


func TestSAMLAuth_BuildIdentity(t *testing.T) {
	a, _ := newTestSAMLAuth(t)
	a.attrEmail = "email"
	a.attrDisplayName = "displayName"
	a.attrRoles = "roles"

	assertion := &saml.Assertion{
		AttributeStatements: []saml.AttributeStatement{
			{
				Attributes: []saml.Attribute{
					{Name: "uid", Values: []saml.AttributeValue{{Value: "jdoe"}}},
					{Name: "email", Values: []saml.AttributeValue{{Value: "jdoe@example.com"}}},
					{Name: "displayName", Values: []saml.AttributeValue{{Value: "John Doe"}}},
					{Name: "roles", Values: []saml.AttributeValue{{Value: "admin"}, {Value: "user"}}},
				},
			},
		},
	}

	identity := a.buildIdentity("jdoe@nameid.example.com", assertion)

	if identity.ClientID != "jdoe" {
		t.Errorf("expected clientID jdoe, got %s", identity.ClientID)
	}
	if identity.Claims["email"] != "jdoe@example.com" {
		t.Errorf("expected email jdoe@example.com, got %v", identity.Claims["email"])
	}
	if identity.Claims["display_name"] != "John Doe" {
		t.Errorf("expected display_name John Doe, got %v", identity.Claims["display_name"])
	}
	roles, ok := identity.Claims["roles"].([]string)
	if !ok || len(roles) != 2 || roles[0] != "admin" {
		t.Errorf("expected roles [admin user], got %v", identity.Claims["roles"])
	}
	if identity.Claims["name_id"] != "jdoe@nameid.example.com" {
		t.Errorf("expected name_id in claims")
	}
}

func TestSAMLAuth_CheckAndRecordAssertion(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// First check should succeed (new assertion)
	if !a.checkAndRecordAssertion("assertion-1", time.Minute) {
		t.Error("expected first check to succeed")
	}

	// Second check with same ID should fail (replay)
	if a.checkAndRecordAssertion("assertion-1", time.Minute) {
		t.Error("expected replay check to fail")
	}

	// Different assertion ID should succeed
	if !a.checkAndRecordAssertion("assertion-2", time.Minute) {
		t.Error("expected different assertion to succeed")
	}
}

func TestSAMLAuth_HandleSLO_IDPInitiated(t *testing.T) {
	a, _ := newTestSAMLAuth(t)

	// Create a mock IdP-initiated LogoutRequest XML manually
	lrXML := `<LogoutRequest xmlns="urn:oasis:names:tc:SAML:2.0:protocol" ID="logout-req-1" Version="2.0" IssueInstant="` + time.Now().UTC().Format("2006-01-02T15:04:05Z") + `" Destination="https://sp.example.com/saml/slo"><Issuer xmlns="urn:oasis:names:tc:SAML:2.0:assertion">https://idp.example.com</Issuer><NameID>testuser</NameID></LogoutRequest>`
	lrB64 := base64.StdEncoding.EncodeToString([]byte(lrXML))

	r := httptest.NewRequest("GET", "/saml/slo?SAMLRequest="+url.QueryEscape(lrB64), nil)
	w := httptest.NewRecorder()

	a.HandleSLO(w, r)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d", w.Code)
	}
}
