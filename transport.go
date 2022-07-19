package datatransfer

import (
	"context"

	"github.com/ipld/go-ipld-prime/datamodel"
)

// TransportID identifies a unique transport
type TransportID string

// LegacyTransportID is the only transport for the fil/data-transfer protocol --
// i.e. graphsync
const LegacyTransportID TransportID = "graphsync"

// LegacyTransportVersion is the only transport version for the fil/data-transfer protocol --
// i.e. graphsync 1.0.0
var LegacyTransportVersion Version = Version{1, 0, 0}

type TransportEvent interface {
	transportEvent()
}

// TransportOpenedChannel occurs when the transport begins processing the
// request (prior to that it may simply be queued) -- only applies to initiator
type TransportOpenedChannel struct{}

// TransportInitiatedTransfer occurs when the transport actually begins sending/receiving data
type TransportInitiatedTransfer struct{}

// TransportReceivedData occurs when we receive data for the given channel ID
// index is a transport dependent of serializing "here's where I am in this transport"
type TransportReceivedData struct {
	Size  uint64
	Index datamodel.Node
}

// TransportSentData occurs when we send data for the given channel ID
// index is a transport dependent of serializing "here's where I am in this transport"
type TransportSentData struct {
	Size  uint64
	Index datamodel.Node
}

// TransportQueuedData occurs when data is queued for sending for the given channel ID
// index is a transport dependent of serializing "here's where I am in this transport"
type TransportQueuedData struct {
	Size  uint64
	Index datamodel.Node
}

// TransportReachedDataLimit occurs when a channel hits a previously set data limit
type TransportReachedDataLimit struct{}

// TransportTransferCancelled occurs when a request we opened (with the given channel Id) to
// receive data is cancelled by us.
type TransportTransferCancelled struct {
	ErrorMessage string
}

// TransportErrorSendingData occurs when a network error occurs trying to send a request
type TransportErrorSendingData struct {
	ErrorMessage string
}

// TransportErrorReceivingData occurs when a network error occurs receiving data
// at the transport layer
type TransportErrorReceivingData struct {
	ErrorMessage string
}

// TransportCompletedTransfer occurs when we finish transferring data for the given channel ID
type TransportCompletedTransfer struct {
	Success      bool
	ErrorMessage string
}

type TransportReceivedRestartExistingChannelRequest struct{}

// TransportErrorSendingMessage occurs when a network error occurs trying to send a request
type TransportErrorSendingMessage struct {
	ErrorMessage string
}

type TransportPaused struct{}

type TransportResumed struct{}

// EventsHandler are semantic data transfer events that happen as a result of transport events
type EventsHandler interface {
	// ChannelState queries for the current channel state
	ChannelState(ctx context.Context, chid ChannelID) (ChannelState, error)

	// OnTransportEvent is dispatched when an event occurs on the transport
	// It MAY be dispatched asynchronously by the transport to the time the
	// event occurs
	// However, the other handler functions may ONLY be called on the same channel
	// after all events are dispatched. In other words, the transport MUST allow
	// the handler to process all events before calling the other functions which
	// have a synchronous return
	OnTransportEvent(chid ChannelID, event TransportEvent)

	// OnRequestReceived occurs when we receive a  request for the given channel ID
	// return values are a message to send an error if the transport should be closed
	// TODO: in a future improvement, a received request should become a
	// just TransportEvent, and should be handled asynchronously
	OnRequestReceived(chid ChannelID, msg Request) (Response, error)

	// OnRequestReceived occurs when we receive a response to a request
	// TODO: in a future improvement, a received response should become a
	// just TransportEvent, and should be handled asynchronously
	OnResponseReceived(chid ChannelID, msg Response) error

	// OnContextAugment allows the transport to attach data transfer tracing information
	// to its local context, in order to create a hierarchical trace
	OnContextAugment(chid ChannelID) func(context.Context) context.Context
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
and send messages. Beyond that, additional commands may or may not be supported.
Whether a command is supported can be determined ahead by calling Capabilities().
*/
type Transport interface {
	// ID is a unique identifier for this transport
	ID() TransportID

	// Versions indicates what versions of this transport are supported
	Versions() []Version

	// Capabilities tells datatransfer what kinds of capabilities this transport supports
	Capabilities() TransportCapabilities
	// OpenChannel opens a channel on a given transport to move data back and forth.
	// OpenChannel MUST ALWAYS called by the initiator.
	OpenChannel(
		ctx context.Context,
		channel Channel,
		req Request,
	) error

	// ChannelUpdated notifies the transport that state of the channel has been updated,
	// along with an optional message to send over the transport to tell
	// the other peer about the update
	ChannelUpdated(ctx context.Context, chid ChannelID, message Message) error
	// SetEventHandler sets the handler for events on channels
	SetEventHandler(events EventsHandler) error
	// CleanupChannel removes any associated data on a closed channel
	CleanupChannel(chid ChannelID)
	// SendMessage sends a data transfer message over the channel to the other peer
	SendMessage(ctx context.Context, chid ChannelID, msg Message) error
	// Shutdown unregisters the current EventHandler and ends all active data transfers
	Shutdown(ctx context.Context) error

	// Optional Methods: Some channels may not support these

	// Restart restarts a channel on the initiator side
	// RestartChannel MUST ALWAYS called by the initiator
	RestartChannel(ctx context.Context, channel ChannelState, req Request) error
}

// TransportCapabilities describes additional capabilities supported by ChannelActions
type TransportCapabilities struct {
	// Restarable indicates ChannelActions will support RestartActions
	Restartable bool
	// Pausable indicates ChannelActions will support PauseActions
	Pausable bool
}

func (TransportOpenedChannel) transportEvent()                         {}
func (TransportInitiatedTransfer) transportEvent()                     {}
func (TransportReceivedData) transportEvent()                          {}
func (TransportSentData) transportEvent()                              {}
func (TransportQueuedData) transportEvent()                            {}
func (TransportReachedDataLimit) transportEvent()                      {}
func (TransportTransferCancelled) transportEvent()                     {}
func (TransportErrorSendingData) transportEvent()                      {}
func (TransportErrorReceivingData) transportEvent()                    {}
func (TransportCompletedTransfer) transportEvent()                     {}
func (TransportReceivedRestartExistingChannelRequest) transportEvent() {}
func (TransportErrorSendingMessage) transportEvent()                   {}
func (TransportPaused) transportEvent()                                {}
func (TransportResumed) transportEvent()                               {}
