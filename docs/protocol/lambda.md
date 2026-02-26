---
title: "AWS Lambda Backend"
sidebar_position: 13
---

The runway can invoke AWS Lambda functions as backends, translating HTTP requests into Lambda invocations and returning the function response to the client.

## Overview

When Lambda is enabled on a route, the Lambda handler replaces the standard HTTP reverse proxy as the innermost handler. Incoming HTTP requests are converted to an API Gateway-style JSON payload and sent to the configured Lambda function via the AWS SDK.

## Configuration

```yaml
routes:
  - id: serverless-api
    path: /api/compute
    path_prefix: true
    lambda:
      enabled: true
      function_name: my-api-handler
      region: us-west-2
      max_retries: 2
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable Lambda backend for this route |
| `function_name` | string | *required* | AWS Lambda function name or ARN |
| `region` | string | `us-east-1` | AWS region for the Lambda function |
| `max_retries` | int | `0` | Maximum retry attempts for failed invocations |

## How It Works

### Request Translation

The handler converts HTTP requests into a JSON payload matching the API Gateway proxy integration format:

**GET requests:**
```json
{
  "httpMethod": "GET",
  "path": "/api/compute/data",
  "queryParameters": {"key": ["value"]},
  "headers": {"Content-Type": "application/json"},
  "pathParameters": {}
}
```

**POST/PUT/DELETE requests:**
```json
{
  "httpMethod": "POST",
  "path": "/api/compute/data",
  "headers": {"Content-Type": "application/json"},
  "body": "{\"name\": \"example\"}",
  "pathParameters": {}
}
```

### Response Handling

- Successful invocations return 200 with the Lambda function's payload as `application/json`.
- Function errors (non-null `FunctionError`) return 502 with the error payload.
- SDK errors (network failures, permissions) return 502 with an error message.

### AWS Authentication

The handler uses the default AWS SDK credential chain (`aws-sdk-go-v2/config.LoadDefaultConfig`). Credentials are resolved from (in order):

1. Environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`)
2. Shared credentials file (`~/.aws/credentials`)
3. IAM instance profile / ECS task role / IRSA (Kubernetes)

## Mutual Exclusions

Lambda replaces the proxy as the innermost handler. It is mutually exclusive with:

- `backends`, `service`, `upstream` (standard proxy targets)
- `echo`, `static`, `fastcgi`, `sequential`, `aggregate`
- `amqp`, `pubsub`

All upstream middleware (auth, rate limiting, WAF, etc.) still applies to Lambda routes.

## Admin API

```
GET /lambda
```

Returns per-route Lambda stats:
```json
{
  "serverless-api": {
    "function_name": "my-api-handler",
    "total_requests": 5000,
    "total_errors": 12,
    "total_invokes": 5000
  }
}
```

## Validation

- `function_name` is required when enabled
- Lambda is mutually exclusive with other innermost handlers (backends, static, echo, fastcgi, sequential, aggregate, amqp, pubsub)
