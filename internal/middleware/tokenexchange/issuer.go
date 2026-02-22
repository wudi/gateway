package tokenexchange

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenIssuer mints internal JWTs for exchanged tokens.
type TokenIssuer struct {
	signingKey interface{}     // *rsa.PrivateKey or []byte
	signingAlg jwt.SigningMethod
	issuer     string
	audience   []string
	lifetime   time.Duration
}

// NewTokenIssuer creates a token issuer from config.
func NewTokenIssuer(
	signingAlg string,
	signingKey, signingKeyFile, signingSecret string,
	issuer string,
	audience []string,
	lifetime time.Duration,
) (*TokenIssuer, error) {
	ti := &TokenIssuer{
		issuer:   issuer,
		audience: audience,
		lifetime: lifetime,
	}

	switch signingAlg {
	case "RS256":
		ti.signingAlg = jwt.SigningMethodRS256
		key, err := loadRSAPrivateKey(signingKey, signingKeyFile)
		if err != nil {
			return nil, err
		}
		ti.signingKey = key
	case "RS512":
		ti.signingAlg = jwt.SigningMethodRS512
		key, err := loadRSAPrivateKey(signingKey, signingKeyFile)
		if err != nil {
			return nil, err
		}
		ti.signingKey = key
	case "HS256":
		ti.signingAlg = jwt.SigningMethodHS256
		ti.signingKey = []byte(signingSecret)
	case "HS512":
		ti.signingAlg = jwt.SigningMethodHS512
		ti.signingKey = []byte(signingSecret)
	default:
		return nil, fmt.Errorf("token exchange: unsupported signing algorithm %q", signingAlg)
	}

	return ti, nil
}

// Issue mints a new JWT with claims from the validated subject token.
func (ti *TokenIssuer) Issue(validated *ValidatedToken, claimMappings map[string]string, scopes []string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": ti.issuer,
		"sub": validated.Subject,
		"iat": now.Unix(),
		"exp": now.Add(ti.lifetime).Unix(),
	}

	if len(ti.audience) > 0 {
		claims["aud"] = ti.audience
	}

	if len(scopes) > 0 {
		claims["scope"] = scopes
	}

	// Apply claim mappings: map subject claims to issued token claims
	for srcClaim, dstClaim := range claimMappings {
		if val, ok := validated.Claims[srcClaim]; ok {
			claims[dstClaim] = val
		}
	}

	token := jwt.NewWithClaims(ti.signingAlg, claims)
	return token.SignedString(ti.signingKey)
}

func loadRSAPrivateKey(inline, file string) (*rsa.PrivateKey, error) {
	var pemData []byte
	if inline != "" {
		pemData = []byte(inline)
	} else if file != "" {
		var err error
		pemData, err = os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("token exchange: reading signing key file: %w", err)
		}
	} else {
		return nil, fmt.Errorf("token exchange: signing_key or signing_key_file required for RSA algorithms")
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("token exchange: failed to parse PEM block from signing key")
	}

	// Try PKCS8, then PKCS1
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		rsaKey, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("token exchange: parsing signing key: not PKCS8 (%v) or PKCS1 (%v)", err, err2)
		}
		return rsaKey, nil
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("token exchange: signing key is not RSA (got %T)", key)
	}
	return rsaKey, nil
}
