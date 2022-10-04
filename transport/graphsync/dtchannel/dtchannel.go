package dtchannel

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/ipfs/go-graphsync"
	"github.com/ipfs/go-graphsync/donotsendfirstblocks"
	logging "github.com/ipfs/go-log/v2"
	ipld "github.com/ipld/go-ipld-prime"
	peer "github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/executor"
)

const maxGSCancelWait = time.Second

var log = logging.Logger("dt_graphsync")

// state is the state graphsync data transfer channel
type state uint64

//
const (
	channelClosed state = iota
	channelOpen
	channelPaused
)

// Info needed to keep track of a data transfer channel
type Channel struct {
	isSender  bool
	channelID datatransfer.ChannelID
	gs        graphsync.GraphExchange

	lk                 sync.RWMutex
	state              state
	requestID          *graphsync.RequestID
	completed          chan struct{}
	requesterCancelled bool
	pendingExtensions  []graphsync.ExtensionData

	storeLk         sync.RWMutex
	storeRegistered bool

	receivedIndex int64
	sentIndex     int64
	queuedIndex   int64
	dataLimit     uint64
	progress      uint64
}

func NewChannel(channelID datatransfer.ChannelID, gs graphsync.GraphExchange) *Channel {
	return &Channel{
		channelID: channelID,
		gs:        gs,
	}
}

// Open a graphsync request for data to the remote peer
func (c *Channel) Open(
	ctx context.Context,
	requestID graphsync.RequestID,
	dataSender peer.ID,
	root ipld.Link,
	stor ipld.Node,
	exts []graphsync.ExtensionData,
) (*executor.Executor, error) {
	c.lk.Lock()
	defer c.lk.Unlock()

	// If there is an existing graphsync request for this channelID
	if c.requestID != nil {
		// Cancel the existing graphsync request
		completed := c.completed
		errch := c.cancel(ctx)

		// Wait for the complete callback to be called
		c.lk.Unlock()
		err := waitForCompleteHook(ctx, completed)
		c.lk.Lock()
		if err != nil {
			return nil, fmt.Errorf("%s: waiting for cancelled graphsync request to complete: %w", c.channelID, err)
		}

		// Wait for the cancel request method to complete
		select {
		case err = <-errch:
		case <-ctx.Done():
			err = fmt.Errorf("timed out waiting for graphsync request to be cancelled")
		}
		if err != nil {
			return nil, fmt.Errorf("%s: restarting graphsync request: %w", c.channelID, err)
		}
	}

	// add do not send cids ext as needed
	if c.receivedIndex > 0 {
		data := donotsendfirstblocks.EncodeDoNotSendFirstBlocks(c.receivedIndex)
		exts = append(exts, graphsync.ExtensionData{
			Name: graphsync.ExtensionsDoNotSendFirstBlocks,
			Data: data,
		})
	}

	// Set up a completed channel that will be closed when the request
	// completes (or is cancelled)
	completed := make(chan struct{})
	var onCompleteOnce sync.Once
	onComplete := func() {
		// Ensure the channel is only closed once
		onCompleteOnce.Do(func() {
			c.MarkTransferComplete()
			log.Infow("closing the completion ch for data-transfer channel", "chid", c.channelID)
			close(completed)
		})
	}
	c.completed = completed

	// Open a new graphsync request
	msg := fmt.Sprintf("Opening graphsync request to %s for root %s", dataSender, root)
	if c.receivedIndex > 0 {
		msg += fmt.Sprintf(" with %d Blocks already received", c.receivedIndex)
	}
	log.Info(msg)
	c.requestID = &requestID
	ctx = context.WithValue(ctx, graphsync.RequestIDContextKey{}, *c.requestID)
	responseChan, errChan := c.gs.Request(ctx, dataSender, root, stor, exts...)
	c.state = channelOpen
	// Save a mapping from the graphsync key to the channel ID so that
	// subsequent graphsync callbacks are associated with this channel

	e := executor.NewExecutor(c.channelID, responseChan, errChan, onComplete)
	return e, nil
}

func waitForCompleteHook(ctx context.Context, completed chan struct{}) error {
	// Wait for the cancel to propagate through to graphsync, and for
	// the graphsync request to complete
	select {
	case <-completed:
		return nil
	case <-time.After(maxGSCancelWait):
		// Fail-safe: give up waiting after a certain amount of time
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// gsReqOpened is called when graphsync makes a request to the remote peer to ask for data
func (c *Channel) GsReqOpened(sender peer.ID, requestID graphsync.RequestID, hookActions graphsync.OutgoingRequestHookActions) {
	// Tell graphsync to store the received blocks in the registered store
	if c.hasStore() {
		hookActions.UsePersistenceOption("data-transfer-" + c.channelID.String())
	}
	log.Infow("outgoing graphsync request", "peer", sender, "graphsync request id", requestID, "data transfer channel id", c.channelID)
}

// gsDataRequestRcvd is called when the transport receives an incoming request
// for data.
func (c *Channel) GsDataRequestRcvd(sender peer.ID, requestID graphsync.RequestID, chst datatransfer.ChannelState, hookActions graphsync.IncomingRequestHookActions) {
	c.lk.Lock()
	defer c.lk.Unlock()
	log.Debugf("%s: received request for data, req_id=%d", c.channelID, requestID)
	// If the requester had previously cancelled their request, send any
	// message that was queued since the cancel
	if c.requesterCancelled {
		c.requesterCancelled = false

		extensions := c.pendingExtensions
		c.pendingExtensions = nil
		for _, ext := range extensions {
			hookActions.SendExtensionData(ext)
		}
	}

	// Tell graphsync to load blocks from the registered store
	if c.hasStore() {
		hookActions.UsePersistenceOption("data-transfer-" + c.channelID.String())
	}

	// Save a mapping from the graphsync key to the channel ID so that
	// subsequent graphsync callbacks are associated with this channel
	c.requestID = &requestID
	log.Infow("incoming graphsync request", "peer", sender, "graphsync request id", requestID, "data transfer channel id", c.channelID)

	c.state = channelOpen

	err := c.updateFromChannelState(chst)
	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	action := c.actionFromChannelState(chst)
	log.Infof(string(action))
	switch action {
	case Pause:
		c.state = channelPaused
		hookActions.PauseResponse()
	case Close:
		c.state = channelClosed
		hookActions.TerminateWithError(datatransfer.ErrRejected)
		return
	default:
	}
	hookActions.ValidateRequest()
}

func (c *Channel) MarkPaused() {
	c.lk.Lock()
	defer c.lk.Unlock()
	c.state = channelPaused
}

func (c *Channel) Paused() bool {
	c.lk.RLock()
	defer c.lk.RUnlock()
	return c.state == channelPaused
}

func (c *Channel) Pause(ctx context.Context) error {
	c.lk.Lock()
	defer c.lk.Unlock()

	// Check if the channel was already cancelled
	if c.requestID == nil {
		log.Debugf("%s: channel was cancelled so not pausing channel", c.channelID)
		return nil
	}

	if c.state != channelOpen {
		log.Debugf("%s: channel is not open so not pausing channel", c.channelID)
		return nil
	}

	c.state = channelPaused

	// If the requester cancelled, bail out
	if c.requesterCancelled {
		log.Debugf("%s: requester has cancelled so not pausing response for now", c.channelID)
		return nil
	}

	// Pause the response
	log.Debugf("%s: pausing response", c.channelID)
	return c.gs.Pause(ctx, *c.requestID)
}

func (c *Channel) Resume(ctx context.Context, extensions []graphsync.ExtensionData) error {
	c.lk.Lock()
	defer c.lk.Unlock()

	// Check if the channel was already cancelled
	if c.requestID == nil {
		log.Debugf("%s: channel was cancelled so not resuming channel", c.channelID)
		return nil
	}
	if c.state != channelPaused {
		log.Debugf("%s: channel is not paused so not resuming channel", c.channelID)
		return nil
	}

	c.state = channelOpen

	// If the requester cancelled, bail out
	if c.requesterCancelled {
		// If there was an associated message, we still want to send it to the
		// remote peer. We're not sending any message now, so instead queue up
		// the message to be sent next time the peer makes a request to us.
		c.pendingExtensions = append(c.pendingExtensions, extensions...)

		log.Debugf("%s: requester has cancelled so not unpausing for now", c.channelID)
		return nil
	}

	log.Debugf("%s: unpausing response", c.channelID)
	return c.gs.Unpause(ctx, *c.requestID, extensions...)
}

type Action string

const (
	NoAction Action = ""
	Close    Action = "close"
	Pause    Action = "pause"
	Resume   Action = "resume"
)

// UpdateFromChannelState updates internal graphsync channel state form a datatransfer
// channel state
func (c *Channel) UpdateFromChannelState(chst datatransfer.ChannelState) error {
	c.lk.Lock()
	defer c.lk.Unlock()
	return c.updateFromChannelState(chst)
}

func (c *Channel) updateFromChannelState(chst datatransfer.ChannelState) error {
	// read the sent value
	sentNode := chst.SentIndex()
	if !sentNode.IsNull() {
		sentIndex, err := sentNode.AsInt()
		if err != nil {
			return err
		}
		if sentIndex > c.sentIndex {
			c.sentIndex = sentIndex
		}
	}

	// read the received
	receivedNode := chst.ReceivedIndex()
	if !receivedNode.IsNull() {
		receivedIndex, err := receivedNode.AsInt()
		if err != nil {
			return err
		}
		if receivedIndex > c.receivedIndex {
			c.receivedIndex = receivedIndex
		}
	}

	// read the queued
	queuedNode := chst.QueuedIndex()
	if !queuedNode.IsNull() {
		queuedIndex, err := queuedNode.AsInt()
		if err != nil {
			return err
		}
		if queuedIndex > c.queuedIndex {
			c.queuedIndex = queuedIndex
		}
	}

	// set progress
	var progress uint64
	if chst.Sender() == chst.SelfPeer() {
		progress = chst.Queued()
	} else {
		progress = chst.Received()
	}
	if progress > c.progress {
		c.progress = progress
	}

	// set data limit
	c.dataLimit = chst.DataLimit()
	return nil
}

// ActionFromChannelState comparse internal graphsync channel state with the data transfer
// state and determines what if any action should be taken on graphsync
func (c *Channel) ActionFromChannelState(chst datatransfer.ChannelState) Action {
	c.lk.Lock()
	defer c.lk.Unlock()
	return c.actionFromChannelState(chst)
}

func (c *Channel) actionFromChannelState(chst datatransfer.ChannelState) Action {
	// if the state is closed, and we haven't closed, we need to close
	if !c.requesterCancelled && c.state != channelClosed && chst.TransferClosed() {
		if chst.ChannelID().Initiator == chst.SelfPeer() {
			log.Infof("closing initiator")
		} else {
			log.Infof("closing responder")
		}
		return Close
	}

	// if the state is running, and we're paused, we need to pause
	if c.requestID != nil && c.state == channelPaused && !chst.SelfPaused() {
		return Resume
	}

	// if the state is paused, and the transfer is running, we need to resume
	if c.requestID != nil && c.state == channelOpen && chst.SelfPaused() {
		return Pause
	}

	return NoAction
}

func (c *Channel) ReconcileChannelState(chst datatransfer.ChannelState) (Action, error) {
	c.lk.Lock()
	defer c.lk.Unlock()
	err := c.updateFromChannelState(chst)
	if err != nil {
		return NoAction, err
	}
	return c.actionFromChannelState(chst), nil
}

func (c *Channel) MarkTransferComplete() {
	c.lk.Lock()
	defer c.lk.Unlock()
	c.state = channelClosed
}

// Called when the responder gets a cancel message from the requester
func (c *Channel) OnRequesterCancelled() {
	c.lk.Lock()
	defer c.lk.Unlock()

	c.requesterCancelled = true
}

func (c *Channel) hasStore() bool {
	c.storeLk.RLock()
	defer c.storeLk.RUnlock()

	return c.storeRegistered
}

// Use the given loader and storer to get / put blocks for the data-transfer.
// Note that each data-transfer channel uses a separate blockstore.
func (c *Channel) UseStore(lsys ipld.LinkSystem) error {
	c.storeLk.Lock()
	defer c.storeLk.Unlock()

	// Register the channel's store with graphsync
	err := c.gs.RegisterPersistenceOption("data-transfer-"+c.channelID.String(), lsys)
	if err != nil {
		return err
	}

	c.storeRegistered = true

	return nil
}

func (c *Channel) UpdateReceivedIndexIfGreater(nextIdx int64) bool {
	c.lk.Lock()
	defer c.lk.Unlock()
	if c.receivedIndex < nextIdx {
		c.receivedIndex = nextIdx
		return true
	}
	return false
}

func (c *Channel) UpdateQueuedIndexIfGreater(nextIdx int64) bool {
	c.lk.Lock()
	defer c.lk.Unlock()
	if c.queuedIndex < nextIdx {
		c.queuedIndex = nextIdx
		return true
	}
	return false
}

func (c *Channel) UpdateSentIndexIfGreater(nextIdx int64) bool {
	c.lk.Lock()
	defer c.lk.Unlock()
	if c.sentIndex < nextIdx {
		c.sentIndex = nextIdx
		return true
	}
	return false
}

func (c *Channel) UpdateProgress(additionalData uint64) bool {
	c.lk.Lock()
	defer c.lk.Unlock()
	c.progress += additionalData
	reachedLimit := c.dataLimit != 0 && c.progress >= c.dataLimit
	if reachedLimit {
		c.state = channelPaused
	}
	return reachedLimit
}

func (c *Channel) Cleanup() {
	c.lk.Lock()
	defer c.lk.Unlock()

	log.Debugf("%s: cleaning up channel", c.channelID)

	if c.hasStore() {
		// Unregister the channel's store from graphsync
		opt := "data-transfer-" + c.channelID.String()
		err := c.gs.UnregisterPersistenceOption(opt)
		if err != nil {
			log.Errorf("failed to unregister persistence option %s: %s", opt, err)
		}
	}

}

func (c *Channel) Close(ctx context.Context) error {
	debug.PrintStack()
	// Cancel the graphsync request
	c.lk.Lock()
	errch := c.cancel(ctx)
	c.lk.Unlock()

	// Wait for the cancel message to complete
	select {
	case err := <-errch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// cancel the graphsync request.
// Note: must be called under the lock.
func (c *Channel) cancel(ctx context.Context) chan error {
	errch := make(chan error, 1)

	// Check that the request has not already been cancelled
	if c.requesterCancelled || c.state == channelClosed {
		errch <- nil
		return errch
	}

	// Clear the graphsync key to indicate that the request has been cancelled
	requestID := c.requestID
	c.requestID = nil
	c.state = channelClosed
	go func() {
		log.Debugf("%s: cancelling request", c.channelID)
		err := c.gs.Cancel(ctx, *requestID)

		// Ignore "request not found" errors
		if err != nil && !errors.Is(graphsync.RequestNotFoundErr{}, err) {
			errch <- fmt.Errorf("cancelling graphsync request for channel %s: %w", c.channelID, err)
		} else {
			errch <- nil
		}
	}()

	return errch
}

func (c *Channel) IsCurrentRequest(requestID graphsync.RequestID) bool {
	return c.requestID != nil && *c.requestID == requestID
}
