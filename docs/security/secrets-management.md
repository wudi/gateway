# Secrets Management

The runway supports referencing secrets stored in environment variables or files instead of embedding them as plaintext in YAML configuration. This prevents credentials from appearing in version control, config backups, and admin API responses.

## Secret Reference Syntax

Use `${scheme:reference}` in any string config field to reference a secret:

| Scheme | Syntax | Example |
|--------|--------|---------|
| `env` | `${env:VAR_NAME}` | `${env:JWT_SECRET}` |
| `file` | `${file:/path/to/file}` | `${file:/run/secrets/db_password}` |

**Important:** The entire field value must be a single secret reference. Inline composition like `prefix-${env:X}-suffix` is **not** supported. If you need a composed value (e.g., a database URL), compose it in the environment variable or file itself.

### `${env:VAR}` vs `${VAR}` (Legacy)

| Feature | `${VAR}` (legacy) | `${env:VAR}` (strict) |
|---------|--------------------|-----------------------|
| When expanded | Before YAML parsing (text substitution) | After YAML parsing (struct-level) |
| Missing variable | Silent passthrough (value becomes literal `${VAR}`) | **Startup error** — runway refuses to start |
| Multi-line values | May break YAML (`:`, `#`, newlines are YAML-significant) | Safe — resolved after YAML is parsed |

Use `${env:VAR}` for secrets that **must** be present. Use `${VAR}` only for optional non-sensitive values where silent passthrough is acceptable.

## Environment Variables

```yaml
authentication:
  jwt:
    secret: "${env:JWT_SECRET}"        # errors if JWT_SECRET is not set
  oauth:
    client_secret: "${env:OAUTH_SECRET}"

redis:
  password: "${env:REDIS_PASSWORD}"
```

## File-Based Secrets

File references read the contents of a file at startup. Trailing whitespace and newlines are trimmed. This is ideal for Docker secrets and Kubernetes mounted secrets.

```yaml
# Docker Swarm secrets (mounted at /run/secrets/)
authentication:
  jwt:
    secret: "${file:/run/secrets/jwt_secret}"

# Kubernetes secrets (mounted via volume)
redis:
  password: "${file:/var/run/secrets/redis/password}"

# PEM keys work correctly (multi-line content)
backend_signing:
  private_key: "${file:/etc/runway/signing-key.pem}"
```

### Path Restrictions

By default, the file provider can read any path (config authors are trusted). To restrict readable paths, use `secrets.file.allowed_prefixes`:

```yaml
secrets:
  file:
    allowed_prefixes:
      - /run/secrets/
      - /var/run/secrets/

authentication:
  jwt:
    secret: "${file:/run/secrets/jwt_secret}"     # allowed
    # secret: "${file:/etc/passwd}"               # would fail — not under allowed prefix
```

## Error Behavior

Secret resolution is **strict**:

- Missing environment variable → startup error with field path (e.g., `Authentication.JWT.Secret`)
- Missing file → startup error with file path
- Unknown scheme (e.g., `${vault:...}`) → startup error
- File outside allowed prefixes → startup error

The runway will not start with unresolved secrets. This prevents running with missing credentials.

## Admin API Redaction

The `GET /api/v1/config` admin endpoint automatically redacts all sensitive fields, returning `[REDACTED]` instead of actual values. This applies to all fields tagged with `redact:"true"` in the config struct, including:

- JWT secrets, OAuth client secrets
- API keys, LDAP bind passwords
- Redis passwords, Consul tokens, etcd passwords
- CSRF secrets, signing keys, encryption keys
- Webhook secrets, AI API keys
- SAML session signing keys, token exchange secrets

The control plane → data plane config push is **not** redacted (data planes need real secrets to operate).

## Extensibility

The `SecretProvider` interface enables adding custom backends (e.g., HashiCorp Vault, AWS Secrets Manager):

```go
type SecretProvider interface {
    Scheme() string
    Resolve(ctx context.Context, reference string) (string, error)
}
```

Custom providers can be registered via the runway builder API:

```go
reg := config.NewSecretRegistry()
reg.Register(&EnvProvider{})
reg.Register(&VaultProvider{...})

gw.New(cfg).
    WithConfigPath(path).
    WithDefaults().
    WithLoaderOption(config.WithSecretRegistry(reg)).
    Build()
```

## Security Notes

- Resolved secrets exist in process memory for the lifetime of the runway. This is inherent to any application that uses secrets at runtime.
- File-based secrets are read once at startup (and on config reload). The runway does not watch secret files for changes.
- The `${env:...}` and `${file:...}` providers are stateless and do not cache values.
