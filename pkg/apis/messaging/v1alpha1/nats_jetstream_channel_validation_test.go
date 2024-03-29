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
	"context"
	"testing"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"

	"github.com/google/go-cmp/cmp"
	"knative.dev/pkg/webhook/resourcesemantics"

	"knative.dev/pkg/apis"
)

func TestNatssChannelValidation(t *testing.T) {
	aURL, _ := apis.ParseURL("http://example.com")

	testCases := map[string]struct {
		cr   resourcesemantics.GenericCRD
		want *apis.FieldError
	}{
		"empty spec": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{},
			},
			want: nil,
		},
		"valid subscribers array": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{
					ChannelableSpec: eventingduckv1.ChannelableSpec{
						SubscribableSpec: eventingduckv1.SubscribableSpec{
							Subscribers: []eventingduckv1.SubscriberSpec{{
								SubscriberURI: aURL,
								ReplyURI:      aURL,
							}},
						},
					},
				},
			},
			want: nil,
		},
		"empty subscriber at index 1": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{
					ChannelableSpec: eventingduckv1.ChannelableSpec{
						SubscribableSpec: eventingduckv1.SubscribableSpec{
							Subscribers: []eventingduckv1.SubscriberSpec{{
								SubscriberURI: aURL,
								ReplyURI:      aURL,
							}, {}},
						},
					},
				},
			},
			want: func() *apis.FieldError {
				fe := apis.ErrMissingField("spec.subscribable.subscriber[1].replyURI", "spec.subscribable.subscriber[1].subscriberURI")
				fe.Details = "expected at least one of, got none"
				return fe
			}(),
		},
		"two empty subscribers": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{
					ChannelableSpec: eventingduckv1.ChannelableSpec{
						SubscribableSpec: eventingduckv1.SubscribableSpec{
							Subscribers: []eventingduckv1.SubscriberSpec{{}, {}},
						},
					},
				},
			},
			want: func() *apis.FieldError {
				var errs *apis.FieldError
				fe := apis.ErrMissingField("spec.subscribable.subscriber[0].replyURI", "spec.subscribable.subscriber[0].subscriberURI")
				fe.Details = "expected at least one of, got none"
				errs = errs.Also(fe)
				fe = apis.ErrMissingField("spec.subscribable.subscriber[1].replyURI", "spec.subscribable.subscriber[1].subscriberURI")
				fe.Details = "expected at least one of, got none"
				errs = errs.Also(fe)
				return errs
			}(),
		},
	}

	for n, test := range testCases {
		t.Run(n, func(t *testing.T) {
			got := test.cr.Validate(context.Background())
			if diff := cmp.Diff(test.want.Error(), got.Error()); diff != "" {
				t.Errorf("%s: validate (-want, +got) = %v", n, diff)
			}
		})
	}
}
