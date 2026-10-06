/*
Copyright 2026 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"log"

	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/signals"

	"knative.dev/eventing-natss/pkg/broker/filter"
)

func main() {
	component := "natsjs-broker-filter"

	ctx := signals.NewContext()
	scope, err := filter.BrokerScopeFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	runtime := filter.NewRuntime(ctx)
	ctx = configureContext(ctx, runtime, scope)

	sharedmain.MainWithContext(ctx, component, runtime.NewController)
}

func configureContext(ctx context.Context, runtime *filter.Runtime, scope filter.BrokerScope) context.Context {
	ctx = injection.WithNamespaceScope(ctx, scope.Namespace)
	// Pull consumers share work between replicas without a controller leader.
	ctx = sharedmain.WithHADisabled(ctx)
	ctx = injection.AddReadiness(ctx, runtime.ReadinessHandler())
	return injection.AddLiveness(ctx, runtime.LivenessHandler())
}
