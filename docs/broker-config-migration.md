# Migrating Broker configuration

After installing the new CRD and controller, move the JSON from the
`natsjetstream.eventing.knative.dev/config` annotation into the `spec` of a
[`NatsJetStreamBrokerConfig`](broker.md#configuring-a-broker-with-a-natsjetstreambrokerconfig)
in the Broker's namespace, keeping the same values. For example:

```bash
kubectl create -f - <<'EOF'
apiVersion: eventing.knative.dev/v1alpha1
kind: NatsJetStreamBrokerConfig
metadata:
  name: my-broker-config
  namespace: default
spec:
  filter:
    replicas: 1
    env:
      - name: CONSUMER_FETCH_BATCH_SIZE
        value: "50"
EOF
```

Then update the existing Broker.

## 1. Temporarily exclude the namespace from Knative webhooks

```bash
kubectl label namespace default webhooks.knative.dev/exclude=true
```

This skips Knative webhook checks for the namespace until the label is removed.

## 2. Edit the Broker

```bash
kubectl -n default edit broker my-broker
```

Remove the `natsjetstream.eventing.knative.dev/config` annotation and set
`spec.config` as follows.

```diff
 apiVersion: eventing.knative.dev/v1
 kind: Broker
 metadata:
   name: my-broker
   namespace: default
   annotations:
     eventing.knative.dev/broker.class: NatsJetStreamBroker
-    natsjetstream.eventing.knative.dev/config: |
-      {"filter":{"replicas":1,"env":[{"name":"CONSUMER_FETCH_BATCH_SIZE","value":"50"}]}}
 spec:
   config:
-    apiVersion: v1
-    kind: ConfigMap
-    namespace: knative-eventing
-    name: config-br-default-channel
+    apiVersion: eventing.knative.dev/v1alpha1
+    kind: NatsJetStreamBrokerConfig
+    namespace: default
+    name: my-broker-config
```

## 3. Restore webhook validation

Remove the exclusion label, even if the edit failed:

```bash
kubectl label namespace default webhooks.knative.dev/exclude-
```
