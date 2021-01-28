module knative.dev/eventing-natss

go 1.15

require (
	github.com/cloudevents/sdk-go/protocol/stan/v2 v2.2.0
	github.com/cloudevents/sdk-go/v2 v2.2.0
	github.com/google/go-cmp v0.5.4
	github.com/google/uuid v1.1.2
	github.com/hashicorp/go-uuid v1.0.2 // indirect
	github.com/influxdata/tdigest v0.0.1 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/nats-io/stan.go v0.6.0
	github.com/pkg/errors v0.9.1
	go.uber.org/zap v1.16.0
	k8s.io/api v0.19.7
	k8s.io/apimachinery v0.19.7
	k8s.io/client-go v0.19.7
	knative.dev/eventing v0.20.1-0.20210127134430-ea452b5b6655
	knative.dev/hack v0.0.0-20210120165453-8d623a0af457
	knative.dev/pkg v0.0.0-20210127163530-0d31134d5f4e
	knative.dev/reconciler-test v0.0.0-20210127151131-28c3852c3e9b
)
