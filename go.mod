module knative.dev/eventing-natss

go 1.16

require (
	github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2 v2.0.0-20210715165402-49fda7a51425
	github.com/cloudevents/sdk-go/protocol/stan/v2 v2.2.0
	github.com/cloudevents/sdk-go/v2 v2.8.0
	github.com/google/go-cmp v0.5.6
	github.com/google/uuid v1.3.0
	github.com/influxdata/tdigest v0.0.1 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/nats-io/nats.go v1.11.1-0.20210623165838-4b75fc59ae30
	github.com/nats-io/stan.go v0.9.0
	github.com/pkg/errors v0.9.1
	go.uber.org/zap v1.19.1
	k8s.io/api v0.22.5
	k8s.io/apimachinery v0.22.5
	k8s.io/client-go v0.22.5
	knative.dev/eventing v0.30.1-0.20220316055858-c02e63aea351
	knative.dev/hack v0.0.0-20220314052818-c9c3ea17a2e9
	knative.dev/pkg v0.0.0-20220316002959-3a4cc56708b9
	knative.dev/reconciler-test v0.0.0-20220314160418-3b7a0d7f7b4b
)

replace github.com/cloudevents/sdk-go/v2 => github.com/cloudevents/sdk-go/v2 v2.4.1-0.20210715165402-49fda7a51425
