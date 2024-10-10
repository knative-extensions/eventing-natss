package dispatcher

type Consumer interface {
	ConsumerType() ConsumerType
	Close() error
	UpdateSubscription(c *ChannelConfig, sub Subscription)
}

type ConsumerType string

const (
	PushConsumerType ConsumerType = "Push"
	PullConsumerType ConsumerType = "Pull"
)
