// +build e2e

/*
Copyright 2020 The Knative Authors

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

package e2e

import (
	"context"
	"strings"
	"time"

	"knative.dev/eventing-natss/test/e2e/config/direct"
	"knative.dev/reconciler-test/pkg/environment"
	"knative.dev/reconciler-test/pkg/eventshub"
	"knative.dev/reconciler-test/pkg/feature"

	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "knative.dev/pkg/system/testing"
	"knative.dev/reconciler-test/pkg/k8s"
)

//
// producer ---> broker --[trigger]--> recorder
//

const (
	interval = 1 * time.Second
	timeout  = 5 * time.Minute
)

// DirectTestBrokerImpl makes sure an MT Broker backed by natss channel delivers events to a single consumer.
func DirectTestBroker() *feature.Feature {
	f := new(feature.Feature)

	f.Setup("install test resources", direct.Install())
	f.Alpha("MT broker with natss goes ready").Must("goes ready", AllGoReady)
	f.Alpha("MT broker with natss delivers events").
		Must("the recorder received all sent events within the time",
			func(ctx context.Context, t feature.T) {
				// TODO: Use constraint matching instead of just counting number of events.
				eventshub.StoreFromContext(ctx, "recorder").AssertAtLeast(5)
			})
	return f
}

func AllGoReady(ctx context.Context, t feature.T) {
	env := environment.FromContext(ctx)
	for _, ref := range env.References() {
		if !strings.Contains(ref.APIVersion, "knative.dev") {
			// Let's not care so much about checking the status of non-Knative
			// resources.
			continue
		}
		if err := k8s.WaitForReadyOrDone(ctx, ref, interval, timeout); err != nil {
			t.Fatal("failed to wait for ready or done, ", err)
		}
	}
	t.Log("all resources ready")
}
