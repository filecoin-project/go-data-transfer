package datatransfer

type transferConfig struct {
	eventsCb Subscriber
}

func (tc *transferConfig) EventsCb() Subscriber {
	return tc.eventsCb
}

// TransferOption customizes a single transfer
type TransferOption func(*transferConfig)

// WithSubscriber dispatches only events for this specific channel transfer
func WithSubscriber(eventsCb Subscriber) TransferOption {
	return func(tc *transferConfig) {
		tc.eventsCb = eventsCb
	}
}

// TransferConfig accesses transfer properties
type TransferConfig interface {
	EventsCb() Subscriber
}

// FromOptions builds a config from an options list
func FromOptions(options []TransferOption) TransferConfig {
	tc := &transferConfig{}
	for _, option := range options {
		option(tc)
	}
	return tc
}
