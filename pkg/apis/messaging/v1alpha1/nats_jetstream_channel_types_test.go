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

package v1alpha1

import (
	"testing"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"

	"github.com/google/go-cmp/cmp"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

func TestNatsJetStreamChannel_GetGroupVersionKind(t *testing.T) {
	chn := NatsJetStreamChannel{}
	gvk := chn.GetGroupVersionKind()

	if gvk.Kind != "NatsJetStreamChannel" {
		t.Errorf("Should be 'NatsJetStreamChannel'.")
	}
}

func TestNatsJetStreamChannelGetStatus(t *testing.T) {
	status := &duckv1.Status{}
	config := NatsJetStreamChannel{
		Status: NatsJetStreamChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				Status: *status,
			},
		},
	}

	if !cmp.Equal(config.GetStatus(), status) {
		t.Errorf("GetStatus did not retrieve status. Got=%v Want=%v", config.GetStatus(), status)
	}
}
