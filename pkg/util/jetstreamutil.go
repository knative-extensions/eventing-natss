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

package util

import (
	"fmt"

	"knative.dev/pkg/network"
)

const (
	// defaultJetStreamURLVar is the environment variable that can be set to specify the nats jetStream url
	defaultJetStreamURLVar = "DEFAULT_JETSTREAM_URL"

	fallbackDefaultJetStreamURLTmpl = "nats://jet-stream.nats.svc.%s:4222"
)

// GetDefaultJetStreamURL returns the default jet stream url to connect to
func GetDefaultJetStreamURL() string {
	return getEnv(defaultJetStreamURLVar, fmt.Sprintf(fallbackDefaultJetStreamURLTmpl, network.GetClusterDomainName()))
}
