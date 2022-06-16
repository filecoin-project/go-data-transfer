package testutil

import (
	"context"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

// OpenedChannel records a call to open a channel
type OpenedChannel struct {
	Channel datatransfer.Channel
	Message datatransfer.Request
}

// RestartedChannel records a call to restart a channel
type RestartedChannel struct {
	Channel datatransfer.ChannelState
	Message datatransfer.Request
}

// MessageSent records a message sent
type MessageSent struct {
	ChannelID datatransfer.ChannelID
	Message   datatransfer.Message
}

// DataLimitSet records setting a data limit
type DataLimitSet struct {
	ChannelID datatransfer.ChannelID
	DataLimit uint64
}

// CustomizedTransfer is just a way to record calls made to transport configurer
type CustomizedTransfer struct {
	ChannelID datatransfer.ChannelID
	Voucher   datatransfer.TypedVoucher
}

var _ datatransfer.Transport = &FakeTransport{}

// FakeTransport is a fake transport with mocked results
type FakeTransport struct {
	OpenedChannels      []OpenedChannel
	OpenChannelErr      error
	RestartedChannels   []RestartedChannel
	RestartChannelErr   error
	ClosedChannels      []datatransfer.ChannelID
	PausedChannels      []datatransfer.ChannelID
	ResumedChannels     []datatransfer.ChannelID
	DataLimitsSet       []DataLimitSet
	MessagesSent        []MessageSent
	UpdateError         error
	CleanedUpChannels   []datatransfer.ChannelID
	CustomizedTransfers []CustomizedTransfer
	EventHandler        datatransfer.EventsHandler
	SetEventHandlerErr  error
}

// NewFakeTransport returns a new instance of FakeTransport
func NewFakeTransport() *FakeTransport {
	return &FakeTransport{}
}

// ID is a unique identifier for this transport
func (ft *FakeTransport) ID() datatransfer.TransportID {
	return "fake"
}

// Versions indicates what versions of this transport are supported
func (ft *FakeTransport) Versions() []datatransfer.Version {
	return []datatransfer.Version{{Major: 1, Minor: 1, Patch: 0}}
}

// Capabilities tells datatransfer what kinds of capabilities this transport supports
func (ft *FakeTransport) Capabilities() datatransfer.TransportCapabilities {
	return datatransfer.TransportCapabilities{
		Restartable: true,
		Pausable:    true,
	}
}

// OpenChannel initiates an outgoing request for the other peer to send data
// to us on this channel
// Note: from a data transfer symantic standpoint, it doesn't matter if the
// request is push or pull -- OpenChannel is called by the party that is
// intending to receive data
func (ft *FakeTransport) OpenChannel(ctx context.Context, channel datatransfer.Channel, msg datatransfer.Request) error {
	ft.OpenedChannels = append(ft.OpenedChannels, OpenedChannel{channel, msg})
	return ft.OpenChannelErr
}

// RestartChannel restarts a channel
func (ft *FakeTransport) RestartChannel(ctx context.Context, channelState datatransfer.ChannelState, msg datatransfer.Request) error {
	ft.RestartedChannels = append(ft.RestartedChannels, RestartedChannel{channelState, msg})
	return ft.RestartChannelErr
}

// WithChannel takes actions on a channel
func (ft *FakeTransport) SendChannelCommand(ctx context.Context, chid datatransfer.ChannelID, command datatransfer.ChannelCommand) error {

	update := command.ChannelUpdate()
	message, sendMessage := update.MessageToSend()
	if sendMessage {
		ft.MessagesSent = append(ft.MessagesSent, MessageSent{chid, message})
	}

	closed, hasClosed := update.Closed()
	if hasClosed && closed {
		ft.ClosedChannels = append(ft.ClosedChannels, chid)
		return ft.UpdateError
	}

	paused, hasPaused := update.Paused()
	if hasPaused {
		if !paused {
			ft.ResumedChannels = append(ft.ResumedChannels, chid)
		} else {
			ft.PausedChannels = append(ft.PausedChannels, chid)
		}
	}

	dataLimit, hasDataLimit := update.DataLimit()
	if hasDataLimit {
		ft.DataLimitsSet = append(ft.DataLimitsSet, DataLimitSet{chid, dataLimit})
	}

	return ft.UpdateError
}

// SetEventHandler sets the handler for events on channels
func (ft *FakeTransport) SetEventHandler(events datatransfer.EventsHandler) error {
	ft.EventHandler = events
	return ft.SetEventHandlerErr
}

// Shutdown close this transport
func (ft *FakeTransport) Shutdown(ctx context.Context) error {
	return nil
}

// CleanupChannel cleans up the given channel
func (ft *FakeTransport) CleanupChannel(chid datatransfer.ChannelID) {
	ft.CleanedUpChannels = append(ft.CleanedUpChannels, chid)
}

func (ft *FakeTransport) RecordCustomizedTransfer(chid datatransfer.ChannelID, voucher datatransfer.TypedVoucher) {
	ft.CustomizedTransfers = append(ft.CustomizedTransfers, CustomizedTransfer{chid, voucher})
}
