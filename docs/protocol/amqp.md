---
title: "AMQP/RabbitMQ Backend"
sidebar_position: 11
---

The runway can publish and consume messages from AMQP/RabbitMQ queues, translating HTTP requests into AMQP operations. This enables HTTP-to-message-queue bridging without requiring application-level AMQP client code.

## Overview

When AMQP is enabled on a route, the AMQP handler replaces the standard HTTP reverse proxy as the innermost handler. HTTP methods determine the operation:

- **POST/PUT** requests publish the request body as a message to the configured exchange.
- **GET** (and other methods) consume a single message from the configured queue.

## Configuration

```yaml
routes:
  - id: message-publisher
    path: /publish
    amqp:
      enabled: true
      url: "amqp://guest:guest@rabbitmq:5672/"
      producer:
        exchange: events
        routing_key: user.created
  - id: message-consumer
    path: /consume
    amqp:
      enabled: true
      url: "amqp://guest:guest@rabbitmq:5672/"
      consumer:
        queue: events-queue
        auto_ack: true
```

### Top-Level Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable AMQP backend for this route |
| `url` | string | *required* | AMQP connection URL |
| `consumer` | object | - | Consumer (subscribe) configuration |
| `producer` | object | - | Producer (publish) configuration |

### Consumer Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `queue` | string | - | Queue name to consume from |
| `auto_ack` | bool | `false` | Automatically acknowledge messages |

### Producer Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `exchange` | string | - | Exchange to publish to |
| `routing_key` | string | - | Routing key for published messages |

## How It Works

### Publishing (POST/PUT)

The request body is published as an AMQP message with `Content-Type: application/json`. On success, the handler returns 202 Accepted:

```json
{
  "status": "published",
  "exchange": "events",
  "routing_key": "user.created"
}
```

### Consuming (GET)

A single message is fetched from the queue. If a message is available, the handler returns 200 with the message body. If the queue is empty, the handler returns 204 No Content.

### Connection Management

The handler establishes an AMQP connection and channel at startup. The connection is shared across requests with read-lock protection for concurrent access.

## Mutual Exclusions

AMQP replaces the proxy as the innermost handler. It is mutually exclusive with:

- `backends`, `service`, `upstream` (standard proxy targets)
- `echo`, `static`, `fastcgi`, `sequential`, `aggregate`
- `lambda`, `pubsub`

All upstream middleware (auth, rate limiting, WAF, etc.) still applies to AMQP routes.

## Admin API

```
GET /amqp
```

Returns per-route AMQP stats:
```json
{
  "message-publisher": {
    "url": "amqp://guest:guest@rabbitmq:5672/",
    "total_requests": 3000,
    "total_errors": 5,
    "published": 2995,
    "consumed": 0
  }
}
```

## Validation

- `url` is required when enabled
- AMQP is mutually exclusive with other innermost handlers (backends, static, echo, fastcgi, sequential, aggregate, lambda, pubsub)

## Example: Bidirectional Queue Bridge

```yaml
routes:
  - id: enqueue
    path: /api/jobs
    methods: [POST]
    amqp:
      enabled: true
      url: "amqp://user:pass@rabbitmq:5672/"
      producer:
        exchange: ""
        routing_key: jobs
  - id: dequeue
    path: /api/jobs
    methods: [GET]
    amqp:
      enabled: true
      url: "amqp://user:pass@rabbitmq:5672/"
      consumer:
        queue: jobs
        auto_ack: true
```
