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

	"k8s.io/utils/ptr"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/eventing/pkg/apis/feature"

	"github.com/google/go-cmp/cmp"
	"knative.dev/pkg/webhook/resourcesemantics"

	"knative.dev/pkg/apis"
)

func TestNatssChannelValidation(t *testing.T) {
	aURL, _ := apis.ParseURL("http://example.com")
	invalidPolicy := eventingduckv1.BackoffPolicyType("invalid")
	invalidFormat := eventingduckv1.FormatType("invalid")

	testCases := map[string]struct {
		ctx  context.Context
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
		"negative default retry": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{ChannelableSpec: eventingduckv1.ChannelableSpec{
					Delivery: &eventingduckv1.DeliverySpec{Retry: ptr.To(int32(-1))},
				}},
			},
			want: apis.ErrInvalidValue(int32(-1), "retry").ViaField("delivery").ViaField("spec"),
		},
		"invalid default backoff policy": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{ChannelableSpec: eventingduckv1.ChannelableSpec{
					Delivery: &eventingduckv1.DeliverySpec{BackoffPolicy: &invalidPolicy},
				}},
			},
			want: apis.ErrInvalidValue(invalidPolicy, "backoffPolicy").ViaField("delivery").ViaField("spec"),
		},
		"malformed default backoff delay": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{ChannelableSpec: eventingduckv1.ChannelableSpec{
					Delivery: &eventingduckv1.DeliverySpec{BackoffDelay: ptr.To("not-a-duration")},
				}},
			},
			want: apis.ErrInvalidValue("not-a-duration", "backoffDelay").ViaField("delivery").ViaField("spec"),
		},
		"invalid default format": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{ChannelableSpec: eventingduckv1.ChannelableSpec{
					Delivery: &eventingduckv1.DeliverySpec{Format: &invalidFormat},
				}},
			},
			want: apis.ErrInvalidValue(invalidFormat, "format").ViaField("delivery").ViaField("spec"),
		},
		"retry after max rejected when feature disabled": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{ChannelableSpec: eventingduckv1.ChannelableSpec{
					Delivery: &eventingduckv1.DeliverySpec{RetryAfterMax: ptr.To("PT2S")},
				}},
			},
			want: apis.ErrDisallowedFields("retryAfterMax").ViaField("delivery").ViaField("spec"),
		},
		"retry after max accepted when feature enabled": {
			ctx: feature.ToContext(context.Background(), feature.Flags{
				feature.DeliveryRetryAfter: feature.Enabled,
			}),
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{ChannelableSpec: eventingduckv1.ChannelableSpec{
					Delivery: &eventingduckv1.DeliverySpec{RetryAfterMax: ptr.To("PT2S")},
				}},
			},
		},
		"negative subscriber retry": {
			cr: &NatsJetStreamChannel{
				Spec: NatsJetStreamChannelSpec{ChannelableSpec: eventingduckv1.ChannelableSpec{
					SubscribableSpec: eventingduckv1.SubscribableSpec{Subscribers: []eventingduckv1.SubscriberSpec{{
						SubscriberURI: aURL,
						Delivery:      &eventingduckv1.DeliverySpec{Retry: ptr.To(int32(-1))},
					}}},
				}},
			},
			want: apis.ErrInvalidValue(int32(-1), "retry").ViaField("delivery").ViaIndex(0).ViaField("subscribers").ViaField("spec"),
		},
	}

	for n, test := range testCases {
		t.Run(n, func(t *testing.T) {
			ctx := test.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			got := test.cr.Validate(ctx)
			if diff := cmp.Diff(test.want.Error(), got.Error()); diff != "" {
				t.Errorf("%s: validate (-want, +got) = %v", n, diff)
			}
		})
	}
}
