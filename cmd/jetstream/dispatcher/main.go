/*
Copyright 2021 The Knative Authors

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
	"os"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/signals"

	"knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher"
	"knative.dev/eventing-natss/pkg/common/configloader/fsloader"
)

func main() {
	component := "jetstream-channel-dispatcher"

	ctx := signals.NewContext()
	ns := os.Getenv("NAMESPACE")
	if ns != "" {
		// component is used to build the lease name for leader-election. When namespace-scoped, dispatchers scoped to
		// different namespaces need to have their own leader election.
		component = component + "-" + ns

		ctx = injection.WithNamespaceScope(ctx, ns)
	}

	// nats-config is volume mounted so initialize the fsloader
	ctx = fsloader.WithLoader(ctx, configmap.Load)

	sharedmain.MainWithContext(ctx, component, dispatcher.NewController)
}
