package datatransfer

type TransportOption func(chid ChannelID, transport Transport) error

type transferConfig struct {
	eventsCb         Subscriber
	transportOptions []TransportOption
}

func (tc *transferConfig) EventsCb() Subscriber {
	return tc.eventsCb
}

func (tc *transferConfig) TransportOptions() []TransportOption {
	return tc.transportOptions
}

// TransferOption customizes a single transfer
type TransferOption func(*transferConfig)

// WithSubscriber dispatches only events for this specific channel transfer
func WithSubscriber(eventsCb Subscriber) TransferOption {
	return func(tc *transferConfig) {
		tc.eventsCb = eventsCb
	}
}

func WithTransportOptions(transportOptions ...TransportOption) TransferOption {
	return func(tc *transferConfig) {
		tc.transportOptions = transportOptions
	}
}

// TransferConfig accesses transfer properties
type TransferConfig interface {
	EventsCb() Subscriber
	TransportOptions() []TransportOption
}

// FromOptions builds a config from an options list
func FromOptions(options []TransferOption) TransferConfig {
	tc := &transferConfig{}
	for _, option := range options {
		option(tc)
	}
	return tc
}
