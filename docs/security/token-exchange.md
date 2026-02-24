---
title: "Token Exchange (RFC 8693)"
sidebar_position: 5
---

The gateway implements OAuth2/OIDC Token Exchange (RFC 8693) to act as a Security Token Service (STS) intermediary. This accepts external identity provider tokens and issues internal service tokens, enabling zero-trust architectures where backends only trust gateway-issued tokens.

## Overview

1. Client authenticates with an external IdP and receives a token
2. Client sends request to gateway with `Authorization: Bearer <external-token>`
3. Gateway validates the external token (via JWKS or introspection)
4. Gateway mints a new internal JWT with configurable claims
5. Backend receives the gateway-issued token

## Configuration

### JWT Validation Mode

```yaml
routes:
  - id: partner-api
    path: /api/partner
    auth:
      required: true
      methods: [jwt]
    token_exchange:
      enabled: true
      validation_mode: jwt
      jwks_url: https://partner-idp.example.com/.well-known/jwks.json
      trusted_issuers:
        - https://partner-idp.example.com
      issuer: https://gateway.internal.example.com
      audience: [internal-services]
      scopes: [read, write]
      token_lifetime: 15m
      signing_algorithm: RS256
      signing_key_file: /etc/gateway/exchange-key.pem
      cache_ttl: 14m
      claim_mappings:
        sub: sub
        email: email
        groups: roles
```

### Introspection Validation Mode

```yaml
routes:
  - id: external-api
    path: /api/external
    auth:
      required: true
    token_exchange:
      enabled: true
      validation_mode: introspection
      introspection_url: https://auth.example.com/introspect
      client_id: gateway
      client_secret: ${EXCHANGE_SECRET}
      issuer: https://gateway.internal.example.com
      audience: [internal-services]
      token_lifetime: 15m
      signing_algorithm: HS256
      signing_secret: ${EXCHANGE_SIGNING_SECRET}
```

## Signing Algorithms

| Algorithm | Type | Key Required |
|-----------|------|-------------|
| RS256 | RSA PKCS#1 v1.5 + SHA-256 | `signing_key` or `signing_key_file` |
| RS512 | RSA PKCS#1 v1.5 + SHA-512 | `signing_key` or `signing_key_file` |
| HS256 | HMAC + SHA-256 | `signing_secret` |
| HS512 | HMAC + SHA-512 | `signing_secret` |

## Claim Mappings

The `claim_mappings` field maps claims from the validated subject token to the issued internal token:

```yaml
claim_mappings:
  sub: sub          # subject → subject
  email: email      # email → email
  groups: roles     # groups → roles (renamed)
```

Claims not listed in mappings are not carried over. Standard JWT claims (`iss`, `sub`, `aud`, `iat`, `exp`) are always set by the issuer.

## Caching

Exchange results are cached by SHA-256 of the subject token. Set `cache_ttl` slightly less than `token_lifetime` to avoid serving tokens that expire immediately.

## Middleware Position

Step 6.07 in the middleware chain — after auth validation (6) and token revocation (6.05), before claims propagation (6.15). This ensures:

- The incoming token is validated by the auth middleware first
- Token revocation checks the original token
- Claims propagation operates on the gateway-issued token

## Admin Endpoint

`GET /token-exchange` returns per-route exchange metrics:

```json
{
  "partner-api": {
    "route_id": "partner-api",
    "cache_size": 42,
    "total": 1000,
    "exchanged": 950,
    "cache_hits": 800,
    "validation_fails": 30,
    "issue_fails": 20
  }
}
```
