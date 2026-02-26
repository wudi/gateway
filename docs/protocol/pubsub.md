---
title: "Go CDK Pub/Sub Backend"
sidebar_position: 12
---

The runway can publish and subscribe to messages using the [Go Cloud Development Kit (Go CDK)](https://gocloud.dev/) Pub/Sub abstraction. This provides a portable interface to multiple message brokers including GCP Pub/Sub, AWS SNS/SQS, Azure Service Bus, NATS, Kafka, and in-memory for testing.

## Overview

When Pub/Sub is enabled on a route, the handler replaces the standard HTTP reverse proxy as the innermost handler. HTTP methods determine the operation:

- **POST/PUT** requests publish the request body as a message to the configured topic.
- **GET** (and other methods) receive a single message from the configured subscription.

## Configuration

```yaml
routes:
  - id: events-publish
    path: /events/publish
    pubsub:
      enabled: true
      publish_url: "gcppubsub://my-project/my-topic"
  - id: events-subscribe
    path: /events/subscribe
    pubsub:
      enabled: true
      subscription_url: "gcppubsub://my-project/my-subscription"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable Pub/Sub backend for this route |
| `subscription_url` | string | - | Go CDK subscription URL for receiving messages |
| `publish_url` | string | - | Go CDK topic URL for publishing messages |

At least one of `subscription_url` or `publish_url` must be provided.

## How It Works

### Publishing (POST/PUT)

The request body is sent as a Pub/Sub message. On success, the handler returns 202 Accepted:

```json
{
  "status": "published"
}
```

If `publish_url` is not configured and a POST/PUT is received, the handler returns 502.

### Subscribing (GET)

A single message is received from the subscription and automatically acknowledged. The handler returns 200 with the message body. If no message is available within the timeout (5 seconds), the handler returns 502.

If `subscription_url` is not configured and a GET is received, the handler returns 502.

## Supported Providers

The Go CDK URL scheme determines the provider:

| Scheme | Provider | Example URL |
|--------|----------|-------------|
| `gcppubsub://` | Google Cloud Pub/Sub | `gcppubsub://project/topic` |
| `awssqs://` | AWS SQS | `awssqs://queue-url?region=us-east-1` |
| `awssns://` | AWS SNS | `awssns://topic-arn?region=us-east-1` |
| `azuresb://` | Azure Service Bus | `azuresb://topic` |
| `nats://` | NATS | `nats://subject` |
| `kafka://` | Kafka | `kafka://group?topic=my-topic` |
| `mem://` | In-memory (testing) | `mem://topic` |

See the [Go CDK Pub/Sub documentation](https://gocloud.dev/howto/pubsub/) for full URL formats and authentication setup.

## Mutual Exclusions

Pub/Sub replaces the proxy as the innermost handler. It is mutually exclusive with:

- `backends`, `service`, `upstream` (standard proxy targets)
- `echo`, `static`, `fastcgi`, `sequential`, `aggregate`
- `lambda`, `amqp`

All upstream middleware (auth, rate limiting, WAF, etc.) still applies to Pub/Sub routes.

## Admin API

```
GET /pubsub
```

Returns per-route Pub/Sub stats:
```json
{
  "events-publish": {
    "publish_url": "gcppubsub://my-project/my-topic",
    "subscription_url": "",
    "total_requests": 2000,
    "total_errors": 3,
    "published": 1997,
    "consumed": 0
  }
}
```

## Validation

- At least one of `publish_url` or `subscription_url` is required when enabled
- Pub/Sub is mutually exclusive with other innermost handlers (backends, static, echo, fastcgi, sequential, aggregate, lambda, amqp)

## Example: Full Publish/Subscribe Bridge

```yaml
routes:
  - id: publish
    path: /messages
    methods: [POST]
    pubsub:
      enabled: true
      publish_url: "mem://messages"
  - id: subscribe
    path: /messages
    methods: [GET]
    pubsub:
      enabled: true
      subscription_url: "mem://messages"
```
