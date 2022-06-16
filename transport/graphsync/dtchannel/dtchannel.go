package dtchannel

import (
	"context"
	"errors"
	"fmt"
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

	receivedCidsTotal int64
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
	if c.receivedCidsTotal > 0 {
		data := donotsendfirstblocks.EncodeDoNotSendFirstBlocks(c.receivedCidsTotal)
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
	if c.receivedCidsTotal > 0 {
		msg += fmt.Sprintf(" with %d Blocks already received", c.receivedCidsTotal)
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
func (c *Channel) GsDataRequestRcvd(sender peer.ID, requestID graphsync.RequestID, pauseRequest bool, hookActions graphsync.IncomingRequestHookActions) {
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

	if pauseRequest {
		c.state = channelPaused
		return
	}
	c.state = channelOpen
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

func (c *Channel) UpdateReceivedCidsIfGreater(nextIdx int64) {
	c.lk.Lock()
	defer c.lk.Unlock()
	if c.receivedCidsTotal < nextIdx {
		c.receivedCidsTotal = nextIdx
	}
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