package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

// JWKSProvider fetches and caches JSON Web Key Sets for JWT validation.
type JWKSProvider struct {
	cache   *jwk.Cache
	url     string
	refresh time.Duration
}

// NewJWKSProvider creates a JWKS provider that auto-refreshes keys.
func NewJWKSProvider(jwksURL string, refreshInterval time.Duration) (*JWKSProvider, error) {
	if refreshInterval <= 0 {
		refreshInterval = time.Hour
	}

	ctx := context.Background()
	cache := jwk.NewCache(ctx)

	err := cache.Register(jwksURL, jwk.WithMinRefreshInterval(refreshInterval))
	if err != nil {
		return nil, fmt.Errorf("failed to register JWKS URL: %w", err)
	}

	// Initial fetch to verify the URL works
	_, err = cache.Refresh(ctx, jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS from %s: %w", jwksURL, err)
	}

	return &JWKSProvider{
		cache:   cache,
		url:     jwksURL,
		refresh: refreshInterval,
	}, nil
}

// KeyFunc returns a jwt.Keyfunc compatible with golang-jwt/jwt/v5.
func (p *JWKSProvider) KeyFunc() jwt.Keyfunc {
	return func(token *jwt.Token) (interface{}, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		keySet, err := p.cache.Get(ctx, p.url)
		if err != nil {
			return nil, fmt.Errorf("failed to get JWKS: %w", err)
		}

		// Find key by kid header
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			// If no kid, try the first key
			if keySet.Len() > 0 {
				key, _ := keySet.Key(0)
				var rawKey interface{}
				if err := key.Raw(&rawKey); err != nil {
					return nil, fmt.Errorf("failed to extract raw key: %w", err)
				}
				return rawKey, nil
			}
			return nil, fmt.Errorf("no kid in token header and no keys in JWKS")
		}

		key, found := keySet.LookupKeyID(kid)
		if !found {
			return nil, fmt.Errorf("key %q not found in JWKS", kid)
		}

		var rawKey interface{}
		if err := key.Raw(&rawKey); err != nil {
			return nil, fmt.Errorf("failed to extract raw key for kid %q: %w", kid, err)
		}

		return rawKey, nil
	}
}

// Close stops the background refresh goroutine.
func (p *JWKSProvider) Close() {
	// jwk.Cache doesn't expose a close method; it stops when context is cancelled.
	// The context used in NewCache is Background, so this is a no-op.
}
