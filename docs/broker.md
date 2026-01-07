# NATS JetStream Broker

This document describes the NATS JetStream Broker implementation for Knative Eventing.

## Overview

The NATS JetStream Broker is a Knative Eventing Broker implementation backed by NATS JetStream. It provides:

- **Durable message storage** - Events are persisted in JetStream streams
- **At-least-once delivery** - Events are redelivered until acknowledged
- **Trigger filtering** - Filter events using CloudEvents attributes
- **Dead letter sink support** - Failed events can be sent to a dead letter sink
- **Configurable retry policies** - Configure backoff and retry behavior

## Components

The broker consists of two main components:

### Broker Controller (`natsjs-broker-controller`)

Reconciles `Broker` resources with the `NatsJetStreamBroker` class annotation. Creates:
- JetStream streams for event storage
- Ingress and Filter deployments for the broker

### Filter (`natsjs-broker-filter`)

Handles event delivery to trigger subscribers:
- Consumes events from JetStream streams
- Applies trigger filters to events
- Dispatches matching events to subscriber endpoints
- Handles retries and dead letter sinks

### Ingress (`natsjs-broker-ingress`)

Receives events via HTTP and publishes them to JetStream streams.

## Installation

1. Install NATS JetStream:

```shell
kubectl apply -f ./config/broker/nats.yaml
```

2. Install the broker controller and data plane:

```shell
ko apply -f ./config/broker
```

## Usage

### Creating a Broker

```yaml
apiVersion: eventing.knative.dev/v1
kind: Broker
metadata:
  name: my-broker
  namespace: default
  annotations:
    eventing.knative.dev/broker.class: NatsJetStreamBroker
spec:
  delivery:
    retry: 3
    backoffPolicy: exponential
    backoffDelay: PT1S
```

### Creating Triggers

Triggers subscribe to events from the broker and deliver them to subscribers.

#### Basic Trigger (no filter - receives all events)

```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: my-trigger
  namespace: default
spec:
  broker: my-broker
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: my-service
```

#### Trigger with Legacy Attributes Filter

The legacy filter uses the `filter.attributes` field to match CloudEvents attributes:

```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: order-created-trigger
  namespace: default
spec:
  broker: my-broker
  filter:
    attributes:
      type: com.example.order.created
      source: /orders
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: order-processor
```

#### Trigger with New Subscriptions API Filters

The new `filters` field (plural) supports more advanced filtering using the CloudEvents Subscriptions API:

**Exact Match:**
```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: exact-filter-trigger
spec:
  broker: my-broker
  filters:
    - exact:
        type: com.example.order.created
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: order-processor
```

**Prefix Match:**
```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: prefix-filter-trigger
spec:
  broker: my-broker
  filters:
    - prefix:
        type: com.example.order.
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: order-processor
```

**Suffix Match:**
```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: suffix-filter-trigger
spec:
  broker: my-broker
  filters:
    - suffix:
        type: .created
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: event-processor
```

**All (AND logic):**
```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: all-filter-trigger
spec:
  broker: my-broker
  filters:
    - all:
        - exact:
            type: com.example.order.created
        - exact:
            source: /orders/premium
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: premium-order-processor
```

**Any (OR logic):**
```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: any-filter-trigger
spec:
  broker: my-broker
  filters:
    - any:
        - exact:
            type: com.example.order.created
        - exact:
            type: com.example.order.updated
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: order-processor
```

**Not (negation):**
```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: not-filter-trigger
spec:
  broker: my-broker
  filters:
    - not:
        exact:
          type: com.example.order.deleted
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: order-processor
```

**CESQL (CloudEvents SQL):**
```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: cesql-filter-trigger
spec:
  broker: my-broker
  filters:
    - cesql: "type LIKE 'com.example.order.%' AND source = '/orders'"
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: order-processor
```

### Filter Priority

When both `filters` (new) and `filter` (legacy) are specified, the new `filters` field takes priority:

```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: priority-example
spec:
  broker: my-broker
  # New filters - USED (takes priority)
  filters:
    - exact:
        type: com.example.new.type
  # Legacy filter - IGNORED
  filter:
    attributes:
      type: com.example.legacy.type
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: my-service
```

### Dead Letter Sink

Configure a dead letter sink to receive events that fail delivery after all retries:

```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: trigger-with-dls
spec:
  broker: my-broker
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: my-service
  delivery:
    deadLetterSink:
      ref:
        apiVersion: v1
        kind: Service
        name: dead-letter-service
    retry: 3
    backoffPolicy: exponential
    backoffDelay: PT1S
```

## Configuration

### Filter Environment Variables

The filter component can be configured using environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `NATS_URL` | NATS server URL | Required |
| `POD_NAME` | Pod name for identification | Required |
| `CONTAINER_NAME` | Container name for identification | Required |
| `CONSUMER_FETCH_BATCH_SIZE` | Number of messages to fetch per batch | 10 |
| `CONSUMER_FETCH_TIMEOUT` | Timeout for fetch operations | 500ms |

### Configuring Consumer Fetch via Broker Annotation

You can configure the consumer fetch settings per-broker using the broker annotation:

```yaml
apiVersion: eventing.knative.dev/v1
kind: Broker
metadata:
  name: my-broker
  annotations:
    eventing.knative.dev/broker.class: NatsJetStreamBroker
    natsjetstream.eventing.knative.dev/config: |
      {
        "filter": {
          "replicas": 2,
          "env": [
            {"name": "CONSUMER_FETCH_BATCH_SIZE", "value": "50"},
            {"name": "CONSUMER_FETCH_TIMEOUT", "value": "1s"}
          ]
        }
      }
```

### Tuning Consumer Performance

**High Throughput** - Increase batch size to fetch more messages per request:

```yaml
natsjetstream.eventing.knative.dev/config: |
  {
    "filter": {
      "env": [
        {"name": "CONSUMER_FETCH_BATCH_SIZE", "value": "100"},
        {"name": "CONSUMER_FETCH_TIMEOUT", "value": "2s"}
      ]
    }
  }
```

**Low Latency** - Decrease batch size for immediate processing:

```yaml
natsjetstream.eventing.knative.dev/config: |
  {
    "filter": {
      "env": [
        {"name": "CONSUMER_FETCH_BATCH_SIZE", "value": "1"},
        {"name": "CONSUMER_FETCH_TIMEOUT", "value": "100ms"}
      ]
    }
  }
```

**Tuning Guidelines:**

| Scenario | Batch Size | Timeout | Use Case |
|----------|------------|---------|----------|
| High Throughput | 50-100 | 1-2s | Batch processing, analytics |
| Low Latency | 1-5 | 100ms | Real-time notifications |
| Balanced | 10-20 | 500ms | General purpose (default) |

## Architecture

```
                                    ┌─────────────────────┐
                                    │   Event Producer    │
                                    └──────────┬──────────┘
                                               │
                                               ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                            Broker                                         │
│  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────────┐   │
│  │     Ingress     │───▶│ JetStream Stream │◀───│       Filter        │   │
│  │  (HTTP Server)  │    │   (Persistence)  │    │ (Consumer Manager)  │   │
│  └─────────────────┘    └─────────────────┘    └─────────┬───────────┘   │
└──────────────────────────────────────────────────────────┼───────────────┘
                                                           │
                         ┌─────────────────────────────────┼─────────────────┐
                         │                                 │                 │
                         ▼                                 ▼                 ▼
              ┌─────────────────┐             ┌─────────────────┐   ┌───────────────┐
              │   Trigger A     │             │   Trigger B     │   │   Trigger C   │
              │ (type=created)  │             │ (type=updated)  │   │ (no filter)   │
              └────────┬────────┘             └────────┬────────┘   └───────┬───────┘
                       │                               │                    │
                       ▼                               ▼                    ▼
              ┌─────────────────┐             ┌─────────────────┐   ┌───────────────┐
              │   Subscriber A  │             │   Subscriber B  │   │  Subscriber C │
              └─────────────────┘             └─────────────────┘   └───────────────┘
```

## Retry Behavior

When a subscriber returns an error or times out:

1. **5xx errors, 408, 429**: Message is redelivered with backoff
2. **4xx errors (except 408, 429)**: Message is terminated (non-retriable)
3. **Network errors**: Message is redelivered with backoff

After all retries are exhausted, if a dead letter sink is configured, the event is sent there before being acknowledged.

## Troubleshooting

### Events not being delivered

1. Check that the trigger filter matches the event attributes
2. Verify the subscriber service is running and accessible
3. Check filter pod logs: `kubectl logs -l app=natsjs-broker-filter`

### High latency

1. Increase `CONSUMER_FETCH_BATCH_SIZE` for better throughput
2. Check NATS server health and connection

### Events going to dead letter sink

1. Check subscriber logs for errors
2. Verify subscriber endpoint is correct
3. Review retry configuration
