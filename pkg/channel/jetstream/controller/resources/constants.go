package resources

const (
	ChannelLabelKey   = "messaging.knative.dev/channel"
	ChannelLabelValue = "nats-jetstream-channel"

	RoleLabelKey             = "messaging.knative.dev/role"
	DispatcherRoleLabelValue = "dispatcher"
	ControllerRoleLabelValue = "controller"
)
