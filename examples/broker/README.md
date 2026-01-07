# NATS JetStream Broker Examples

This directory contains example YAML files for the NATS JetStream Broker.

## Prerequisites

1. Install Knative Eventing
2. Install NATS JetStream
3. Install the NATS JetStream Broker components

## Table of Contents

- [Broker Configuration](#broker-configuration)
- [Trigger Filter Examples](#trigger-filter-examples)
- [Quick Start](#quick-start)

## Broker Configuration

### Basic Broker

| File | Description |
|------|-------------|
| [broker.yaml](broker.yaml) | Simple broker with default settings |
| [broker-with-delivery.yaml](broker-with-delivery.yaml) | Broker with retry and dead letter sink |

### Stream Configuration

| File | Description |
|------|-------------|
| [broker-with-stream-config.yaml](broker-with-stream-config.yaml) | Custom JetStream stream settings |
| [broker-memory-storage.yaml](broker-memory-storage.yaml) | High-throughput broker using memory storage |
| [broker-work-queue.yaml](broker-work-queue.yaml) | Work queue pattern with Work retention policy |
| [broker-consumer-tuning.yaml](broker-consumer-tuning.yaml) | Consumer fetch batch size and timeout tuning |

### Production Configuration

| File | Description |
|------|-------------|
| [broker-with-resources.yaml](broker-with-resources.yaml) | HA broker with resource limits and replicas |
| [broker-config-configmap.yaml](broker-config-configmap.yaml) | Cluster/namespace default configuration |
| [nats-config.yaml](nats-config.yaml) | NATS connection configuration |

## Trigger Filter Examples

### Basic Triggers

| File | Description |
|------|-------------|
| [trigger-no-filter.yaml](trigger-no-filter.yaml) | Receives all events (no filtering) |
| [trigger-legacy-filter.yaml](trigger-legacy-filter.yaml) | Legacy attributes filter |

### New Subscriptions API Filters

| File | Description |
|------|-------------|
| [trigger-exact-filter.yaml](trigger-exact-filter.yaml) | Exact match filter |
| [trigger-prefix-filter.yaml](trigger-prefix-filter.yaml) | Prefix match filter |
| [trigger-suffix-filter.yaml](trigger-suffix-filter.yaml) | Suffix match filter |
| [trigger-all-filter.yaml](trigger-all-filter.yaml) | All (AND) filter |
| [trigger-any-filter.yaml](trigger-any-filter.yaml) | Any (OR) filter |
| [trigger-not-filter.yaml](trigger-not-filter.yaml) | Not (negation) filter |
| [trigger-cesql-filter.yaml](trigger-cesql-filter.yaml) | CloudEvents SQL filter |
| [trigger-complex-filter.yaml](trigger-complex-filter.yaml) | Complex nested filters |

### Advanced Triggers

| File | Description |
|------|-------------|
| [trigger-with-dls.yaml](trigger-with-dls.yaml) | Trigger with dead letter sink |
| [trigger-filter-priority.yaml](trigger-filter-priority.yaml) | Filter priority demonstration |

## Quick Start

### 1. Configure NATS Connection (optional)

If your NATS server is not at the default location, configure the connection:

```bash
kubectl apply -f nats-config.yaml
```

### 2. Set Cluster Defaults (optional)

Apply default configuration for all brokers:

```bash
kubectl apply -f broker-config-configmap.yaml
```

### 3. Create a Broker

```bash
# Simple broker
kubectl apply -f broker.yaml

# Or production broker with HA
kubectl apply -f broker-with-resources.yaml
```

### 4. Create a Trigger

```bash
# Simple trigger with exact match filter
kubectl apply -f trigger-exact-filter.yaml
```

### 5. Create a Subscriber Service

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: event-display
spec:
  selector:
    app: event-display
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: event-display
spec:
  replicas: 1
  selector:
    matchLabels:
      app: event-display
  template:
    metadata:
      labels:
        app: event-display
    spec:
      containers:
        - name: event-display
          image: gcr.io/knative-releases/knative.dev/eventing/cmd/event_display
          ports:
            - containerPort: 8080
EOF
```

### 6. Send a Test Event

```bash
kubectl run curl --image=curlimages/curl --rm -it --restart=Never -- \
  -X POST \
  -H "Content-Type: application/cloudevents+json" \
  -d '{
    "specversion": "1.0",
    "type": "com.example.order.created",
    "source": "/orders",
    "id": "1234",
    "data": {"orderId": "12345"}
  }' \
  http://broker-ingress.knative-eventing.svc.cluster.local/default/example-broker
```

## Configuration Reference

### Stream Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `retention` | string | `Limits` | Retention policy: `Limits`, `Interest`, `Work` |
| `storage` | string | `File` | Storage type: `File`, `Memory` |
| `replicas` | int | `1` | Number of stream replicas (clustered NATS) |
| `maxMsgs` | int64 | `0` | Maximum messages (0 = unlimited) |
| `maxBytes` | int64 | `0` | Maximum bytes (0 = unlimited) |
| `maxAge` | duration | `0` | Maximum message age (e.g., `24h`, `168h`) |
| `maxMsgSize` | int32 | `0` | Maximum message size in bytes |
| `discard` | string | `Old` | Discard policy: `Old`, `New` |
| `duplicateWindow` | duration | `0` | Duplicate detection window |

### Consumer Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `deliverPolicy` | string | `New` | `All`, `Last`, `New`, `ByStartSequence`, `ByStartTime` |
| `replayPolicy` | string | `Instant` | `Instant`, `Original` |
| `ackWait` | duration | `30s` | Acknowledgement timeout |
| `maxDeliver` | int | `3` | Maximum delivery attempts |
| `maxAckPending` | int | `0` | Maximum unacknowledged messages |
| `filterSubject` | string | `""` | Subject filter pattern |

### Deployment Template Options

| Option | Type | Description |
|--------|------|-------------|
| `replicas` | int32 | Number of replicas |
| `resources` | ResourceRequirements | CPU/memory requests and limits |
| `nodeSelector` | map[string]string | Node selector labels |
| `affinity` | Affinity | Pod affinity rules |
| `annotations` | map[string]string | Deployment annotations |
| `labels` | map[string]string | Deployment labels |
| `podAnnotations` | map[string]string | Pod annotations |
| `podLabels` | map[string]string | Pod labels |
| `env` | []EnvVar | Additional environment variables |

### Filter Environment Variables

These environment variables can be set in the filter deployment template:

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `CONSUMER_FETCH_BATCH_SIZE` | int | `10` | Number of messages to fetch per batch |
| `CONSUMER_FETCH_TIMEOUT` | duration | `500ms` | Timeout for fetch operations |

**Tuning Guidelines:**

| Scenario | Batch Size | Timeout | Notes |
|----------|------------|---------|-------|
| High Throughput | 50-100 | 1-2s | Maximize messages per fetch |
| Low Latency | 1-5 | 100ms | Process messages immediately |
| Balanced | 10-20 | 500ms | Default, good for most cases |

### Filter Types Summary

| Filter Type | Description | Example |
|-------------|-------------|---------|
| `exact` | Exact string match | `exact: {type: "foo"}` |
| `prefix` | String starts with | `prefix: {type: "com.example."}` |
| `suffix` | String ends with | `suffix: {type: ".created"}` |
| `all` | All filters must match (AND) | `all: [{exact: ...}, {prefix: ...}]` |
| `any` | Any filter must match (OR) | `any: [{exact: ...}, {exact: ...}]` |
| `not` | Negation | `not: {exact: {type: "foo"}}` |
| `cesql` | CloudEvents SQL expression | `cesql: "type LIKE 'foo%'"` |

## Filter Priority

When both `filters` (new) and `filter` (legacy) are specified on a trigger:

1. **`filters`** (new Subscriptions API) takes priority
2. **`filter`** (legacy attributes) is ignored

This allows gradual migration from legacy to new filters.

## Best Practices

### High Availability

- Use `replicas: 3` for stream (requires clustered NATS)
- Use `replicas: 3` for ingress and filter deployments
- Configure appropriate resource limits

### High Throughput

- Use `storage: Memory` for non-critical events
- Use `retention: Interest` to auto-delete consumed messages
- Increase `maxMsgs` and `maxBytes` limits

### Durability

- Use `storage: File` for message persistence
- Use `retention: Limits` with appropriate limits
- Configure `maxAge` based on your SLAs

### Work Queue Pattern

- Use `retention: Work` for exactly-once processing
- Configure `maxDeliver` for retry limits
- Set up dead letter sink for failed messages
