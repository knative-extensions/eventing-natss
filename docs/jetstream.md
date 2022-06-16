# NATS JetStream - Proposal Specification

This document outlines the proposed implementation of NATS JetStream channel support.

## Overview

There are two components to this implementation:

- Controller - `jetstream-channel-controller`
- Dispatcher - `jetstream-channel-dispatcher`

### Controller

The controller reconciles `NatsJetStreamChannel` CRDs by ensuring the `Deployment` named `jetstream-ch-dispatcher`
exists and ensures a `Service` corresponding to the channel exists with an `externalName` pointing to the
`jetstream-ch-dispatcher`.

#### Scoping

`NatsJetStreamChannel` resources may be scoped to a namespace by adding the annotation
`eventing.knative.dev/scope: namespace`, allowing the use of dedicated dispatcher deployments per namespace. This has
two main benefits:

- Channels across different namespaces cannot be starved of resources based on channels in other namespaces.
- Channels across different namespaces can be configured with different credentials.

#### Controller Dispatcher Configuration

The controller dispatcher configuration tells the controller how dispatchers should be managed. It is not configuration
for the dispatcher itself. This is implemented as a `ConfigMap` which is volume-mounted into the controller pod and must
contain a file `jetstream-dispatcher-config`.

In the below example, you may already have a `ConfigMap` named `config-nats` in `my-namespace` which serves another
purpose, in these circumstances you can tell the controller to use a different `ConfigMap` when creating a dispatcher in
that namespace.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
    name: config-ch-jetstream-defaults
    namespace: knative-eventing
data:
    jetstream-dispatcher-config: |
        clusterDefault:
            configName: config-nats
        namespaceDefaults:
            my-namespace:
                configName: custom-jetstream-config     # for dispatchers in the namespace, "my-namespace", use the 
                                                        # ConfigMap "custom-jetstream-config". Only applicable when 
                                                        # namespace-scoping is enabled.
```

### Dispatcher

The dispatcher is not deployed manually, but is created by the controller when a `NatsJetStreamChannel` is reconciled
and the dispatcher does not exist.

The dispatcher has 3 roles:

1. Ensuring a Stream per `NatsJetStreamChannel` exists with the correct configuration. The Stream is configured based on
   the configuration within the `NatsJetStreamChannel` resource itself.
2. Ensuring each subscriber for a `NatsJetStreamChannel` (from `.spec.subscribers`) has an associated JetStream Consumer
   consuming messages from the Stream. Each received message is forwarded to the underlying subscriber workload (by the
   `.spec.subscriber.ref` or `.spec.subscriber.uri` field in the `Subscription`)
3. An event receiver, receiving Cloudevents over HTTP, matching the `Host` header to a `NatsJetStreamChannel` (recall
   that events are sent to the channel's address which is an `ExternalName` to the dispatcher, so each channel has a
   unique address). Each received event is then published to the correct JetStream queue.

#### Dispatcher configuration

The dispatcher is configured by a `ConfigMap` which should contain a file in yaml format called `eventing-nats`.
This config map is mounted as a volume by the controller when it creates the dispatcher. Which config map is mounted is
configurable by the [Controller Dispatcher Configuration](#Controller Dispatcher Configuration) documented above.

This config map should follow the shape outlined below:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
    name: config-nats
    namespace: knative-eventing
data:
    eventing-nats: |
        url: ""                         # the URL to the JetStream-enabled NATS server
        auth:                           # only one of the following keys may be set (see "Future Functionality" below)
            credentialFile:             # details of the NATS credential file to use for authentication, initially only
                                        # 'secret' will be supported, but other options may be implemented in the future.
                secret:
                    key: ""             # the key of the secret containing the credentials, defaults to "nats.creds"
                    secretName: ""      # the secret containing a NATS credentials file.
        tls:
            secretName: ""              # a secret containing the same keys as the "kubernetes.io/tls" type, if only a 
                                        # `ca.crt` is present then we skip client verification but still ensure the 
                                        # connection is encrypted with server verification.
```

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

The dispatcher will support the following security features:

- Unsecured NATS clusters (i.e. for usage within private networks)
- Secured NATS clusters with a Credentials File (https://docs.nats.io/developing-with-nats/security/creds)
  - It is the responsibility of the user to ensure a ConfigMap named `config-nats` exists in the namespace to which a 
    dispatcher is being deployed in.
- TLS-enabled clusters (https://docs.nats.io/developing-with-nats/security/tls)
    - CA should be defined in a ConfigMap/Secret in the `knative-eventing` namespace.

All `NatsJetStreamChannel` resources managed by a dispatcher will inherit the same security configuration. If you wish
for different channels use separate credentials then they must exist in different namespaces and scoped to that
namespace (see [Scoping](#Scoping) above).

## `NatsJetStreamChannel` CRD

```yaml
apiVersion: messaging.knative.dev/v1alpha1
kind: NatsJetStreamChannel
metadata:
    name: foo
spec:
    subscribers: []                 # SubscribableSpec
    delivery: {}                    # DeliverySpec
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

### Future functionality

- **Flow control** - this relies on some client-side functionality to resume delivery so isn't planned for the initial
  implementation
- Additional authentication mechanisms - initially only credential files will be supported, but the following should be
  added to the roadmap:

  ```yaml
  auth:
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
  ```
