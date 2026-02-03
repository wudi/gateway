package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
	"github.com/example/gateway/internal/middleware"
	"github.com/example/gateway/internal/variables"
	"github.com/golang-jwt/jwt/v5"
)

// JWTAuth provides JWT authentication
type JWTAuth struct {
	secret     []byte
	publicKey  *rsa.PublicKey
	issuer     string
	audience   []string
	algorithm  string
	keyFunc    jwt.Keyfunc
}

// NewJWTAuth creates a new JWT authenticator
func NewJWTAuth(cfg config.JWTConfig) (*JWTAuth, error) {
	auth := &JWTAuth{
		issuer:    cfg.Issuer,
		audience:  cfg.Audience,
		algorithm: cfg.Algorithm,
	}

	if auth.algorithm == "" {
		auth.algorithm = "HS256"
	}

	// Set up key based on algorithm
	if strings.HasPrefix(auth.algorithm, "HS") {
		// HMAC algorithms use symmetric secret
		auth.secret = []byte(cfg.Secret)
		auth.keyFunc = func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return auth.secret, nil
		}
	} else if strings.HasPrefix(auth.algorithm, "RS") {
		// RSA algorithms use asymmetric keys
		if cfg.PublicKey != "" {
			block, _ := pem.Decode([]byte(cfg.PublicKey))
			if block == nil {
				return nil, fmt.Errorf("failed to parse PEM block containing public key")
			}

			pub, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse public key: %w", err)
			}

			rsaPub, ok := pub.(*rsa.PublicKey)
			if !ok {
				return nil, fmt.Errorf("public key is not an RSA key")
			}
			auth.publicKey = rsaPub
		}

		auth.keyFunc = func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return auth.publicKey, nil
		}
	}

	return auth, nil
}

// Authenticate verifies the JWT token and returns the identity
func (a *JWTAuth) Authenticate(r *http.Request) (*variables.Identity, error) {
	tokenString := a.extractToken(r)
	if tokenString == "" {
		return nil, errors.ErrUnauthorized.WithDetails("Bearer token not provided")
	}

	// Parse and validate token
	token, err := jwt.Parse(tokenString, a.keyFunc)
	if err != nil {
		return nil, errors.ErrUnauthorized.WithDetails(fmt.Sprintf("Invalid token: %v", err))
	}

	if !token.Valid {
		return nil, errors.ErrUnauthorized.WithDetails("Token is not valid")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.ErrUnauthorized.WithDetails("Invalid token claims")
	}

	// Validate issuer
	if a.issuer != "" {
		iss, _ := claims.GetIssuer()
		if iss != a.issuer {
			return nil, errors.ErrUnauthorized.WithDetails("Invalid token issuer")
		}
	}

	// Validate audience
	if len(a.audience) > 0 {
		aud, _ := claims.GetAudience()
		if !a.containsAudience(aud) {
			return nil, errors.ErrUnauthorized.WithDetails("Invalid token audience")
		}
	}

	// Extract client ID from sub claim
	clientID := ""
	if sub, _ := claims.GetSubject(); sub != "" {
		clientID = sub
	} else if cid, ok := claims["client_id"].(string); ok {
		clientID = cid
	}

	// Convert claims to map[string]interface{}
	claimsMap := make(map[string]interface{})
	for k, v := range claims {
		claimsMap[k] = v
	}

	return &variables.Identity{
		ClientID: clientID,
		AuthType: "jwt",
		Claims:   claimsMap,
	}, nil
}

// extractToken extracts the JWT token from the Authorization header
func (a *JWTAuth) extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	// Remove "Bearer " prefix
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	if strings.HasPrefix(auth, "bearer ") {
		return auth[7:]
	}

	return ""
}

// containsAudience checks if any of the token's audiences match the expected audiences
func (a *JWTAuth) containsAudience(tokenAud []string) bool {
	for _, ta := range tokenAud {
		for _, ea := range a.audience {
			if ta == ea {
				return true
			}
		}
	}
	return false
}

// IsEnabled returns true if JWT auth is configured
func (a *JWTAuth) IsEnabled() bool {
	return len(a.secret) > 0 || a.publicKey != nil
}

// Middleware creates a middleware for JWT authentication
func (a *JWTAuth) Middleware(required bool) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, err := a.Authenticate(r)

			if err != nil {
				if required {
					gatewayErr := err.(*errors.GatewayError)
					w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
					gatewayErr.WriteJSON(w)
					return
				}
				// Not required, continue without identity
				next.ServeHTTP(w, r)
				return
			}

			// Add identity to context
			varCtx := variables.GetFromRequest(r)
			varCtx.Identity = identity
			ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GenerateToken generates a JWT token (for testing purposes)
func (a *JWTAuth) GenerateToken(claims map[string]interface{}) (string, error) {
	mapClaims := jwt.MapClaims{}
	for k, v := range claims {
		mapClaims[k] = v
	}

	var method jwt.SigningMethod
	var key interface{}

	switch a.algorithm {
	case "HS256":
		method = jwt.SigningMethodHS256
		key = a.secret
	case "HS384":
		method = jwt.SigningMethodHS384
		key = a.secret
	case "HS512":
		method = jwt.SigningMethodHS512
		key = a.secret
	default:
		return "", fmt.Errorf("unsupported algorithm for token generation: %s", a.algorithm)
	}

	token := jwt.NewWithClaims(method, mapClaims)
	return token.SignedString(key)
}
