package testutil

import (
	"context"
	"errors"
	"sync"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/libp2p/go-libp2p/core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

// OpenedChannel records a call to open a channel
type OpenedChannel struct {
	DataSender peer.ID
	ChannelID  datatransfer.ChannelID
	Root       ipld.Link
	Selector   datamodel.Node
	Channel    datatransfer.ChannelState
	Message    datatransfer.Message
}

// ResumedChannel records a call to resume a channel
type ResumedChannel struct {
	ChannelID datatransfer.ChannelID
	Message   datatransfer.Message
}

// FakeTransport is a fake transport with mocked results
type FakeTransport struct {
	TransportLk         sync.Mutex
	OpenedChannels      []OpenedChannel
	OpenChannelErr      error
	ClosedChannels      []datatransfer.ChannelID
	CloseChannelErr     error
	PausedChannels      []datatransfer.ChannelID
	PauseChannelErr     error
	ResumedChannels     []ResumedChannel
	ResumeChannelErr    error
	CleanedUpChannels   []datatransfer.ChannelID
	CustomizedTransfers []datatransfer.ChannelID
	EventHandler        datatransfer.EventsHandler
	SetEventHandlerErr  error
}

// NewFakeTransport returns a new instance of FakeTransport
func NewFakeTransport() *FakeTransport {
	return &FakeTransport{}
}

// OpenChannel initiates an outgoing request for the other peer to send data
// to us on this channel
// Note: from a data transfer symantic standpoint, it doesn't matter if the
// request is push or pull -- OpenChannel is called by the party that is
// intending to receive data
func (ft *FakeTransport) OpenChannel(ctx context.Context, dataSender peer.ID, channelID datatransfer.ChannelID, root ipld.Link, stor datamodel.Node, channel datatransfer.ChannelState, msg datatransfer.Message) error {
	ft.TransportLk.Lock()
	defer ft.TransportLk.Unlock()
	ft.OpenedChannels = append(ft.OpenedChannels, OpenedChannel{dataSender, channelID, root, stor, channel, msg})
	return ft.OpenChannelErr
}

// CloseChannel closes the given channel
func (ft *FakeTransport) CloseChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	ft.TransportLk.Lock()
	defer ft.TransportLk.Unlock()
	ft.ClosedChannels = append(ft.ClosedChannels, chid)
	return ft.CloseChannelErr
}

// SetEventHandler sets the handler for events on channels
func (ft *FakeTransport) SetEventHandler(events datatransfer.EventsHandler) error {
	ft.TransportLk.Lock()
	defer ft.TransportLk.Unlock()
	ft.EventHandler = events
	return ft.SetEventHandlerErr
}

func (ft *FakeTransport) Shutdown(ctx context.Context) error {
	return nil
}

// PauseChannel paused the given channel ID
func (ft *FakeTransport) PauseChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	ft.TransportLk.Lock()
	defer ft.TransportLk.Unlock()
	ft.PausedChannels = append(ft.PausedChannels, chid)
	return ft.PauseChannelErr
}

// ResumeChannel resumes the given channel
func (ft *FakeTransport) ResumeChannel(ctx context.Context, msg datatransfer.Message, chid datatransfer.ChannelID) error {
	ft.TransportLk.Lock()
	defer ft.TransportLk.Unlock()
	ft.ResumedChannels = append(ft.ResumedChannels, ResumedChannel{chid, msg})
	return ft.ResumeChannelErr
}

// CleanupChannel cleans up the given channel
func (ft *FakeTransport) CleanupChannel(chid datatransfer.ChannelID) {
	ft.TransportLk.Lock()
	defer ft.TransportLk.Unlock()
	ft.CleanedUpChannels = append(ft.CleanedUpChannels, chid)
}

func RecordCustomizedTransfer() datatransfer.TransportOption {
	return func(chid datatransfer.ChannelID, transport datatransfer.Transport) error {
		ft, ok := transport.(*FakeTransport)
		if !ok {
			return errors.New("incorrect transport")
		}
		ft.TransportLk.Lock()
		defer ft.TransportLk.Unlock()
		ft.CustomizedTransfers = append(ft.CustomizedTransfers, chid)
		return nil
	}
}
