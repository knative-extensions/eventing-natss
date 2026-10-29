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

The NATS JetStream Broker has three components:

### Broker controller

The `natsjetstream-broker-controller` Deployment reconciles `Broker` resources
that use the `NatsJetStreamBroker` class and creates JetStream streams for event
storage.

### Filter

For each Broker that has at least one Trigger, the controller creates a
`<broker-name>-broker-filter` Deployment. The filter:

- Consumes events from JetStream streams
- Applies trigger filters to events
- Dispatches matching events to subscriber endpoints
- Handles retries and dead letter sinks

### Ingress

The shared `nats-broker-ingress` Deployment receives events over HTTP and
publishes them to JetStream streams for all NATS JetStream Brokers in the
cluster.
The shared ingress uses a HorizontalPodAutoscaler and requires the Kubernetes
resource metrics API.

## Install the NATS JetStream Broker

Before you install the NATS JetStream Broker, you must have:

- Knative Eventing installed
- [`ko`](https://ko.build/install/) installed

From the root directory of this repository:

1. Install NATS JetStream by running the command:

    ```bash
    kubectl apply -f ./config/mtbroker/natsjsm.yaml
    ```

1. Install the Broker controller and data plane by running the command:

    ```bash
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
    retry: 6
    backoffPolicy: exponential
    backoffDelay: PT1S
    backoffMax: PT4S
    retryAfterMax: PT6S
```

### Delivery retry limits

The Trigger owner uses these fields to keep retry delays bounded when
`my-service` is unavailable. With the example above, the normal exponential
delays calculated by the Broker filter are `1s`, `2s`, `4s`, `4s`, `4s`, and
`4s`.

| Field | Delay it limits | When it applies | Example result |
|-------|-----------------|-----------------|----------------|
| `backoffMax` | The delay calculated from `backoffDelay` and `backoffPolicy` | Every normal retry | `PT4S` stops the exponential sequence at 4 seconds |
| `retryAfterMax` | A delay requested by a subscriber's `Retry-After` response header | HTTP 429 and 503 responses | `PT6S` reduces `Retry-After: 10` to 6 seconds |

For a 429 or 503 response, the Broker uses the larger of the normal backoff and
the capped `Retry-After` value. These fields limit the delay requested from
JetStream; scheduling and processing load can cause the next delivery to occur
later.

Cluster operators must enable both experimental fields in the Knative Eventing
`config-features` ConfigMap before Trigger or Broker owners use this example:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: config-features
  namespace: knative-eventing
data:
  delivery-backoff-max: enabled
  delivery-retryafter: enabled
```

## Configuration

### Filter Environment Variables

These variables set the **broker-wide defaults** for the filter deployment. All triggers in the same broker share these defaults unless they override them with per-trigger annotations (see below).

| Variable | Description | Default |
|----------|-------------|---------|
| `NATS_URL` | NATS server URL | Required |
| `POD_NAME` | Pod name for identification | Required |
| `CONTAINER_NAME` | Container name for identification | Required |
| `CONSUMER_FETCH_BATCH_SIZE` | Number of messages to fetch per batch | `10` |
| `CONSUMER_FETCH_TIMEOUT` | How long a fetch waits for messages before returning empty | `200ms` |
| `CONSUMER_MAX_CONCURRENCY` | Maximum concurrent in-flight HTTP dispatches per trigger | `20` |

### Configuring a Broker with a NatsJetStreamBrokerConfig

To configure a Broker, create a `NatsJetStreamBrokerConfig` resource and
reference it from the Broker's `spec.config`.
Its `spec` covers the JetStream `stream` settings and the `filter` deployment,
whose `env` entries become environment variables on the filter.

```yaml
apiVersion: eventing.knative.dev/v1alpha1
kind: NatsJetStreamBrokerConfig
metadata:
  name: my-broker-config
  namespace: default
spec:
  filter:
    replicas: 2
    env:
      - name: CONSUMER_FETCH_BATCH_SIZE
        value: "50"
      - name: CONSUMER_FETCH_TIMEOUT
        value: "1s"
      - name: CONSUMER_MAX_CONCURRENCY
        value: "40"
  stream:
    replicas: 3
---
apiVersion: eventing.knative.dev/v1
kind: Broker
metadata:
  name: my-broker
  namespace: default
  annotations:
    eventing.knative.dev/broker.class: NatsJetStreamBroker
spec:
  config:
    apiVersion: eventing.knative.dev/v1alpha1
    kind: NatsJetStreamBrokerConfig
    name: my-broker-config
```

`spec.config.namespace` defaults to the Broker's namespace. Knative Eventing
prevents changing `spec.config` after the Broker is created; the referenced
`NatsJetStreamBrokerConfig` itself can be updated.

Existing Brokers can continue using the inline JSON annotation
`natsjetstream.eventing.knative.dev/config`. The controller emits a
`LegacyConfigDeprecated` warning when it is non-empty. An explicit
`NatsJetStreamBrokerConfig` reference takes precedence over the annotation.

| Broker configuration | Configuration used |
|----------------------|--------------------|
| `spec.config` references a `NatsJetStreamBrokerConfig` | That resource's entire `spec`; defaults are not merged into it |
| `spec.config` is omitted or references a `v1/ConfigMap`, and the legacy annotation is non-empty | The annotation's entire JSON configuration; defaults are not merged into it |
| `spec.config` is omitted or references a `v1/ConfigMap`, and the legacy annotation is absent or empty | The Broker namespace's `natsjetstream-broker-config` ConfigMap: matching `namespaceDefaults`, then `clusterDefault`, then built-in defaults |
| `spec.config` references another kind or API version | `ConfigLoadFailed` |
| The referenced `NatsJetStreamBrokerConfig` does not exist | `ConfigLoadFailed` |
| The legacy annotation contains invalid JSON and no new CR is referenced | `ConfigLoadFailed` |

Knative's webhook can supply a `v1/ConfigMap` reference even for a
`NatsJetStreamBroker`. The controller does not read NATS settings from that
reference. When it uses broker defaults instead, it emits a
`ConfigMapReferenceIgnored` warning naming the ignored reference and the
`natsjetstream-broker-config` ConfigMap it uses for defaults. This applies to
explicit ConfigMap references as well as webhook defaults.

`filter.labels` and `filter.podLabels` can add custom labels to the Deployment
and Pods. The required `eventing.knative.dev/broker` and
`eventing.knative.dev/role` labels take precedence over conflicting custom
labels so that the Deployment and Service selectors continue to match the Pods.

`stream` settings are used only when the JetStream stream is first created;
changing them does not update an existing stream.

### Per-Trigger Configuration

Each trigger can override the broker-wide defaults via annotations. This lets individual triggers with different throughput or latency requirements coexist in the same broker without affecting each other.

| Annotation | Description | Default |
|------------|-------------|---------|
| `natsjetstream.eventing.knative.dev/fetch-batch-size` | Messages fetched per pull request | `CONSUMER_FETCH_BATCH_SIZE` |
| `natsjetstream.eventing.knative.dev/fetch-timeout` | Wait time per fetch when no messages arrive (Go duration, e.g. `200ms`, `1s`) | `CONSUMER_FETCH_TIMEOUT` |
| `natsjetstream.eventing.knative.dev/max-concurrency` | Maximum concurrent in-flight HTTP dispatches for this trigger | `CONSUMER_MAX_CONCURRENCY` |

```yaml
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: high-throughput-trigger
  annotations:
    natsjetstream.eventing.knative.dev/fetch-batch-size: "100"
    natsjetstream.eventing.knative.dev/fetch-timeout: "1s"
    natsjetstream.eventing.knative.dev/max-concurrency: "50"
spec:
  broker: my-broker
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: analytics-service
```

### Backpressure and Concurrency Model

The filter dispatches messages concurrently using a per-trigger counting semaphore controlled by `max-concurrency`. The semaphore is acquired **before** fetching each message's dispatch goroutine, so when all slots are occupied the fetch loop stalls and new messages remain in the JetStream stream with their AckWait clocks not yet running. This prevents unbounded goroutine growth and avoids the AckWait expiry / duplicate-delivery problem that arises when messages are fetched faster than they can be processed.

Each dispatched message also carries a context deadline equal to the consumer's `AckWait` (set via `trigger.spec.delivery.timeout`). If the subscriber does not respond within that window the HTTP call is cancelled and JetStream redelivers the message automatically.

Slow triggers affect only their own semaphore — they cannot starve or delay other triggers sharing the same filter pod.

### Tuning Guidelines

| Scenario | Batch Size | Fetch Timeout | Max Concurrency | Use Case |
|----------|------------|---------------|-----------------|----------|
| High Throughput | 50–100 | 1–2s | 50–100 | Batch processing, analytics |
| Low Latency | 1–5 | 100ms | 10–20 | Real-time notifications |
| Slow Subscriber | 5–10 | 200ms | 2–5 | Rate-limited or expensive downstream |
| Balanced (default) | 10 | 200ms | 20 | General purpose |

The fetch loop caps each pull request to the number of free semaphore slots, so a batch is never larger than the available dispatch capacity. This means `max-concurrency` and `fetch-batch-size` can be tuned independently — there is no requirement for one to be larger than the other.

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
3. Check the filter Pod logs by running the command:

    ```bash
    kubectl logs -l eventing.knative.dev/role=filter -n <namespace>
    ```

### High latency or low throughput

1. Increase `CONSUMER_FETCH_BATCH_SIZE` (or the trigger annotation) to fetch more messages per round-trip
2. Increase `CONSUMER_MAX_CONCURRENCY` (or the trigger annotation) to allow more concurrent dispatches
3. Check NATS server health and connection

### Duplicate message delivery

If consumers are receiving the same event more than once, the most likely cause is that the subscriber is taking longer than `trigger.spec.delivery.timeout` (the consumer's AckWait) to respond. JetStream redelivers the message once AckWait expires. Increase the timeout or reduce subscriber latency.

### Events going to dead letter sink

1. Check subscriber logs for errors
2. Verify subscriber endpoint is correct
3. Review retry configuration
