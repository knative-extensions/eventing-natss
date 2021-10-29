module knative.dev/eventing-natss

go 1.16

require (
	github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2 v2.0.0-20210715165402-49fda7a51425
	github.com/cloudevents/sdk-go/protocol/stan/v2 v2.2.0
	github.com/cloudevents/sdk-go/v2 v2.4.1
	github.com/google/go-cmp v0.5.6
	github.com/google/uuid v1.3.0
	github.com/influxdata/tdigest v0.0.1 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/nats-io/nats.go v1.11.1-0.20210623165838-4b75fc59ae30
	github.com/nats-io/stan.go v0.9.0
	github.com/pkg/errors v0.9.1
	go.uber.org/zap v1.19.1
	k8s.io/api v0.21.4
	k8s.io/apimachinery v0.21.4
	k8s.io/client-go v0.21.4
	knative.dev/eventing v0.26.1-0.20211029085753-34c08a406eca
	knative.dev/hack v0.0.0-20211028194650-b96d65a5ff5e
	knative.dev/pkg v0.0.0-20211028235650-5d9d300c2e40
	knative.dev/reconciler-test v0.0.0-20211029073051-cff9b538d33c
)

replace github.com/cloudevents/sdk-go/v2 => github.com/cloudevents/sdk-go/v2 v2.4.1-0.20210715165402-49fda7a51425
