package tokenexchange

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/wudi/gateway/internal/middleware/auth"
)

// ValidatedToken is the result of validating a subject token.
type ValidatedToken struct {
	Subject  string
	Claims   map[string]interface{}
	Issuer   string
	Audience []string
}

// SubjectValidator validates incoming subject tokens.
type SubjectValidator interface {
	Validate(token, tokenType string) (*ValidatedToken, error)
}

// JWTValidator validates JWTs locally using JWKS.
type JWTValidator struct {
	jwks           *auth.JWKSProvider
	trustedIssuers map[string]bool
}

// NewJWTValidator creates a JWT validator using a JWKS URL.
func NewJWTValidator(jwksURL string, trustedIssuers []string, refreshInterval time.Duration) (*JWTValidator, error) {
	provider, err := auth.NewJWKSProvider(jwksURL, refreshInterval)
	if err != nil {
		return nil, fmt.Errorf("token exchange: JWKS setup failed: %w", err)
	}

	issuers := make(map[string]bool, len(trustedIssuers))
	for _, iss := range trustedIssuers {
		issuers[iss] = true
	}

	return &JWTValidator{
		jwks:           provider,
		trustedIssuers: issuers,
	}, nil
}

// Validate validates a JWT subject token.
func (v *JWTValidator) Validate(tokenStr, _ string) (*ValidatedToken, error) {
	parser := jwt.NewParser(jwt.WithExpirationRequired())
	token, err := parser.Parse(tokenStr, v.jwks.KeyFunc())
	if err != nil {
		return nil, fmt.Errorf("invalid subject token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type")
	}

	// Check trusted issuers
	iss, _ := claims.GetIssuer()
	if len(v.trustedIssuers) > 0 && !v.trustedIssuers[iss] {
		return nil, fmt.Errorf("untrusted issuer: %s", iss)
	}

	sub, _ := claims.GetSubject()
	aud, _ := claims.GetAudience()

	// Convert claims to map[string]interface{}
	claimsMap := make(map[string]interface{})
	for k, val := range claims {
		claimsMap[k] = val
	}

	return &ValidatedToken{
		Subject:  sub,
		Claims:   claimsMap,
		Issuer:   iss,
		Audience: aud,
	}, nil
}

// IntrospectionValidator validates tokens via OAuth2 introspection endpoint.
type IntrospectionValidator struct {
	introspectionURL string
	clientID         string
	clientSecret     string
	httpClient       *http.Client
}

// NewIntrospectionValidator creates an introspection-based validator.
func NewIntrospectionValidator(introspectionURL, clientID, clientSecret string) *IntrospectionValidator {
	return &IntrospectionValidator{
		introspectionURL: introspectionURL,
		clientID:         clientID,
		clientSecret:     clientSecret,
		httpClient:       &http.Client{Timeout: 10 * time.Second},
	}
}

// Validate validates a token via introspection.
func (v *IntrospectionValidator) Validate(tokenStr, tokenType string) (*ValidatedToken, error) {
	data := url.Values{
		"token": {tokenStr},
	}
	if tokenType != "" {
		data.Set("token_type_hint", tokenType)
	}

	req, err := http.NewRequest(http.MethodPost, v.introspectionURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("introspection request build failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(v.clientID, v.clientSecret)

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspection request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("introspection returned status %d", resp.StatusCode)
	}

	var result struct {
		Active   bool     `json:"active"`
		Sub      string   `json:"sub"`
		Iss      string   `json:"iss"`
		Aud      jsonAud  `json:"aud"`
		Scope    string   `json:"scope"`
		ClientID string   `json:"client_id"`
		Exp      int64    `json:"exp"`
		Email    string   `json:"email"`
		Groups   []string `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("introspection response decode failed: %w", err)
	}

	if !result.Active {
		return nil, fmt.Errorf("token is not active")
	}

	claims := map[string]interface{}{
		"sub":       result.Sub,
		"iss":       result.Iss,
		"scope":     result.Scope,
		"client_id": result.ClientID,
	}
	if result.Email != "" {
		claims["email"] = result.Email
	}
	if len(result.Groups) > 0 {
		claims["groups"] = result.Groups
	}

	return &ValidatedToken{
		Subject:  result.Sub,
		Claims:   claims,
		Issuer:   result.Iss,
		Audience: result.Aud,
	}, nil
}

// jsonAud handles audience that can be string or []string in JSON.
type jsonAud []string

func (a *jsonAud) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*a = []string{s}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*a = arr
	return nil
}
