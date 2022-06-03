package datatransfer

import (
	"context"

	ipld "github.com/ipld/go-ipld-prime"
)

// EventsHandler are semantic data transfer events that happen as a result of transport events
type EventsHandler interface {
	// ChannelState queries for the current channel state
	ChannelState(ctx context.Context, chid ChannelID) (ChannelState, error)

	// OnChannelOpened is called when we send a request for data to the other
	// peer on the given channel ID
	// return values are:
	// - error = ignore incoming data for this channel
	OnChannelOpened(chid ChannelID) error
	// OnResponseReceived is called when we receive a response to a request
	// - nil = continue receiving data
	// - error = cancel this request
	OnResponseReceived(chid ChannelID, msg Response) error
	// OnDataReceive is called when we receive data for the given channel ID
	// return values are:
	// - nil = proceed with sending data
	// - error = cancel this request
	// - err == ErrPause - pause this request
	OnDataReceived(chid ChannelID, link ipld.Link, size uint64, index int64, unique bool) error

	// OnDataQueued is called when data is queued for sending for the given channel ID
	// return values are:
	// - nil = proceed with sending data
	// - error = cancel this request
	// - err == ErrPause - pause this request
	OnDataQueued(chid ChannelID, link ipld.Link, size uint64, index int64, unique bool) (Message, error)

	// OnDataSent is called when we send data for the given channel ID
	OnDataSent(chid ChannelID, link ipld.Link, size uint64, index int64, unique bool) error

	// OnTransferQueued is called when a new data transfer request is queued in the transport layer.
	OnTransferQueued(chid ChannelID)

	// OnRequestReceived is called when we receive a new request to send data
	// for the given channel ID
	// return values are:
	// message = data transfer message along with reply
	// err = error
	// - nil = proceed with sending data
	// - error = cancel this request
	// - err == ErrPause - pause this request (only for new requests)
	// - err == ErrResume - resume this request (only for update requests)
	OnRequestReceived(chid ChannelID, msg Request) (Response, error)
	// OnChannelCompleted is called when we finish transferring data for the given channel ID
	// Error returns are logged but otherwise have no effect
	OnChannelCompleted(chid ChannelID, err error) error

	// OnRequestCancelled is called when a request we opened (with the given channel Id) to
	// receive data is cancelled by us.
	// Error returns are logged but otherwise have no effect
	OnRequestCancelled(chid ChannelID, err error) error

	// OnRequestDisconnected is called when a network error occurs trying to send a request
	OnRequestDisconnected(chid ChannelID, err error) error

	// OnSendDataError is called when a network error occurs sending data
	// at the transport layer
	OnSendDataError(chid ChannelID, err error) error

	// OnReceiveDataError is called when a network error occurs receiving data
	// at the transport layer
	OnReceiveDataError(chid ChannelID, err error) error

	// OnContextAugment allows the transport to attach data transfer tracing information
	// to its local context, in order to create a hierarchical trace
	OnContextAugment(chid ChannelID) func(context.Context) context.Context

	OnRestartExistingChannelRequestReceived(chid ChannelID) error
}

/*
Transport defines the interface for a transport layer for data
transfer. Where the data transfer manager will coordinate setting up push and
pull requests, persistence, validation, etc, the transport layer is responsible for moving
data back and forth, and may be medium specific. For example, some transports
may have the ability to pause and resume requests, while others may not.
Some may dispatch data update events, while others may only support message
events. Some transport layers may opt to use the actual data transfer network
protocols directly while others may be able to encode messages in their own
data protocol.

Transport is the minimum interface that must be satisfied to serve as a datatransfer
transport layer. Transports must be able to open and close channels, set at an event handler,
and send messages. Beyond that, additional actions you can take with a transport is
entirely based on the ChannelActions interface, which may have different
traits exposing different actions. */
type Transport interface {
	// Capabilities tells datatransfer what kinds of capabilities this transport supports
	Capabilities() TransportCapabilities
	// OpenChannel opens a channel on a given transport to move data back and forth.
	// OpenChannel MUST ALWAYS called by the initiator.
	OpenChannel(
		ctx context.Context,
		channel Channel,
		req Request,
	) error
	// WithChannel takes action using the given command func on an open channel
	WithChannel(ctx context.Context, chid ChannelID, actions ChannelActionFunc) error
	// SetEventHandler sets the handler for events on channels
	SetEventHandler(events EventsHandler) error
	// CleanupChannel removes any associated data on a closed channel
	CleanupChannel(chid ChannelID)
	Shutdown(ctx context.Context) error
}

// TransportCapabilities describes additional capabilities supported by ChannelActions
type TransportCapabilities struct {
	// Restarable indicates ChannelActions will support RestartActions
	Restartable bool
	// Pausable indicates ChannelActions will support PauseActions
	Pausable bool
}

// ChannelActionFunc is a function that takes actions on a channel
type ChannelActionFunc func(ChannelActions)

// ChannelActions are default actions that can be taken on a channel
type ChannelActions interface {
	CloseActions
	SendMessageActions
}

// SendMessageActions is a trait that allows sending messages
// directly
type SendMessageActions interface {
	// SendMessage sends an arbitrary message over a transport
	SendMessage(message Message)
}

// CloseActions is a trait that allows closing a channel
type CloseActions interface {
	// CloseChannel close this channel and effectively closes it to further
	// action
	CloseChannel()
}

// RestartableTransport is a transport that allows restarting of channels
type RestartableTransport interface {
	// Restart restarts a channel on the initiator side
	// RestartChannel MUST ALWAYS called by the initiator
	RestartChannel(ctx context.Context, channel ChannelState, req Request) error
}

// PauseActions a trait that allows pausing and resuming a channel
type PauseActions interface {
	// PauseChannel paused the given channel ID
	PauseChannel()
	// ResumeChannel resumes the given channel
	ResumeChannel()
}
