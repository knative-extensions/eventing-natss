# NATS - simple installation

## 1. NATS Streaming Deprecation Notice :
> 1. The NATS Streaming Server is being deprecated.
> NATS enabled applications requiring persistence should use [JetStream](https://docs.nats.io/jetstream/jetstream).
> 2. Nats Streaming Channel Supported in `eventing-natss` will be supported 4 release cycles thereafter (until v0.29.0)


1. For an installation of a simple NATS Streaming server, a setup is provided:

   ```shell
   kubectl create namespace natss
   kubectl apply -n natss -f ./config/broker/natss.yaml
   ```

   NATS Streaming is deployed as a StatefulSet, using "nats-streaming" ConfigMap
   in the namespace "natss".

For tuning NATS Streaming, see
[here](https://github.com/nats-io/nats-streaming-server#configuring)

## 2. NATS JetStream （recommend）

1. For an installation of a simple NATS JetStream server, a setup is provided:

   ```shell
   kubectl apply -n natss -f ./config/broker/natsjsm.yaml
   ```

   NATS JetStream is also deployed as a StatefulSet, using "nats-jetstream" ConfigMap
   in the namespace "nats".

See more about NATS JetStream [here](https://docs.nats.io/jetstream/jetstream)
