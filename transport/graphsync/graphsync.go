package graphsync

import (
	"context"
	"sync"
	"time"

	"github.com/ipfs/go-graphsync"
	logging "github.com/ipfs/go-log/v2"
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/sync/errgroup"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	dtchannel "github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/dtchannel"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
	"github.com/filecoin-project/go-data-transfer/v2/transport/helpers/network"
)

var log = logging.Logger("dt_graphsync")

var transportID datatransfer.TransportID = "graphsync"

// When restarting a data transfer, we cancel the existing graphsync request
// before opening a new one.
// This constant defines the maximum time to wait for the request to be
// cancelled.

var defaultSupportedExtensions = []graphsync.ExtensionName{
	extension.ExtensionDataTransfer1_1,
}

var incomingReqExtensions = []graphsync.ExtensionName{
	extension.ExtensionIncomingRequest1_1,
	extension.ExtensionDataTransfer1_1,
}

var outgoingBlkExtensions = []graphsync.ExtensionName{
	extension.ExtensionOutgoingBlock1_1,
	extension.ExtensionDataTransfer1_1,
}

// Option is an option for setting up the graphsync transport
type Option func(*Transport)

// SupportedExtensions sets what data transfer extensions are supported
func SupportedExtensions(supportedExtensions []graphsync.ExtensionName) Option {
	return func(t *Transport) {
		t.supportedExtensions = supportedExtensions
	}
}

// RegisterCompletedRequestListener is used by the tests
func RegisterCompletedRequestListener(l func(channelID datatransfer.ChannelID)) Option {
	return func(t *Transport) {
		t.completedRequestListener = l
	}
}

// RegisterCompletedResponseListener is used by the tests
func RegisterCompletedResponseListener(l func(channelID datatransfer.ChannelID)) Option {
	return func(t *Transport) {
		t.completedResponseListener = l
	}
}

// Transport manages graphsync hooks for data transfer, translating from
// graphsync hooks to semantic data transfer events
type Transport struct {
	events datatransfer.EventsHandler
	gs     graphsync.GraphExchange
	dtNet  network.DataTransferNetwork
	peerID peer.ID

	supportedExtensions       []graphsync.ExtensionName
	unregisterFuncs           []graphsync.UnregisterHookFunc
	completedRequestListener  func(channelID datatransfer.ChannelID)
	completedResponseListener func(channelID datatransfer.ChannelID)

	// Map from data transfer channel ID to information about that channel
	dtChannelsLk sync.RWMutex
	dtChannels   map[datatransfer.ChannelID]*dtchannel.Channel

	// Used in graphsync callbacks to map from graphsync request to the
	// associated data-transfer channel ID.
	requestIDToChannelID *requestIDToChannelIDMap
}

// NewTransport makes a new hooks manager with the given hook events interface
func NewTransport(peerID peer.ID, gs graphsync.GraphExchange, dtNet network.DataTransferNetwork, options ...Option) *Transport {
	t := &Transport{
		gs:                   gs,
		dtNet:                dtNet,
		peerID:               peerID,
		supportedExtensions:  defaultSupportedExtensions,
		dtChannels:           make(map[datatransfer.ChannelID]*dtchannel.Channel),
		requestIDToChannelID: newRequestIDToChannelIDMap(),
	}
	for _, option := range options {
		option(t)
	}
	return t
}

func (t *Transport) ID() datatransfer.TransportID {
	return transportID
}

func (t *Transport) Capabilities() datatransfer.TransportCapabilities {
	return datatransfer.TransportCapabilities{
		Pausable:    true,
		Restartable: true,
	}
}

// OpenChannel opens a channel on a given transport to move data back and forth.
// OpenChannel MUST ALWAYS called by the initiator.
func (t *Transport) OpenChannel(
	ctx context.Context,
	channel datatransfer.Channel,
	req datatransfer.Request) error {
	t.dtNet.Protect(channel.OtherPeer(), channel.ChannelID().String())
	if channel.IsPull() {
		return t.openRequest(ctx,
			channel.Sender(),
			channel.ChannelID(),
			cidlink.Link{Cid: channel.BaseCID()},
			channel.Selector(),
			req)
	}
	return t.dtNet.SendMessage(ctx, channel.OtherPeer(), transportID, req)
}

// RestartChannel restarts a channel on the initiator side
// RestartChannel MUST ALWAYS called by the initiator
func (t *Transport) RestartChannel(
	ctx context.Context,
	channelState datatransfer.ChannelState,
	req datatransfer.Request) error {
	log.Debugf("%s: re-establishing connection to %s", channelState.ChannelID(), channelState.OtherPeer())
	start := time.Now()
	err := t.dtNet.ConnectWithRetry(ctx, channelState.OtherPeer())
	if err != nil {
		return xerrors.Errorf("%s: failed to reconnect to peer %s after %s: %w",
			channelState.ChannelID(), channelState.OtherPeer(), time.Since(start), err)
	}
	log.Debugf("%s: re-established connection to %s in %s", channelState.ChannelID(), channelState.OtherPeer(), time.Since(start))

	t.dtNet.Protect(channelState.OtherPeer(), channelState.ChannelID().String())

	ch := t.trackDTChannel(channelState.ChannelID())
	ch.UpdateReceivedCidsIfGreater(channelState.ReceivedCidsTotal())
	if channelState.IsPull() {
		return t.openRequest(ctx,
			channelState.Sender(),
			channelState.ChannelID(),
			cidlink.Link{Cid: channelState.BaseCID()},
			channelState.Selector(),
			req)
	}
	return t.dtNet.SendMessage(ctx, channelState.OtherPeer(), transportID, req)
}

func (t *Transport) openRequest(
	ctx context.Context,
	dataSender peer.ID,
	channelID datatransfer.ChannelID,
	root ipld.Link,
	stor datamodel.Node,
	msg datatransfer.Message,
) error {
	if t.events == nil {
		return datatransfer.ErrHandlerNotSet
	}

	exts, err := extension.ToExtensionData(msg, t.supportedExtensions)
	if err != nil {
		return err
	}

	// Start tracking the data-transfer channel
	ch := t.trackDTChannel(channelID)

	requestID := graphsync.NewRequestID()
	t.requestIDToChannelID.set(requestID, false, channelID)

	// Open a graphsync request to the remote peer
	execetor, err := ch.Open(ctx, requestID, dataSender, root, stor, exts)

	if err != nil {
		return err
	}

	execetor.Start(t.events, t.completedRequestListener)
	return nil
}

// UpdateChannel sends one or more updates the transport channel at once,
// such as pausing/resuming, closing the transfer, or sending additional
// messages over the channel. Grouping the commands allows the transport
// the ability to plan how to execute these updates
func (t *Transport) UpdateChannel(ctx context.Context, chid datatransfer.ChannelID, update datatransfer.ChannelUpdate) error {

	ch, err := t.getDTChannel(chid)
	if err != nil {
		if update.SendMessage != nil && !update.Closed {
			return t.dtNet.SendMessage(ctx, t.otherPeer(chid), transportID, update.SendMessage)
		}
		return err
	}

	if !update.Paused && ch.Paused() {

		var extensions []graphsync.ExtensionData
		if update.SendMessage != nil {
			var err error
			extensions, err = extension.ToExtensionData(update.SendMessage, t.supportedExtensions)
			if err != nil {
				return err
			}
		}

		return ch.Resume(ctx, extensions)
	}

	if update.SendMessage != nil {
		if err := t.dtNet.SendMessage(ctx, t.otherPeer(chid), transportID, update.SendMessage); err != nil {
			return err
		}
	}

	if update.Closed {
		return ch.Close(ctx)
	}

	if update.Paused && !ch.Paused() {
		return ch.Pause(ctx)
	}

	return nil
}

// SendMessage sends a data transfer message over the channel to the other peer
func (t *Transport) SendMessage(ctx context.Context, chid datatransfer.ChannelID, msg datatransfer.Message) error {
	return t.dtNet.SendMessage(ctx, t.otherPeer(chid), transportID, msg)
}

// CleanupChannel is called on the otherside of a cancel - removes any associated
// data for the channel
func (t *Transport) CleanupChannel(chid datatransfer.ChannelID) {
	t.dtChannelsLk.Lock()

	ch, ok := t.dtChannels[chid]
	if ok {
		// Remove the reference to the channel from the channels map
		delete(t.dtChannels, chid)
	}

	t.dtChannelsLk.Unlock()

	// Clean up mapping from gs key to channel ID
	t.requestIDToChannelID.deleteRefs(chid)

	// Clean up the channel
	if ok {
		ch.Cleanup()
	}

	t.dtNet.Unprotect(t.otherPeer(chid), chid.String())
}

// SetEventHandler sets the handler for events on channels
func (t *Transport) SetEventHandler(events datatransfer.EventsHandler) error {
	if t.events != nil {
		return datatransfer.ErrHandlerAlreadySet
	}
	t.events = events

	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterIncomingRequestQueuedHook(t.gsReqQueuedHook))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterIncomingRequestHook(t.gsReqRecdHook))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterCompletedResponseListener(t.gsCompletedResponseListener))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterIncomingBlockHook(t.gsIncomingBlockHook))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterOutgoingBlockHook(t.gsOutgoingBlockHook))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterBlockSentListener(t.gsBlockSentHook))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterOutgoingRequestHook(t.gsOutgoingRequestHook))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterIncomingResponseHook(t.gsIncomingResponseHook))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterRequestUpdatedHook(t.gsRequestUpdatedHook))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterRequestorCancelledListener(t.gsRequestorCancelledListener))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterNetworkErrorListener(t.gsNetworkSendErrorListener))
	t.unregisterFuncs = append(t.unregisterFuncs, t.gs.RegisterReceiverNetworkErrorListener(t.gsNetworkReceiveErrorListener))

	t.dtNet.SetDelegate(transportID, &receiver{t})
	return nil
}

// Shutdown disconnects a transport interface from graphsync
func (t *Transport) Shutdown(ctx context.Context) error {
	for _, unregisterFunc := range t.unregisterFuncs {
		unregisterFunc()
	}

	t.dtChannelsLk.Lock()
	defer t.dtChannelsLk.Unlock()

	var eg errgroup.Group
	for _, ch := range t.dtChannels {
		ch := ch
		eg.Go(func() error {
			return ch.Close(ctx)
		})
	}

	err := eg.Wait()
	if err != nil {
		return xerrors.Errorf("shutting down graphsync transport: %w", err)
	}
	return nil
}

// UseStore tells the graphsync transport to use the given loader and storer for this channelID
func (t *Transport) UseStore(channelID datatransfer.ChannelID, lsys ipld.LinkSystem) error {
	ch := t.trackDTChannel(channelID)
	return ch.UseStore(lsys)
}

// ChannelGraphsyncRequests describes any graphsync request IDs associated with a given channel
type ChannelGraphsyncRequests struct {
	// Current is the current request ID for the transfer
	Current graphsync.RequestID
	// Previous are ids of previous GraphSync requests in a transfer that
	// has been restarted. We may be interested to know if these IDs are active
	// on either side of the request
	Previous []graphsync.RequestID
}

// ChannelsForPeer describes current active channels for a given peer and their
// associated graphsync requests
type ChannelsForPeer struct {
	SendingChannels   map[datatransfer.ChannelID]ChannelGraphsyncRequests
	ReceivingChannels map[datatransfer.ChannelID]ChannelGraphsyncRequests
}

// ChannelsForPeer identifies which channels are open and which request IDs they map to
func (t *Transport) ChannelsForPeer(p peer.ID) ChannelsForPeer {
	t.dtChannelsLk.RLock()
	defer t.dtChannelsLk.RUnlock()

	// cannot have active transfers with self
	if p == t.peerID {
		return ChannelsForPeer{
			SendingChannels:   map[datatransfer.ChannelID]ChannelGraphsyncRequests{},
			ReceivingChannels: map[datatransfer.ChannelID]ChannelGraphsyncRequests{},
		}
	}

	sending := make(map[datatransfer.ChannelID]ChannelGraphsyncRequests)
	receiving := make(map[datatransfer.ChannelID]ChannelGraphsyncRequests)
	// loop through every graphsync request key we're currently tracking
	t.requestIDToChannelID.forEach(func(requestID graphsync.RequestID, isSending bool, chid datatransfer.ChannelID) {
		// if the associated channel ID includes the requested peer
		if chid.Initiator == p || chid.Responder == p {
			// determine whether the requested peer is one at least one end of the channel
			// and whether we're receving from that peer or sending to it
			collection := sending
			if !isSending {
				collection = receiving
			}
			channelGraphsyncRequests := collection[chid]
			// finally, determine if the request key matches the current GraphSync key we're tracking for
			// this channel, indicating it's the current graphsync request
			if t.dtChannels[chid] != nil && t.dtChannels[chid].IsCurrentRequest(requestID) {
				channelGraphsyncRequests.Current = requestID
			} else {
				// otherwise this id was a previous graphsync request on a channel that was restarted
				// and it has not been cleaned up yet
				channelGraphsyncRequests.Previous = append(channelGraphsyncRequests.Previous, requestID)
			}
			collection[chid] = channelGraphsyncRequests
		}
	})
	return ChannelsForPeer{
		SendingChannels:   sending,
		ReceivingChannels: receiving,
	}
}

var _ datatransfer.Transport = (*Transport)(nil)
