# NATS Streaming Channels

JetStream channels are alpha-quality Channels that are backed by
[JetStream](https://docs.nats.io/nats-concepts/jetstream).

They offer:

- Persistence
  - If the Channel's Pod goes down, all events already ACKed by the Channel will
    persist and be retransmitted when the Pod restarts.
- Redelivery attempts
  - If downstream rejects an event, that request is attempted again. NOTE:
    downstream must successfully process the event within one minute or the
    delivery is assumed to have failed and will be reattempted.

They do not offer:

- Ordering guarantees
  - Events seen downstream may not occur in the same order they were inserted
    into the Channel.

## Deployment steps

1. Setup [Knative Eventing](http://knative.dev/docs/install).
2. If not done already, install a [JetStream Broker](./broker/README.md)
3. Apply the NATSS configuration (from project root):

   ```shell
   ko apply -f ./config
   ```

4. Create JetStream channels:

   ```yaml
    apiVersion: messaging.knative.dev/v1alpha1
    kind: NatsJetStreamChannel
    metadata:
        name: channel-defaults
        namespace: knative-eventing
   ```

## Components

The major components are:

- JetStream
- JetStream Channel Controller
- JetStream Channel Dispatcher

The JetStream Channel Controller is located in one Pod.

```shell
kubectl get deployment -n knative-eventing jetstream-ch-controller
```

The JetStream Channel Dispatcher receives and distributes all events. There is a
single Dispatcher for all JetStream Channels.

By default the components are configured to connect to NATS at
`nats://nats.nats-io.svc.cluster.local:4222`.
