/*
Copyright 2018 The Knative Authors

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

package dispatcher

import (
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
)

type subscriptionReference eventingduckv1.SubscriberSpec

func newSubscriptionReference(spec eventingduckv1.SubscriberSpec) subscriptionReference {
	s := eventingduckv1.SubscriberSpec{
		UID:           spec.UID,
		Generation:    spec.Generation,
		SubscriberURI: spec.SubscriberURI,
		ReplyURI:      spec.ReplyURI,
		Delivery:      spec.Delivery,
	}

	// TODO: this does not map to v1 from v1alpha1
	//if spec.Delivery != nil && spec.Delivery.DeadLetterSink != nil {
	//	s.DeadLetterSinkURI = spec.Delivery.DeadLetterSink.URI
	//}
	return subscriptionReference(s)
}

func (r *subscriptionReference) String() string {
	return string(r.UID)
}
