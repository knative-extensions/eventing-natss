module knative.dev/eventing-natss

go 1.16

require (
	github.com/cloudevents/sdk-go/protocol/stan/v2 v2.2.0
	github.com/cloudevents/sdk-go/v2 v2.4.1
	github.com/google/go-cmp v0.5.6
	github.com/google/uuid v1.2.0
	github.com/influxdata/tdigest v0.0.1 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/nats-io/stan.go v0.6.0
	github.com/pkg/errors v0.9.1
	go.uber.org/zap v1.18.1
	k8s.io/api v0.20.7
	k8s.io/apimachinery v0.20.7
	k8s.io/client-go v0.20.7
	knative.dev/eventing v0.24.1-0.20210714200632-25bd8efb7179
	knative.dev/hack v0.0.0-20210622141627-e28525d8d260
	knative.dev/pkg v0.0.0-20210715175632-d9b7180af6f2
	knative.dev/reconciler-test v0.0.0-20210714231231-91e5b206e1d5
)
