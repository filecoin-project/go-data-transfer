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

// EventsHandler are semantic data transfer events that happen as a result of transport events
type EventsHandler interface {
	// ChannelState queries for the current channel state
	ChannelState(ctx context.Context, chid ChannelID) (ChannelState, error)

	// OnChannelOpened is called at the point the transport begins processing the
	// request (prior to that it may simply be queued) -- only applies to initiator
	OnChannelOpened(chid ChannelID)

	// OnTransferInitiated is called at the point the transport actually begins sending/receiving data
	OnTransferInitiated(chid ChannelID)

	// OnRequestReceived is called when we receive a new request for the given channel ID
	// return values is a channel command to take action based on this response
	OnRequestReceived(chid ChannelID, msg Request) (ChannelCommand, error)

	// OnResponseReceived is called when we receive a response to a request
	OnResponseReceived(chid ChannelID, msg Response)

	// OnDataReceive is called when we receive data for the given channel ID
	// index is a transport dependent of serializing "here's where I am in this transport"
	OnDataReceived(chid ChannelID, size uint64, index datamodel.Node)

	// OnDataQueued is called when data is queued for sending for the given channel ID
	// index is a transport dependent of serializing "here's where I am in this transport"
	OnDataQueued(chid ChannelID, size uint64, index datamodel.Node)

	// OnDataSent is called when we send data for the given channel ID
	// index is a transport dependent of serializing "here's where I am in this transport"
	OnDataSent(chid ChannelID, size uint64, index datamodel.Node)

	// OnDataLimitReached is called when a channel hits a previously set data limit
	OnDataLimitReached(chid ChannelID)

	// OnTransferCompleted is called when we finish transferring data for the given channel ID
	OnTransferCompleted(chid ChannelID, err error)

	// OnTransferCannceled is called when a request we opened (with the given channel Id) to
	// receive data is cancelled by us.
	OnTransferCancelled(chid ChannelID, err error)

	// OnMessageSendError is called when a network error occurs while sending a message
	OnMessageSendError(chid ChannelID, err error)

	// OnRequestDisconnected is called when a network error occurs trying to send a request
	OnRequestDisconnected(chid ChannelID, err error)

	// OnSendDataError is called when a network error occurs sending data
	// at the transport layer
	OnSendDataError(chid ChannelID, err error)

	// OnReceiveDataError is called when a network error occurs receiving data
	// at the transport layer
	OnReceiveDataError(chid ChannelID, err error)

	// OnContextAugment allows the transport to attach data transfer tracing information
	// to its local context, in order to create a hierarchical trace
	OnContextAugment(chid ChannelID) func(context.Context) context.Context

	OnRestartExistingChannelRequestReceived(chid ChannelID)
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

	// UpdateChannel sends one or more updates the transport channel at once,
	// such as pausing/resuming, closing the transfer, or sending additional
	// messages over the channel. Grouping the commands allows the transport
	// the ability to plan how to execute these updates based on the capabilities
	// and API of the underlying transport protocol and library
	SendChannelCommand(ctx context.Context, chid ChannelID, command ChannelCommand) error
	// SetEventHandler sets the handler for events on channels
	SetEventHandler(events EventsHandler) error
	// CleanupChannel removes any associated data on a closed channel
	CleanupChannel(chid ChannelID)
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

// ChannelUpdate describes updates to a channel - changing it's paused status, closing the transfer,
// and additional messages to send
type ChannelUpdate interface {
	Paused() (bool, bool)
	Closed() (bool, bool)
	MessageToSend() (Message, bool)
	DataLimit() (uint64, bool)
}

type channelUpdate struct {
	paused           bool
	hasPaused        bool
	closed           bool
	hasClosed        bool
	messageToSend    Message
	hasMessageToSend bool
	dataLimit        uint64
	hasDataLimit     bool
}

func (cu channelUpdate) Paused() (bool, bool) {
	return cu.paused, cu.hasPaused
}

func (cu channelUpdate) Closed() (bool, bool) {
	return cu.closed, cu.hasClosed
}

func (cu channelUpdate) MessageToSend() (Message, bool) {
	return cu.messageToSend, cu.hasMessageToSend
}

func (cu channelUpdate) DataLimit() (uint64, bool) {
	return cu.dataLimit, cu.hasDataLimit
}

type ChannelCommand struct {
	cu channelUpdate
}

func NewCommand() ChannelCommand {
	return ChannelCommand{}
}

func (cc ChannelCommand) SetPaused(paused bool) ChannelCommand {
	cu := cc.cu
	cu.paused = paused
	cu.hasPaused = true
	return ChannelCommand{cu}
}

func (cc ChannelCommand) SetClosed(closed bool) ChannelCommand {
	cu := cc.cu
	cu.closed = true
	cu.hasClosed = true
	return ChannelCommand{cu}
}

func (cc ChannelCommand) SendMessage(msg Message) ChannelCommand {
	cu := cc.cu
	cu.messageToSend = msg
	cu.hasMessageToSend = true
	return ChannelCommand{cu}
}

func (cc ChannelCommand) SetDataLimit(datalimit uint64) ChannelCommand {
	cu := cc.cu
	cu.dataLimit = datalimit
	cu.hasDataLimit = true
	return ChannelCommand{cu}
}

func (cc ChannelCommand) ChannelUpdate() ChannelUpdate {
	return cc.cu
}
