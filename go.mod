module knative.dev/eventing-natss

go 1.14

require (
	github.com/cloudevents/sdk-go/protocol/stan/v2 v2.2.0
	github.com/cloudevents/sdk-go/v2 v2.2.0
	github.com/google/go-cmp v0.5.2
	github.com/google/uuid v1.1.1
	github.com/hashicorp/go-uuid v1.0.2 // indirect
	github.com/influxdata/tdigest v0.0.1 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/nats-io/stan.go v0.6.0
	github.com/pkg/errors v0.9.1
	github.com/stretchr/testify v1.6.0 // indirect
	go.opencensus.io v0.22.5-0.20200716030834-3456e1d174b2 // indirect
	go.uber.org/zap v1.15.0
	k8s.io/api v0.18.8
	k8s.io/apimachinery v0.18.8
	k8s.io/client-go v11.0.1-0.20190805182717-6502b5e7b1b5+incompatible
	knative.dev/eventing v0.18.1-0.20201027234734-7ba58ca471c0
	knative.dev/pkg v0.0.0-20201028014535-ed076316cbe1
	knative.dev/test-infra v0.0.0-20201028004634-fe8002e29c05
)

replace (
	k8s.io/api => k8s.io/api v0.18.8
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.18.8
	k8s.io/apimachinery => k8s.io/apimachinery v0.18.8
	k8s.io/apiserver => k8s.io/apiserver v0.18.8
	k8s.io/client-go => k8s.io/client-go v0.18.8
	k8s.io/code-generator => k8s.io/code-generator v0.18.8
)
