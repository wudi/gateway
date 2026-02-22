package tokenexchange

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

// testKeyPair generates RSA key pair and returns PEM-encoded private key and a JWKS server.
func testKeyPair(t *testing.T) (*rsa.PrivateKey, string, *httptest.Server) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	privBytes, _ := x509.MarshalPKCS8PrivateKey(key)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}))

	// Create JWKS server
	jwkKey, err := jwk.FromRaw(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	jwkKey.Set(jwk.KeyIDKey, "test-kid")
	jwkKey.Set(jwk.AlgorithmKey, jwa.RS256)
	jwkKey.Set(jwk.KeyUsageKey, "sig")

	set := jwk.NewSet()
	set.AddKey(jwkKey)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(set)
	}))

	return key, privPEM, server
}

// mintSubjectToken creates a signed JWT for testing.
func mintSubjectToken(t *testing.T, key *rsa.PrivateKey, issuer, subject string, extraClaims map[string]interface{}) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": subject,
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	for k, v := range extraClaims {
		claims[k] = v
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-kid"
	tokenStr, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return tokenStr
}

func TestExchange_JWT_Success(t *testing.T) {
	subjectKey, issuingPrivPEM, jwksServer := testKeyPair(t)
	defer jwksServer.Close()

	// Create a separate issuing key
	issuingKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	issuingPrivBytes, _ := x509.MarshalPKCS8PrivateKey(issuingKey)
	issuingPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: issuingPrivBytes}))

	// Use the subject key's JWKS server and a separate issuing key
	_ = issuingPrivPEM // not needed; use issuingPEM

	validator, err := NewJWTValidator(jwksServer.URL, []string{"https://test-idp.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	issuer, err := NewTokenIssuer("RS256", issuingPEM, "", "", "https://gateway.example.com", []string{"internal"}, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	te := &TokenExchanger{
		routeID:   "test-route",
		validator: validator,
		issuer:    issuer,
		claimMappings: map[string]string{
			"sub":   "sub",
			"email": "email",
		},
		scopes: []string{"read", "write"},
	}

	// Mint a subject token
	subjectToken := mintSubjectToken(t, subjectKey, "https://test-idp.example.com", "user-123", map[string]interface{}{
		"email": "user@example.com",
	})

	// Exchange
	issuedToken, err := te.Exchange(subjectToken)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}

	// Parse the issued token (don't verify signature, just check claims)
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, err := parser.ParseUnverified(issuedToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse issued token: %v", err)
	}

	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != "https://gateway.example.com" {
		t.Errorf("expected gateway issuer, got %v", claims["iss"])
	}
	if claims["sub"] != "user-123" {
		t.Errorf("expected sub user-123, got %v", claims["sub"])
	}
	if claims["email"] != "user@example.com" {
		t.Errorf("expected email mapped, got %v", claims["email"])
	}
}

func TestExchange_JWT_UntrustedIssuer(t *testing.T) {
	subjectKey, _, jwksServer := testKeyPair(t)
	defer jwksServer.Close()

	validator, err := NewJWTValidator(jwksServer.URL, []string{"https://trusted.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// Use HMAC for simplicity in issuing
	issuer, err := NewTokenIssuer("HS256", "", "", "supersecretkeythatisatleast32bytes!", "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	te := &TokenExchanger{
		routeID:   "test-route",
		validator: validator,
		issuer:    issuer,
	}

	// Token from untrusted issuer
	subjectToken := mintSubjectToken(t, subjectKey, "https://untrusted.example.com", "user-456", nil)

	_, err = te.Exchange(subjectToken)
	if err == nil {
		t.Error("expected error for untrusted issuer")
	}
	if !strings.Contains(err.Error(), "untrusted") {
		t.Errorf("expected untrusted error, got: %v", err)
	}
}

func TestExchange_JWT_ExpiredToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	// Create JWKS server
	jwkKey, _ := jwk.FromRaw(&key.PublicKey)
	jwkKey.Set(jwk.KeyIDKey, "exp-kid")
	jwkKey.Set(jwk.AlgorithmKey, jwa.RS256)
	set := jwk.NewSet()
	set.AddKey(jwkKey)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(set)
	}))
	defer server.Close()

	validator, err := NewJWTValidator(server.URL, []string{"https://idp.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	issuer, err := NewTokenIssuer("HS256", "", "", "supersecretkeythatisatleast32bytes!", "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	te := &TokenExchanger{
		routeID:   "test-route",
		validator: validator,
		issuer:    issuer,
	}

	// Mint an expired token
	claims := jwt.MapClaims{
		"iss": "https://idp.example.com",
		"sub": "user-expired",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "exp-kid"
	expiredToken, _ := token.SignedString(key)

	_, err = te.Exchange(expiredToken)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestExchange_Cache(t *testing.T) {
	subjectKey, _, jwksServer := testKeyPair(t)
	defer jwksServer.Close()

	validator, err := NewJWTValidator(jwksServer.URL, []string{"https://test-idp.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	issuer, err := NewTokenIssuer("HS256", "", "", "supersecretkeythatisatleast32bytes!", "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	cache := newExchangeCache(10 * time.Minute)

	te := &TokenExchanger{
		routeID:   "test-route",
		validator: validator,
		issuer:    issuer,
		cache:     cache,
	}

	subjectToken := mintSubjectToken(t, subjectKey, "https://test-idp.example.com", "user-cache", nil)

	// First exchange
	issued1, err := te.Exchange(subjectToken)
	if err != nil {
		t.Fatal(err)
	}

	// Second exchange — should hit cache
	issued2, err := te.Exchange(subjectToken)
	if err != nil {
		t.Fatal(err)
	}

	if issued1 != issued2 {
		t.Error("expected cached token to be identical")
	}

	if te.metrics.CacheHits.Load() != 1 {
		t.Errorf("expected 1 cache hit, got %d", te.metrics.CacheHits.Load())
	}
	if te.metrics.Exchanged.Load() != 1 {
		t.Errorf("expected 1 exchange (not 2), got %d", te.metrics.Exchanged.Load())
	}
}

func TestExchange_ClaimMappings(t *testing.T) {
	subjectKey, _, jwksServer := testKeyPair(t)
	defer jwksServer.Close()

	validator, err := NewJWTValidator(jwksServer.URL, []string{"https://idp.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	issuer, err := NewTokenIssuer("HS256", "", "", "supersecretkeythatisatleast32bytes!", "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	te := &TokenExchanger{
		routeID:   "test-route",
		validator: validator,
		issuer:    issuer,
		claimMappings: map[string]string{
			"groups": "roles",
			"email":  "email",
		},
	}

	subjectToken := mintSubjectToken(t, subjectKey, "https://idp.example.com", "user-claims", map[string]interface{}{
		"email":  "test@example.com",
		"groups": []string{"admin", "devops"},
	})

	issuedToken, err := te.Exchange(subjectToken)
	if err != nil {
		t.Fatal(err)
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, _ := parser.ParseUnverified(issuedToken, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	if claims["email"] != "test@example.com" {
		t.Errorf("expected email claim mapped, got %v", claims["email"])
	}
	if claims["roles"] == nil {
		t.Error("expected roles claim mapped from groups")
	}
}

func TestMiddleware_ReplacesToken(t *testing.T) {
	subjectKey, _, jwksServer := testKeyPair(t)
	defer jwksServer.Close()

	validator, err := NewJWTValidator(jwksServer.URL, []string{"https://idp.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	issuer, err := NewTokenIssuer("HS256", "", "", "supersecretkeythatisatleast32bytes!", "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	te := &TokenExchanger{
		routeID:   "test-route",
		validator: validator,
		issuer:    issuer,
	}

	subjectToken := mintSubjectToken(t, subjectKey, "https://idp.example.com", "user-mw", nil)

	var receivedAuth string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})

	handler := te.Middleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+subjectToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !strings.HasPrefix(receivedAuth, "Bearer ") {
		t.Error("expected Bearer token in Authorization header")
	}
	// The received token should be different from the subject token
	receivedToken := strings.TrimPrefix(receivedAuth, "Bearer ")
	if receivedToken == subjectToken {
		t.Error("expected gateway-issued token, not original subject token")
	}
}

func TestMiddleware_NoBearerToken_Passthrough(t *testing.T) {
	te := &TokenExchanger{
		routeID: "test-route",
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := te.Middleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("expected next handler to be called when no bearer token")
	}
}

func TestIntrospectionValidator(t *testing.T) {
	// Mock introspection endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		token := r.FormValue("token")
		if token == "valid-token" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"active":    true,
				"sub":       "introspected-user",
				"iss":       "https://auth.example.com",
				"scope":     "read write",
				"client_id": "client-1",
				"email":     "intro@example.com",
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{"active": false})
		}
	}))
	defer server.Close()

	v := NewIntrospectionValidator(server.URL, "gateway", "secret")

	// Valid token
	result, err := v.Validate("valid-token", "access_token")
	if err != nil {
		t.Fatalf("expected valid result: %v", err)
	}
	if result.Subject != "introspected-user" {
		t.Errorf("expected introspected-user, got %q", result.Subject)
	}
	if result.Claims["email"] != "intro@example.com" {
		t.Error("expected email claim")
	}

	// Invalid token
	_, err = v.Validate("invalid-token", "access_token")
	if err == nil {
		t.Error("expected error for inactive token")
	}
	if !strings.Contains(err.Error(), "not active") {
		t.Errorf("expected 'not active' error, got: %v", err)
	}
}

func TestTokenIssuer_HMAC(t *testing.T) {
	secret := "test-secret-that-is-at-least-32-bytes-long!"
	issuer, err := NewTokenIssuer("HS256", "", "", secret, "https://gw.example.com", []string{"internal"}, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{
		Subject: "user-123",
		Claims: map[string]interface{}{
			"email": "test@example.com",
		},
	}

	tokenStr, err := issuer.Issue(validated, map[string]string{"email": "email"}, []string{"read"})
	if err != nil {
		t.Fatal(err)
	}

	// Parse and verify
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["iss"] != "https://gw.example.com" {
		t.Errorf("wrong issuer: %v", claims["iss"])
	}
	if claims["sub"] != "user-123" {
		t.Errorf("wrong subject: %v", claims["sub"])
	}
}

func TestTokenIssuer_RSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privBytes, _ := x509.MarshalPKCS8PrivateKey(key)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}))

	issuer, err := NewTokenIssuer("RS256", privPEM, "", "", "https://gw.example.com", nil, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{
		Subject: "rsa-user",
		Claims:  map[string]interface{}{},
	}

	tokenStr, err := issuer.Issue(validated, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify with public key
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["sub"] != "rsa-user" {
		t.Errorf("wrong subject: %v", claims["sub"])
	}
}

func TestExchangeStatus(t *testing.T) {
	te := &TokenExchanger{
		routeID: "test-route",
		cache:   newExchangeCache(5 * time.Minute),
	}

	te.metrics.Total.Store(10)
	te.metrics.Exchanged.Store(8)
	te.metrics.CacheHits.Store(5)
	te.metrics.ValidationFails.Store(1)
	te.metrics.IssueFails.Store(1)

	status := te.Status()
	if status.Total != 10 {
		t.Errorf("expected 10 total, got %d", status.Total)
	}
	if status.Exchanged != 8 {
		t.Errorf("expected 8 exchanged, got %d", status.Exchanged)
	}
	if status.CacheHits != 5 {
		t.Errorf("expected 5 cache hits, got %d", status.CacheHits)
	}
}

// tokenIssuerInvalidAlgo is a utility to ensure unused vars are not needed.
func TestTokenIssuer_InvalidAlgo(t *testing.T) {
	_, err := NewTokenIssuer("INVALID", "", "", "", "iss", nil, 10*time.Minute)
	if err == nil {
		t.Error("expected error for invalid algorithm")
	}
}

// Ensure serial number generation works (used internally by jwk).
func init() {
	// Suppress unused import warning for big
	_ = big.NewInt(0)
}
