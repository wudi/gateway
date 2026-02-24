---
title: "Consumer Groups"
sidebar_position: 5
---

Consumer groups define named tiers of API consumers with associated resource policies. Groups are resolved from authenticated identity claims or roles and made available in the request context for use by other middleware (rate limiting, quotas, priority).

## Configuration

Global:

```yaml
consumer_groups:
  enabled: true
  groups:
    free:
      rate_limit: 100
      quota: 10000
      priority: 3
      metadata:
        plan: "free"
        support: "community"
    pro:
      rate_limit: 1000
      quota: 100000
      priority: 7
      metadata:
        plan: "pro"
        support: "email"
    enterprise:
      rate_limit: 10000
      quota: 1000000
      priority: 10
      metadata:
        plan: "enterprise"
        support: "dedicated"
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `consumer_groups.enabled` | bool | false | Enable consumer groups |
| `consumer_groups.groups` | map | -- | Named group definitions |

### Per-Group Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rate_limit` | int | 0 | Suggested rate limit (requests/second) |
| `quota` | int64 | 0 | Suggested quota (requests/period) |
| `priority` | int | 0 | Priority level (1-10) |
| `metadata` | map[string]string | -- | Arbitrary key-value metadata |

## Group Resolution

Consumer groups are resolved from the authenticated identity associated with the request. The middleware checks the `Identity.Claims` and `Identity.Roles` fields to determine group membership. The resolved group is stored in the request context and available to downstream middleware.

## Integration with Other Middleware

Consumer group data is informational and can be used by:
- **Rate limiting** -- to select tier-appropriate limits
- **Quota enforcement** -- to apply per-group quotas
- **Priority admission** -- to assign priority levels based on group

## Admin Endpoint

`GET /consumer-groups` returns consumer group configuration and resolution statistics.

```bash
curl http://localhost:8081/consumer-groups
```

See [Configuration Reference](../reference/configuration-reference.md#consumer-groups-global) for field details.
