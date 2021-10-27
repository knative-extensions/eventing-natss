# NATS JetStream - Proposal Specification

This document outlines the proposed implementation of NATS JetStream channel support.

## Overview

There are two components to this implementation:

- Controller - `jetstream-channel-controller`
- Dispatcher - `jetstream-channel-dispatcher`

### Controller

The controller reconciles `NatsJetStreamChannel` CRDs by checking the availability of the `jetstream-channel-dispatcher`
and ensures a `Service` corresponding to the channel exists with an `externalName` pointing to the 
`jetstream-channel-dispatcher`.

### Dispatcher

The dispatcher has 3 roles:

1. Ensuring a Stream per `NatsJetStreamChannel` exists with the correct configuration. The Stream is configured based on
   defaults set in a `ConfigMap` or by explicit configuration within the `NatsJetStreamChannel` resource itself.
2. Ensuring each subscriber for a `NatsJetStreamChannel` (from `.spec.subscribers`) has an associated JetStream Consumer
   consuming messages from the Stream. Each received message is forwarded to the underlying subscriber workload (by the 
   `.spec.subscriber.ref` or `.spec.subsriber.uri` field in the `Subscription`)
3. An event receiver, receiving Cloudevents over HTTP, matching the `Host` header to a `NatsJetStreamChannel` (recall
   that events are sent to the channel's address which is an `ExternalName` to the dispatcher, so each channel has a 
   unique address). Each received event is then published to the correct JetStream queue.

## JetStream integration

This section outlines the features of NATS/JetStream which should be supported by this channel implementation.

### Stream/Consumer configuration

Each `NatsJetStreamChannel` should have the ability to configure the details of the created Streams and Consumers from 
it's `spec` field. For instances where `NatsJetStreamChannel` is configured as the default implementation for `Channel` 
resources, it's up to the user to configure these fields correctly in the `default-ch-webhook` config map.

Channels will by default create a stream name based of the channel namespace/name, however a name can be overridden to 
bind an existing stream which may already contain events.

Consumers will be created with the default configuration:

- Durable - the durable name will be the same as the consumer name. All Knative eventing consumers must be durable to 
  ensure delivery, this cannot be disabled.
- Pull based - the dispatcher is responsible for pulling messages as it's ready, push-based options cannot be configured
- Ack Policy is Explicit - this is the only allowed option for pull-based consumers

Other configuration elements use the JetStream defaults, and can be overridden in the CRD.

### Security

The following security features should be supported:

- Unsecured NATS clusters (i.e. for usage within private networks)
- Secured NATS clusters with a Credentials File (https://docs.nats.io/developing-with-nats/security/creds)
  - Globally-scoped credentials by a ConfigMap/Secret in the `knative-eventing` namespace
  - Namespace-scoped credentials by a ConfigMap/Secret in the same namespace as the `NatsJetStreamChannel`
  - Channel-scoped credentials specified by a Secret and referenced in the `NatsJetStreamChannel` resource.
- TLS-enabled clusters (https://docs.nats.io/developing-with-nats/security/tls)
  - CA should be defined in a ConfigMap/Secret in the `knative-eventing` namespace.

## `NatsJetStreamChannel` CRD

```yaml
apiVersion: messaging.knative.dev/v1alpha1
kind: NatsJetStreamChannel
metadata:
    name: foo
spec:
    subscribers: []                 # SubscribableSpec
    delivery: {}                    # DeliverySpec
    natsURL: ""                     # the URL to the JetStream-enabled NATS server
    authentication:                 # only one of the following keys may be set (see "Future Functionality" below)
        credentialFile:
            secret:
                key: ""             # the key of the secret containing the credentials, defaults to "nats.creds"
                secretName: ""      # the secret containing a NATS credentials file.
    tls:
        secretName: ""              # a secret containing the same keys as the "kubernetes.io/tls" type, if only a 
                                    # `ca.crt` is present then we skip client verification but still ensure the 
                                    # connection is encrypted with server verification.
    stream:
        overrideName: ""            # stream name defaults to be based on the namespace/name of the channel, use this 
                                    # to override it
        config:
            additionalSubjects: []  # we create a unique subject per channel, but users may have existing components 
                                    # publishing to existing subjects which users may want to adopt into Knative 
                                    # subscriptions
            retention: ""           # one of: Limits, Interest, Work - defaults to "Limits"
            maxConsumers: 0         # default 0 denoting no maximum
            maxMsgs: 0              # default 0 denoting no maximum
            maxBytes: 0             # default 0 denoting no maximum
            discard: ""             # one of: Old, New - defaults to "Old"
            maxAge: ""              # time.Duration, defaults to no max age
            maxMsgSize: 0           # default 0 denoting no maximum
            storage: ""             # one of: File, Memory - defaults to "File"
            replicas: 0             # applicable when the JetStream instance is clustered; the number of replicas of 
                                    # the data to create
            noAck: false            # defaults to false (i.e. ack enabled), not sure if we should allow users to 
                                    # configure this?
            template: ""            # the stream template to base this stream configuration from
            duplicateWindow: ""     # time.Duration for duplication tracking
            placement:
                cluster: ""         # place the stream on a specific cluster
                tags: [""]          # place the stream on servers with specific tags
            mirror:                 # mirror this stream from another stream
                name: ""            # the name of the stream to mirror
                optStartSeq: ""     # stringified uint64 - takes precendence over optStartTime
                optStartTime: ""    # RFC3339 datetime
                filterSubject: ""   # defaults to no filter
            sources: []             # an array of stream sources (see mirror)
    consumerConfigTemplate:         # a consumer per .spec.subscribers[] will be created with these defaults
        deliverPolicy: ""           # one of: All, Last, New, ByStartSequence, ByStartTime - defaults to "All"
        optStartSeq: ""             # stringified uint64 - takes precendence over optStartTime
        optStartTime: ""            # RFC3339 datetime
        ackWait: ""                 # time.Duration
        maxDeliver: 0               # defaults to no maximum
        filterSubject: ""           # defaults to no filter
        replayPolicy: ""            # one of: Instant, Original - defaults to "Instant"
        rateLimitBps: 0             # bits-per-second
        sampleFrequency: ""         # percentage of events to sample in the range 0-100, this is a string and allows 
                                    # both 30 and 30% as valid values
        maxAckPending: 0            # number of outstanding messages awaiting ack before suspending delivery, 0 denotes 
                                    # no maximum
        idleHeartbeat: ""           # if set, a time.Duration for the idle heartbeat the server sends to the client
```

### Defaults

> I'm not 100% certain on this aspect, if anybody wanted to comment on this design doc, this is the section I'd 
> appreciate feedback the most. For my requirements I need separate accounts per namespace, so providing this can be 
> achieved I don't mind the approach. It's similar to the Kafka approach but I'm not sure if Kafka allows this 
> customisation per-namespace like I'm suggesting.

Some CRD configuration can be defaulted by creating a ConfigMap containing a channel template. Global defaults 
should be defined in the Knative Eventing namespace (for most users this is `knative-eventing`), or namespace-scoped 
defaults in that corresponding namespace; the latter approach allows different NATS accounts per namespace.

The name of the config map is configurable in the eventing-nats Deployment by the `CONFIG_JETSTREAM_NAME` environment
variable and defaults to `config-ch-jetstream`. The eventing-nats controller will first look for the ConfigMap of this 
name in the same namespace as the `NatsJetStreamChannel`, and will fall back to the namespace the deployment is running 
in (i.e. `knative-eventing`) if the ConfigMap doesn't exist. If no ConfigMap is found, no defaults are applied.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
    name: config-ch-jetstream
data:
    channelTemplateSpec: |
        apiVersion: messaging.knative.dev/v1alpha1
        kind: NatsJetStreamChannel
        spec:
            natsURL: ""
            authentication:
                credentialFile:
                    secret:
                        key: ""
                        secretName: ""
            tls:
                secretName: ""
            ...
```

### Future functionality

- **Flow control** - this relies on some client-side functionality to resume delivery so isn't planned for the initial 
  implementation
- Additional authentication mechanisms - initially only credential files will be supported.

```yaml
apiVersion: messaging.knative.dev/v1alpha1
kind: NatsJetStreamChannel
metadata:
    name: foo
spec:
    authentication:
        usernamePassword:
            secret:
                usernameKey: ""     # the key of the secret containing the username, defaults to "username"
                passwordKey: ""     # the key of the secret containing the password, defaults to "password"
                secretName: ""      # the secret containing the username and password
        token:
            secret:
                key: ""             # the key of the secret containing the token, defaults to "token"
                secretName: ""      # the secret containing a token.
        nKey:                       # NKey authentication requires some client-side 
            secret:
                key: ""             # the key of the secret containing the credentials, defaults to "nats.creds"
                secretName: ""      # the secret containing a NATS credentials file.
    consumerConfigTemplate:         # a consumer per .spec.subscribers[] will be created with these defaults
        flowControl: false          # whether to enable flow control: https://docs.nats.io/jetstream/concepts/consumers#flowcontrol
```
