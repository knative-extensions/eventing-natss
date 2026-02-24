# NATS - simple installation

## NATS JetStream

1. For an installation of a simple NATS JetStream server, a setup is provided:

   ```shell
   kubectl apply -f ./config/broker/natsjsm.yaml
   kubectl apply -f ./config/broker/config-nats.yaml
   kubectl apply -f ./config/broker/config-br-default-channel-jsm.yaml
   ```

   NATS JetStream is also deployed as a StatefulSet, using "nats-jetstream" ConfigMap
   in the namespace "nats-io".

See more about NATS JetStream [here](https://docs.nats.io/jetstream/jetstream)
