//go:build e2e
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
	"os"
	"testing"

	"knative.dev/pkg/system"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	// For our e2e testing, we want this linked first so that our
	// system namespace environment variable is defaulted prior to
	// logstream initialization.
	_ "knative.dev/eventing-natss/test/defaultsystem"
	"knative.dev/reconciler-test/pkg/environment"
	"knative.dev/reconciler-test/pkg/k8s"
	"knative.dev/reconciler-test/pkg/knative"
)

var global environment.GlobalEnvironment

func TestMain(m *testing.M) {
	global = environment.NewStandardGlobalEnvironment()
	os.Exit(m.Run())
}

// TestBrokerDirect makes sure a Broker can delivery events to a consumer.
func TestBrokerDirect(t *testing.T) {
	t.Parallel()
	ctx, env := global.Environment(
		knative.WithKnativeNamespace(system.Namespace()),
		knative.WithLoggingConfig,
		knative.WithTracingConfig,
		k8s.WithEventListener,
	)
	env.Test(ctx, t, RecorderFeature())
	env.Test(ctx, t, DirectTestBroker())
	env.Finish()
}
