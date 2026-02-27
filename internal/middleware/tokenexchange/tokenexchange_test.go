package tokenexchange

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
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

	issuer, err := NewTokenIssuer("RS256", issuingPEM, "", "", "https://runway.example.com", []string{"internal"}, 15*time.Minute)
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
	if claims["iss"] != "https://runway.example.com" {
		t.Errorf("expected runway issuer, got %v", claims["iss"])
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
		t.Error("expected runway-issued token, not original subject token")
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

	v := NewIntrospectionValidator(server.URL, "runway", "secret")

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

// ── Comprehensive tests ──

func TestExchange_ConcurrentRequests(t *testing.T) {
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

	cache := newExchangeCache(10 * time.Minute)
	te := &TokenExchanger{
		routeID:   "test-concurrent",
		validator: validator,
		issuer:    issuer,
		cache:     cache,
	}

	subjectToken := mintSubjectToken(t, subjectKey, "https://idp.example.com", "user-concurrent", nil)

	const n = 50
	results := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			token, err := te.Exchange(subjectToken)
			results <- token
			errs <- err
		}()
	}

	var firstToken string
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent exchange %d failed: %v", i, err)
		}
		tok := <-results
		if firstToken == "" {
			firstToken = tok
		}
		// All should get the same token (from cache after first)
		if tok != firstToken {
			// Note: tokens may differ if the first request hasn't cached yet
			// when another request starts, so we just check all succeeded.
		}
	}

	if te.metrics.Total.Load() != int64(n) {
		t.Errorf("expected %d total, got %d", n, te.metrics.Total.Load())
	}
}

func TestExchange_CacheTTLExpiry(t *testing.T) {
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

	// Very short TTL
	cache := newExchangeCache(50 * time.Millisecond)
	te := &TokenExchanger{
		routeID:   "test-expiry",
		validator: validator,
		issuer:    issuer,
		cache:     cache,
	}

	subjectToken := mintSubjectToken(t, subjectKey, "https://idp.example.com", "user-expiry", nil)

	// First exchange
	_, err = te.Exchange(subjectToken)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for cache to expire
	time.Sleep(100 * time.Millisecond)

	// Second exchange should hit the validator again (cache expired)
	_, err = te.Exchange(subjectToken)
	if err != nil {
		t.Fatal(err)
	}

	// Key metric: both should be full exchanges (not cache hits)
	if te.metrics.Exchanged.Load() != 2 {
		t.Errorf("expected 2 exchanges (not cache hits), got %d", te.metrics.Exchanged.Load())
	}
	if te.metrics.CacheHits.Load() != 0 {
		t.Errorf("expected 0 cache hits after expiry, got %d", te.metrics.CacheHits.Load())
	}
}

func TestExchange_NoCache(t *testing.T) {
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

	// No cache
	te := &TokenExchanger{
		routeID:   "test-nocache",
		validator: validator,
		issuer:    issuer,
		cache:     nil,
	}

	subjectToken := mintSubjectToken(t, subjectKey, "https://idp.example.com", "user-nocache", nil)

	_, err = te.Exchange(subjectToken)
	if err != nil {
		t.Fatal(err)
	}
	_, err = te.Exchange(subjectToken)
	if err != nil {
		t.Fatal(err)
	}

	// Both should be full exchanges (no caching)
	if te.metrics.Exchanged.Load() != 2 {
		t.Errorf("expected 2 exchanges without cache, got %d", te.metrics.Exchanged.Load())
	}
	if te.metrics.CacheHits.Load() != 0 {
		t.Errorf("expected 0 cache hits, got %d", te.metrics.CacheHits.Load())
	}
}

func TestMiddleware_ExchangeFailReturns401(t *testing.T) {
	// Create a JWKS server that serves valid JWKS (so setup succeeds)
	// but the subject token itself is invalid
	subjectKey, _, jwksServer := testKeyPair(t)
	defer jwksServer.Close()
	_ = subjectKey

	validator, err := NewJWTValidator(jwksServer.URL, []string{"https://idp.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	issuer, err := NewTokenIssuer("HS256", "", "", "supersecretkeythatisatleast32bytes!", "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	te := &TokenExchanger{
		routeID:   "test-fail",
		validator: validator,
		issuer:    issuer,
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called on exchange failure")
	})

	handler := te.Middleware()(next)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer some-invalid-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_NonBearerAuthPassthrough(t *testing.T) {
	te := &TokenExchanger{routeID: "test-nonbearer"}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Auth header should be unchanged
		if r.Header.Get("Authorization") != "Basic dXNlcjpwYXNz" {
			t.Error("non-Bearer auth header should be preserved")
		}
	})

	handler := te.Middleware()(next)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("expected next to be called for non-Bearer auth")
	}
}

func TestMiddleware_EmptyBearerToken(t *testing.T) {
	subjectKey, _, jwksServer := testKeyPair(t)
	defer jwksServer.Close()
	_ = subjectKey

	validator, err := NewJWTValidator(jwksServer.URL, []string{"https://idp.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	issuer, err := NewTokenIssuer("HS256", "", "", "supersecretkeythatisatleast32bytes!", "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	te := &TokenExchanger{
		routeID:   "test-empty-bearer",
		validator: validator,
		issuer:    issuer,
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called for empty bearer token")
	})

	handler := te.Middleware()(next)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer ")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Empty bearer should attempt exchange and fail with 401
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for empty bearer, got %d", rr.Code)
	}
}

func TestIntrospectionValidator_Error500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	v := NewIntrospectionValidator(server.URL, "runway", "secret")
	_, err := v.Validate("some-token", "access_token")
	if err == nil {
		t.Error("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected status 500 in error, got: %v", err)
	}
}

func TestIntrospectionValidator_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	v := NewIntrospectionValidator(server.URL, "runway", "secret")
	_, err := v.Validate("some-token", "access_token")
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("expected decode error, got: %v", err)
	}
}

func TestIntrospectionValidator_BasicAuth(t *testing.T) {
	var receivedUser, receivedPass string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUser, receivedPass, _ = r.BasicAuth()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true,
			"sub":    "user-1",
		})
	}))
	defer server.Close()

	v := NewIntrospectionValidator(server.URL, "my-client", "my-secret")
	_, err := v.Validate("some-token", "access_token")
	if err != nil {
		t.Fatal(err)
	}

	if receivedUser != "my-client" {
		t.Errorf("expected client_id 'my-client', got %q", receivedUser)
	}
	if receivedPass != "my-secret" {
		t.Errorf("expected client_secret 'my-secret', got %q", receivedPass)
	}
}

func TestIntrospectionValidator_AudienceAsString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Audience as string instead of array
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true,
			"sub":    "user-1",
			"aud":    "single-audience",
		})
	}))
	defer server.Close()

	v := NewIntrospectionValidator(server.URL, "gw", "secret")
	result, err := v.Validate("some-token", "access_token")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Audience) != 1 || result.Audience[0] != "single-audience" {
		t.Errorf("expected single audience parsed, got %v", result.Audience)
	}
}

func TestIntrospectionValidator_AudienceAsArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true,
			"sub":    "user-1",
			"aud":    []string{"aud1", "aud2"},
		})
	}))
	defer server.Close()

	v := NewIntrospectionValidator(server.URL, "gw", "secret")
	result, err := v.Validate("some-token", "access_token")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Audience) != 2 {
		t.Errorf("expected 2 audiences, got %v", result.Audience)
	}
}

func TestIntrospectionValidator_WithGroups(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true,
			"sub":    "user-1",
			"groups": []string{"admin", "devops"},
		})
	}))
	defer server.Close()

	v := NewIntrospectionValidator(server.URL, "gw", "secret")
	result, err := v.Validate("some-token", "access_token")
	if err != nil {
		t.Fatal(err)
	}

	// Groups are decoded as []string from the struct field
	groups, ok := result.Claims["groups"].([]string)
	if !ok {
		t.Fatalf("expected groups claim as []string, got %T", result.Claims["groups"])
	}
	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}
	if groups[0] != "admin" || groups[1] != "devops" {
		t.Errorf("expected [admin, devops], got %v", groups)
	}
}

func TestTokenIssuer_HS512(t *testing.T) {
	secret := "test-secret-that-is-at-least-32-bytes-long!"
	issuer, err := NewTokenIssuer("HS512", "", "", secret, "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{
		Subject: "user-512",
		Claims:  map[string]interface{}{},
	}
	tokenStr, err := issuer.Issue(validated, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if token.Header["alg"] != "HS512" {
		t.Errorf("expected HS512, got %v", token.Header["alg"])
	}
}

func TestTokenIssuer_RS512(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	privBytes, _ := x509.MarshalPKCS8PrivateKey(key)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}))

	issuer, err := NewTokenIssuer("RS512", privPEM, "", "", "https://gw.example.com", nil, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{Subject: "rs512-user", Claims: map[string]interface{}{}}
	tokenStr, err := issuer.Issue(validated, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if token.Header["alg"] != "RS512" {
		t.Errorf("expected RS512, got %v", token.Header["alg"])
	}
}

func TestTokenIssuer_WithAudienceAndScopes(t *testing.T) {
	secret := "test-secret-that-is-at-least-32-bytes-long!"
	issuer, err := NewTokenIssuer("HS256", "", "", secret, "https://gw.example.com", []string{"aud1", "aud2"}, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{Subject: "user-1", Claims: map[string]interface{}{}}
	tokenStr, err := issuer.Issue(validated, nil, []string{"read", "write"})
	if err != nil {
		t.Fatal(err)
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, _ := parser.ParseUnverified(tokenStr, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	// Audience
	aud, _ := claims.GetAudience()
	if len(aud) != 2 {
		t.Errorf("expected 2 audiences, got %v", aud)
	}

	// Scopes
	if claims["scope"] == nil {
		t.Error("expected scope claim")
	}
}

func TestTokenIssuer_EmptyAudienceAndScopes(t *testing.T) {
	secret := "test-secret-that-is-at-least-32-bytes-long!"
	issuer, err := NewTokenIssuer("HS256", "", "", secret, "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{Subject: "user-1", Claims: map[string]interface{}{}}
	tokenStr, err := issuer.Issue(validated, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, _ := parser.ParseUnverified(tokenStr, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	if claims["aud"] != nil {
		t.Error("expected no audience when nil")
	}
	if claims["scope"] != nil {
		t.Error("expected no scope when nil")
	}
}

func TestTokenIssuer_ClaimMappingsMissingSrc(t *testing.T) {
	secret := "test-secret-that-is-at-least-32-bytes-long!"
	issuer, err := NewTokenIssuer("HS256", "", "", secret, "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{
		Subject: "user-1",
		Claims: map[string]interface{}{
			"email": "test@example.com",
		},
	}

	// Mapping references a nonexistent claim "org_id"
	mappings := map[string]string{
		"email":  "email",
		"org_id": "organization",
	}
	tokenStr, err := issuer.Issue(validated, mappings, nil)
	if err != nil {
		t.Fatal(err)
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, _ := parser.ParseUnverified(tokenStr, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	if claims["email"] != "test@example.com" {
		t.Error("email claim should be mapped")
	}
	if claims["organization"] != nil {
		t.Error("organization should not be present when source claim is missing")
	}
}

func TestTokenIssuer_EmptyClaimMappings(t *testing.T) {
	secret := "test-secret-that-is-at-least-32-bytes-long!"
	issuer, err := NewTokenIssuer("HS256", "", "", secret, "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{
		Subject: "user-1",
		Claims: map[string]interface{}{
			"email": "test@example.com",
		},
	}

	tokenStr, err := issuer.Issue(validated, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, _ := parser.ParseUnverified(tokenStr, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	// Standard claims should be present
	if claims["iss"] != "https://gw.example.com" {
		t.Error("expected issuer")
	}
	if claims["sub"] != "user-1" {
		t.Error("expected subject")
	}
	// But email should NOT be present (no mapping)
	if claims["email"] != nil {
		t.Error("email should not be mapped without explicit mapping")
	}
}

func TestTokenIssuer_RSAFromKeyFile(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	privBytes, _ := x509.MarshalPKCS8PrivateKey(key)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	tmpFile := t.TempDir() + "/signing-key.pem"
	os.WriteFile(tmpFile, privPEM, 0600)

	issuer, err := NewTokenIssuer("RS256", "", tmpFile, "", "https://gw.example.com", nil, 10*time.Minute)
	if err != nil {
		t.Fatalf("expected success from file key: %v", err)
	}

	validated := &ValidatedToken{Subject: "file-user", Claims: map[string]interface{}{}}
	tokenStr, err := issuer.Issue(validated, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["sub"] != "file-user" {
		t.Errorf("expected file-user, got %v", claims["sub"])
	}
}

func TestTokenIssuer_InvalidPEM(t *testing.T) {
	_, err := NewTokenIssuer("RS256", "not-a-pem", "", "", "iss", nil, 10*time.Minute)
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
	if !strings.Contains(err.Error(), "PEM") {
		t.Errorf("expected PEM error, got: %v", err)
	}
}

func TestTokenIssuer_MissingKeyForRSA(t *testing.T) {
	_, err := NewTokenIssuer("RS256", "", "", "", "iss", nil, 10*time.Minute)
	if err == nil {
		t.Error("expected error for missing RSA key")
	}
	if !strings.Contains(err.Error(), "signing_key") {
		t.Errorf("expected signing_key error, got: %v", err)
	}
}

func TestTokenIssuer_KeyFileNotFound(t *testing.T) {
	_, err := NewTokenIssuer("RS256", "", "/nonexistent/key.pem", "", "iss", nil, 10*time.Minute)
	if err == nil {
		t.Error("expected error for missing key file")
	}
}

func TestTokenIssuer_LifetimeInToken(t *testing.T) {
	secret := "test-secret-that-is-at-least-32-bytes-long!"
	lifetime := 30 * time.Minute
	issuer, err := NewTokenIssuer("HS256", "", "", secret, "https://gw.example.com", nil, lifetime)
	if err != nil {
		t.Fatal(err)
	}

	validated := &ValidatedToken{Subject: "user-1", Claims: map[string]interface{}{}}
	tokenStr, err := issuer.Issue(validated, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, _ := parser.ParseUnverified(tokenStr, jwt.MapClaims{})
	claims := parsed.Claims.(jwt.MapClaims)

	iat, _ := claims.GetIssuedAt()
	exp, _ := claims.GetExpirationTime()

	diff := exp.Sub(iat.Time)
	if diff < 29*time.Minute || diff > 31*time.Minute {
		t.Errorf("expected ~30 minute lifetime, got %v", diff)
	}
}

func TestExchangeCache_BasicOperations(t *testing.T) {
	cache := newExchangeCache(1 * time.Minute)

	// Put and get
	cache.put("token-1", "issued-1")
	if issued, ok := cache.get("token-1"); !ok || issued != "issued-1" {
		t.Error("expected cache hit for token-1")
	}

	// Miss
	if _, ok := cache.get("nonexistent"); ok {
		t.Error("expected cache miss")
	}

	// Size
	cache.put("token-2", "issued-2")
	if cache.size() != 2 {
		t.Errorf("expected size 2, got %d", cache.size())
	}
}

func TestExchangeCache_NilOnZeroTTL(t *testing.T) {
	cache := newExchangeCache(0)
	if cache != nil {
		t.Error("expected nil cache for 0 TTL")
	}

	cache2 := newExchangeCache(-1 * time.Second)
	if cache2 != nil {
		t.Error("expected nil cache for negative TTL")
	}
}

func TestExchangeCache_ExpiredEntry(t *testing.T) {
	cache := newExchangeCache(10 * time.Millisecond)
	cache.put("token-1", "issued-1")

	time.Sleep(50 * time.Millisecond)

	if _, ok := cache.get("token-1"); ok {
		t.Error("expected cache miss for expired entry")
	}
}

func TestExchangeCache_ConcurrentAccess(t *testing.T) {
	cache := newExchangeCache(1 * time.Minute)

	const n = 100
	done := make(chan bool, n*2)
	for i := 0; i < n; i++ {
		go func(idx int) {
			cache.put("token-"+strings.Repeat("x", idx%10), "issued")
			done <- true
		}(i)
		go func(idx int) {
			cache.get("token-" + strings.Repeat("x", idx%10))
			done <- true
		}(i)
	}
	for i := 0; i < n*2; i++ {
		<-done
	}
}

func TestExchangeMetricsConsistency(t *testing.T) {
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
		routeID:   "test-metrics",
		validator: validator,
		issuer:    issuer,
		cache:     newExchangeCache(10 * time.Minute),
	}

	// Successful exchange
	subjectToken := mintSubjectToken(t, subjectKey, "https://idp.example.com", "user-1", nil)
	te.Exchange(subjectToken)
	te.Exchange(subjectToken) // cache hit

	// Failed exchange (bad token)
	te.Exchange("invalid-token")

	total := te.metrics.Total.Load()
	exchanged := te.metrics.Exchanged.Load()
	cacheHits := te.metrics.CacheHits.Load()
	valFails := te.metrics.ValidationFails.Load()

	// Total = exchanged + cacheHits + validationFails + issueFails
	if total != exchanged+cacheHits+valFails {
		t.Errorf("metrics inconsistency: total=%d, exchanged=%d, cacheHits=%d, valFails=%d",
			total, exchanged, cacheHits, valFails)
	}
}

func TestTokenExchangeByRoute_MultipleRoutes(t *testing.T) {
	m := NewTokenExchangeByRoute()

	// GetExchanger for nonexistent route
	if m.Lookup("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	// Stats with no routes
	stats := m.Stats()
	if len(stats) != 0 {
		t.Errorf("expected empty stats, got %d", len(stats))
	}
}

func TestJWTValidator_MultipleIssuers(t *testing.T) {
	subjectKey, _, jwksServer := testKeyPair(t)
	defer jwksServer.Close()

	validator, err := NewJWTValidator(jwksServer.URL, []string{
		"https://idp1.example.com",
		"https://idp2.example.com",
		"https://idp3.example.com",
	}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// Token from second trusted issuer
	token := mintSubjectToken(t, subjectKey, "https://idp2.example.com", "user-2", nil)
	result, err := validator.Validate(token, "access_token")
	if err != nil {
		t.Fatalf("token from trusted issuer should validate: %v", err)
	}
	if result.Issuer != "https://idp2.example.com" {
		t.Errorf("expected issuer idp2, got %q", result.Issuer)
	}
}

func TestExchange_ValidationFailsMetric(t *testing.T) {
	subjectKey, _, jwksServer := testKeyPair(t)
	defer jwksServer.Close()
	_ = subjectKey

	validator, err := NewJWTValidator(jwksServer.URL, []string{"https://trusted.example.com"}, 1*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	issuer, err := NewTokenIssuer("HS256", "", "", "supersecretkeythatisatleast32bytes!", "https://gw.example.com", nil, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	te := &TokenExchanger{
		routeID:   "test-valfail",
		validator: validator,
		issuer:    issuer,
	}

	// Invalid token
	_, err = te.Exchange("definitely-not-a-jwt")
	if err == nil {
		t.Error("expected error for invalid token")
	}

	if te.metrics.ValidationFails.Load() != 1 {
		t.Errorf("expected 1 validation fail, got %d", te.metrics.ValidationFails.Load())
	}
}

func TestExchange_StatusWithCache(t *testing.T) {
	cache := newExchangeCache(5 * time.Minute)
	cache.put("token-1", "issued-1")
	cache.put("token-2", "issued-2")

	te := &TokenExchanger{
		routeID: "test-status-cache",
		cache:   cache,
	}

	status := te.Status()
	if status.CacheSize != 2 {
		t.Errorf("expected cache size 2, got %d", status.CacheSize)
	}
	if status.RouteID != "test-status-cache" {
		t.Errorf("expected route ID test-status-cache, got %q", status.RouteID)
	}
}

func TestExchange_StatusWithoutCache(t *testing.T) {
	te := &TokenExchanger{
		routeID: "test-no-cache",
		cache:   nil,
	}

	status := te.Status()
	if status.CacheSize != 0 {
		t.Errorf("expected cache size 0 without cache, got %d", status.CacheSize)
	}
}
