package dispatcher

import (
	"context"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	messagingv1 "knative.dev/eventing/pkg/apis/messaging/v1"
)

type NatsDispatcher interface {
	Start(ctx context.Context) error
	UpdateSubscriptions(ctx context.Context, name, ns string, subscriptions []eventingduckv1.SubscriberSpec, isFinalizer bool) (map[eventingduckv1.SubscriberSpec]error, error)
	ProcessChannels(ctx context.Context, chanList []messagingv1.Channel) error
}
