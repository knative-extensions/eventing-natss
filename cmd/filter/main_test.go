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
	"testing"

	"knative.dev/eventing-natss/pkg/broker/filter"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
)

func TestConfigureContext(t *testing.T) {
	ctx := configureContext(context.Background(), filter.BrokerScope{Namespace: "namespace-a", Name: "broker-a"})
	if got := injection.GetNamespaceScope(ctx); got != "namespace-a" {
		t.Fatalf("namespace = %q", got)
	}
	if !sharedmain.IsHADisabled(ctx) {
		t.Fatal("each filter replica must run its local consumers")
	}
}
