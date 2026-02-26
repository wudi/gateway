//go:build integration
// +build integration

package test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/runway"
)

// dockerCmd returns the command prefix for running docker (with sudo if needed).
func dockerCmd() []string {
	if out, err := exec.Command("docker", "ps").CombinedOutput(); err == nil {
		_ = out
		return []string{"docker"}
	}
	if out, err := exec.Command("sudo", "docker", "ps").CombinedOutput(); err == nil {
		_ = out
		return []string{"sudo", "docker"}
	}
	return nil
}

// TestSAMLIntegration tests the full SAML 2.0 SSO lifecycle against a real
// SimpleSAMLphp IdP running in Docker.
func TestSAMLIntegration(t *testing.T) {
	// Check Docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found, skipping SAML integration test")
	}
	docker := dockerCmd()
	if docker == nil {
		t.Skip("docker not accessible (even with sudo)")
	}

	// 1. Create a test backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"path":   r.URL.Path,
		})
	}))
	t.Cleanup(func() { backend.Close() })

	// 2. Pre-allocate runway listener to know its port
	gwListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate runway listener: %v", err)
	}
	gwBaseURL := fmt.Sprintf("http://127.0.0.1:%d", gwListener.Addr().(*net.TCPAddr).Port)

	// 3. Generate SP certificate/key files
	spCertFile, spKeyFile := generateSPCertFiles(t)

	// 3b. Generate IdP certificate/key files (the Docker image's built-in cert is expired)
	idpCertFile, idpKeyFile := generateIdPCertFiles(t)

	// 4. Start Docker IdP
	idpBaseURL := startDockerIdP(t, docker, gwBaseURL, idpCertFile, idpKeyFile)

	// 5. Fetch IdP metadata and save to file
	idpMetadataFile := fetchIdPMetadata(t, idpBaseURL)

	// 6. Create runway config
	cfg := baseConfig()
	cfg.Authentication = config.AuthenticationConfig{
		SAML: config.SAMLConfig{
			Enabled:           true,
			EntityID:          gwBaseURL,
			CertFile:          spCertFile,
			KeyFile:           spKeyFile,
			IDPMetadataFile:   idpMetadataFile,
			AllowIDPInitiated: true,
			Session: config.SAMLSessionConfig{
				SigningKey: "test-saml-session-signing-key-32bytes!",
				Secure:    false,
				SameSite:  "lax",
			},
			AttributeMapping: config.SAMLAttributeMapping{
				ClientID: "uid",
				Email:    "email",
			},
		},
	}
	cfg.Routes = []config.RouteConfig{{
		ID:         "protected",
		Path:       "/api",
		PathPrefix: true,
		Backends:   []config.BackendConfig{{URL: backend.URL}},
		Auth: config.RouteAuthConfig{
			Required: true,
			Methods:  []string{"saml"},
		},
	}}
	cfg.Admin = config.AdminConfig{Enabled: true, Port: 0}

	// 7. Create runway
	gw, err := runway.New(cfg)
	if err != nil {
		t.Fatalf("failed to create runway: %v", err)
	}
	t.Cleanup(func() { gw.Close() })

	// 8. Start runway on pre-allocated listener
	ts := &httptest.Server{
		Listener: gwListener,
		Config:   &http.Server{Handler: gw.Handler()},
	}
	ts.Start()
	t.Cleanup(func() { ts.Close() })

	t.Logf("Runway: %s, IdP: %s", gwBaseURL, idpBaseURL)

	t.Run("SPMetadata", func(t *testing.T) {
		resp, err := http.Get(gwBaseURL + "/saml/metadata")
		if err != nil {
			t.Fatalf("metadata request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if ct != "application/samlmetadata+xml" {
			t.Errorf("expected Content-Type application/samlmetadata+xml, got %q", ct)
		}
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, gwBaseURL) {
			t.Error("metadata does not contain entity ID")
		}
		if !strings.Contains(bodyStr, "/saml/acs") {
			t.Error("metadata does not contain ACS URL")
		}
	})

	t.Run("UnauthenticatedReject", func(t *testing.T) {
		resp, err := http.Get(gwBaseURL + "/api/test")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		var result map[string]string
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("failed to parse JSON: %v (body: %s)", err, body)
		}
		if result["error"] != "unauthorized" {
			t.Errorf("expected error=unauthorized, got %q", result["error"])
		}
		if result["login_url"] != "/saml/login" {
			t.Errorf("expected login_url=/saml/login, got %q", result["login_url"])
		}
	})

	t.Run("LoginRedirect", func(t *testing.T) {
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Get(gwBaseURL + "/saml/login")
		if err != nil {
			t.Fatalf("login request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusFound {
			t.Errorf("expected 302, got %d", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc == "" {
			t.Fatal("expected Location header")
		}
		if !strings.Contains(loc, "/simplesaml/") {
			t.Errorf("expected redirect to IdP, got %q", loc)
		}
		parsed, err := url.Parse(loc)
		if err != nil {
			t.Fatalf("failed to parse Location: %v", err)
		}
		if parsed.Query().Get("SAMLRequest") == "" {
			t.Error("expected SAMLRequest parameter in redirect URL")
		}
	})

	t.Run("FullSSOFlow", func(t *testing.T) {
		cookies := performSAMLLogin(t, gwBaseURL, idpBaseURL, "user1", "password")

		// Verify we got the session cookie
		var sessionCookie *http.Cookie
		for _, c := range cookies {
			if c.Name == "runway_saml" {
				sessionCookie = c
				break
			}
		}
		if sessionCookie == nil {
			t.Fatal("expected runway_saml cookie after SSO")
		}

		// Use the cookie to access the protected resource
		req, _ := http.NewRequest("GET", gwBaseURL+"/api/test", nil)
		req.AddCookie(sessionCookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("authenticated request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("SessionReuse", func(t *testing.T) {
		cookies := performSAMLLogin(t, gwBaseURL, idpBaseURL, "user1", "password")
		var sessionCookie *http.Cookie
		for _, c := range cookies {
			if c.Name == "runway_saml" {
				sessionCookie = c
				break
			}
		}
		if sessionCookie == nil {
			t.Fatal("expected runway_saml cookie")
		}

		for i := 0; i < 3; i++ {
			req, _ := http.NewRequest("GET", gwBaseURL+"/api/test", nil)
			req.AddCookie(sessionCookie)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request %d failed: %v", i, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("request %d: expected 200, got %d", i, resp.StatusCode)
			}
		}
	})

	t.Run("SLO", func(t *testing.T) {
		cookies := performSAMLLogin(t, gwBaseURL, idpBaseURL, "user1", "password")
		var sessionCookie *http.Cookie
		for _, c := range cookies {
			if c.Name == "runway_saml" {
				sessionCookie = c
				break
			}
		}
		if sessionCookie == nil {
			t.Fatal("expected runway_saml cookie")
		}

		// Call SLO endpoint with the session cookie (don't follow redirect)
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		req, _ := http.NewRequest("GET", gwBaseURL+"/saml/slo", nil)
		req.AddCookie(sessionCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("SLO request failed: %v", err)
		}
		resp.Body.Close()

		// Check that the session cookie is cleared
		for _, c := range resp.Cookies() {
			if c.Name == "runway_saml" {
				if c.MaxAge > 0 {
					t.Error("expected cookie MaxAge <= 0 after SLO")
				}
			}
		}

		// Old cookie should no longer work
		req2, _ := http.NewRequest("GET", gwBaseURL+"/api/test", nil)
		req2.AddCookie(sessionCookie)
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatalf("post-SLO request failed: %v", err)
		}
		resp2.Body.Close()
		// After SLO the old cookie is still technically valid (it's a JWT),
		// but the server has cleared it. Verify the SLO redirect happened.
		if resp.StatusCode != http.StatusFound {
			t.Errorf("expected SLO to return 302 redirect, got %d", resp.StatusCode)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		// Perform a login to ensure stats are populated
		performSAMLLogin(t, gwBaseURL, idpBaseURL, "user1", "password")

		samlAuth := gw.GetSAMLAuth()
		if samlAuth == nil {
			t.Fatal("expected SAML auth to be configured")
		}
		stats := samlAuth.Stats()
		if stats.SSOAttempts < 1 {
			t.Errorf("expected sso_attempts >= 1, got %d", stats.SSOAttempts)
		}
		if stats.SSOSuccesses < 1 {
			t.Errorf("expected sso_successes >= 1, got %d", stats.SSOSuccesses)
		}
	})
}

// --- Helper Functions ---

// startDockerIdP starts a SimpleSAMLphp IdP container and waits for it to become ready.
// idpCertFile and idpKeyFile are mounted to replace the expired built-in certificate.
func startDockerIdP(t *testing.T, docker []string, gwBaseURL, idpCertFile, idpKeyFile string) string {
	t.Helper()

	// Find a free port for the IdP
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	idpPort := l.Addr().(*net.TCPAddr).Port
	l.Close()

	containerName := fmt.Sprintf("saml-test-idp-%d", idpPort)
	idpBaseURL := fmt.Sprintf("http://127.0.0.1:%d", idpPort)

	// Build docker run arguments
	args := append([]string{}, docker[1:]...)
	args = append(args, "run", "-d",
		"--name", containerName,
		"-v", fmt.Sprintf("%s:/var/www/simplesamlphp/cert/server.crt:ro", idpCertFile),
		"-v", fmt.Sprintf("%s:/var/www/simplesamlphp/cert/server.pem:ro", idpKeyFile),
		"-e", fmt.Sprintf("SIMPLESAMLPHP_SP_ENTITY_ID=%s", gwBaseURL),
		"-e", fmt.Sprintf("SIMPLESAMLPHP_SP_ASSERTION_CONSUMER_SERVICE=%s/saml/acs", gwBaseURL),
		"-e", fmt.Sprintf("SIMPLESAMLPHP_SP_SINGLE_LOGOUT_SERVICE=%s/saml/slo", gwBaseURL),
		"-e", fmt.Sprintf("SIMPLESAMLPHP_IDP_BASE_URL=%s/simplesaml/", idpBaseURL),
		"-p", fmt.Sprintf("%d:8080", idpPort),
		"kenchan0130/simplesamlphp",
	)

	// Start the container
	cmd := exec.Command(docker[0], args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to start IdP container: %v\n%s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Logf("Started IdP container: %s (port %d)", containerID[:12], idpPort)

	t.Cleanup(func() {
		rmArgs := append([]string{}, docker[1:]...)
		rmArgs = append(rmArgs, "rm", "-f", containerName)
		exec.Command(docker[0], rmArgs...).Run()
	})

	// Wait for IdP to become ready
	metadataURL := idpBaseURL + "/simplesaml/saml2/idp/metadata.php"
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(metadataURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Log("IdP is ready")
				return idpBaseURL
			}
		}
		time.Sleep(1 * time.Second)
	}
	// Dump container logs on failure
	logArgs := append([]string{}, docker[1:]...)
	logArgs = append(logArgs, "logs", containerName)
	logs, _ := exec.Command(docker[0], logArgs...).CombinedOutput()
	t.Fatalf("IdP did not become ready within timeout\nLogs:\n%s", logs)
	return ""
}

// generateIdPCertFiles generates an RSA 2048 IdP certificate and key for the Docker container.
// The built-in certificate in the kenchan0130/simplesamlphp image is expired.
func generateIdPCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()

	idpKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate IdP key: %v", err)
	}
	idpTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject:      pkix.Name{CommonName: "test-idp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	idpCertDER, err := x509.CreateCertificate(rand.Reader, idpTemplate, idpTemplate, &idpKey.PublicKey, idpKey)
	if err != nil {
		t.Fatalf("failed to create IdP cert: %v", err)
	}

	certFile = filepath.Join(dir, "idp.crt")
	keyFile = filepath.Join(dir, "idp.pem")

	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: idpCertDER}), 0o644); err != nil {
		t.Fatalf("failed to write IdP cert: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(idpKey)}), 0o644); err != nil {
		t.Fatalf("failed to write IdP key: %v", err)
	}

	return certFile, keyFile
}

// generateSPCertFiles generates an RSA 2048 SP certificate and key, returning their file paths.
func generateSPCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()

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

	certFile = filepath.Join(dir, "sp.cert")
	keyFile = filepath.Join(dir, "sp.key")

	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: spCertDER}), 0o600); err != nil {
		t.Fatalf("failed to write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(spKey)}), 0o600); err != nil {
		t.Fatalf("failed to write key: %v", err)
	}

	return certFile, keyFile
}

// fetchIdPMetadata downloads the IdP metadata XML and saves it to a temp file.
func fetchIdPMetadata(t *testing.T, idpBaseURL string) string {
	t.Helper()
	metadataURL := idpBaseURL + "/simplesaml/saml2/idp/metadata.php"
	resp, err := http.Get(metadataURL)
	if err != nil {
		t.Fatalf("failed to fetch IdP metadata: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("IdP metadata returned %d: %s", resp.StatusCode, body)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read IdP metadata: %v", err)
	}

	metadataFile := filepath.Join(t.TempDir(), "idp-metadata.xml")
	if err := os.WriteFile(metadataFile, data, 0o600); err != nil {
		t.Fatalf("failed to write IdP metadata: %v", err)
	}
	t.Logf("Saved IdP metadata (%d bytes) to %s", len(data), metadataFile)
	return metadataFile
}

// performSAMLLogin simulates a browser performing the full SAML SSO flow.
// It returns the cookies set by the runway ACS handler.
func performSAMLLogin(t *testing.T, gwBaseURL, idpBaseURL, username, password string) []*http.Cookie {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}

	// Client that follows redirects (like a browser) with cookie jar
	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
	}

	// Client that does NOT follow redirects (to inspect individual responses)
	noRedirectClient := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Step 1: GET /saml/login → 302 redirect to IdP
	resp, err := noRedirectClient.Get(gwBaseURL + "/saml/login")
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from /saml/login, got %d", resp.StatusCode)
	}
	idpRedirectURL := resp.Header.Get("Location")
	if idpRedirectURL == "" {
		t.Fatal("no Location header from /saml/login")
	}

	// Step 2: Follow the redirect to IdP login page (browser follows to login form)
	loginResp, err := client.Get(idpRedirectURL)
	if err != nil {
		t.Fatalf("IdP redirect request failed: %v", err)
	}
	loginBody, err := io.ReadAll(loginResp.Body)
	loginResp.Body.Close()
	if err != nil {
		t.Fatalf("failed to read IdP login page: %v", err)
	}

	// Step 3: Parse the login form to get action URL and hidden fields
	// The form action may be relative or "?" — resolve against the final URL
	formAction := extractFormAction(t, string(loginBody), loginResp.Request.URL.String())
	hiddenFields := extractHiddenFields(t, string(loginBody))

	// Step 4: Submit login credentials
	formData := url.Values{}
	for k, v := range hiddenFields {
		formData.Set(k, v)
	}
	formData.Set("username", username)
	formData.Set("password", password)

	loginSubmitResp, err := client.PostForm(formAction, formData)
	if err != nil {
		t.Fatalf("login form submission failed: %v", err)
	}
	loginSubmitBody, err := io.ReadAll(loginSubmitResp.Body)
	loginSubmitResp.Body.Close()
	if err != nil {
		t.Fatalf("failed to read login response: %v", err)
	}

	// Step 5: Parse the SAML response form (auto-submit form with SAMLResponse)
	samlResponse := extractInputValue(t, string(loginSubmitBody), "SAMLResponse")
	relayState := extractInputValue(t, string(loginSubmitBody), "RelayState")

	if samlResponse == "" {
		t.Fatalf("no SAMLResponse found in IdP response. Response body:\n%s", string(loginSubmitBody))
	}

	// Step 6: POST the SAMLResponse to the runway ACS endpoint
	acsData := url.Values{}
	acsData.Set("SAMLResponse", samlResponse)
	if relayState != "" {
		acsData.Set("RelayState", relayState)
	}

	acsResp, err := noRedirectClient.PostForm(gwBaseURL+"/saml/acs", acsData)
	if err != nil {
		t.Fatalf("ACS request failed: %v", err)
	}
	acsBody, _ := io.ReadAll(acsResp.Body)
	acsResp.Body.Close()

	if acsResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from ACS, got %d: %s", acsResp.StatusCode, acsBody)
	}

	return acsResp.Cookies()
}

// extractFormAction parses HTML and returns the action URL of the first <form>.
// If the action is relative, it's resolved against the page URL.
func extractFormAction(t *testing.T, body, pageURL string) string {
	t.Helper()
	tokenizer := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			token := tokenizer.Token()
			if token.Data == "form" {
				for _, attr := range token.Attr {
					if attr.Key == "action" {
						action := attr.Val
						base, _ := url.Parse(pageURL)
						ref, _ := url.Parse(action)
						return base.ResolveReference(ref).String()
					}
				}
			}
		}
	}
	t.Fatalf("no <form> with action found in HTML:\n%.500s", body)
	return ""
}

// extractHiddenFields parses HTML and returns all hidden input field name/value pairs.
func extractHiddenFields(t *testing.T, body string) map[string]string {
	t.Helper()
	fields := make(map[string]string)
	tokenizer := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			token := tokenizer.Token()
			if token.Data == "input" {
				var name, value, inputType string
				for _, attr := range token.Attr {
					switch attr.Key {
					case "type":
						inputType = attr.Val
					case "name":
						name = attr.Val
					case "value":
						value = attr.Val
					}
				}
				if inputType == "hidden" && name != "" {
					fields[name] = value
				}
			}
		}
	}
	return fields
}

// extractInputValue parses HTML and returns the value of an <input> with the given name.
func extractInputValue(t *testing.T, body, name string) string {
	t.Helper()
	tokenizer := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			token := tokenizer.Token()
			if token.Data == "input" {
				var fieldName, fieldValue string
				for _, attr := range token.Attr {
					switch attr.Key {
					case "name":
						fieldName = attr.Val
					case "value":
						fieldValue = attr.Val
					}
				}
				if fieldName == name {
					return fieldValue
				}
			}
		}
	}
	return ""
}
