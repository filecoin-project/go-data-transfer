package graphsync

import (
	"context"
	"fmt"
	"sync"
	"time"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
	"github.com/ipfs/go-graphsync"
	"github.com/ipfs/go-graphsync/donotsendfirstblocks"
	ipld "github.com/ipld/go-ipld-prime"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"
)

// Info needed to keep track of a data transfer channel
type dtChannel struct {
	channelID datatransfer.ChannelID
	t         *Transport

	lk                 sync.RWMutex
	isOpen             bool
	requestID          *graphsync.RequestID
	completed          chan struct{}
	requesterCancelled bool
	xferStarted        bool
	pendingExtensions  []graphsync.ExtensionData

	opened chan graphsync.RequestID

	storeLk         sync.RWMutex
	storeRegistered bool

	receivedCidsTotal int64
	transferComplete  bool
}

// Info needed to monitor an ongoing graphsync request
type gsReq struct {
	channelID    datatransfer.ChannelID
	responseChan <-chan graphsync.ResponseProgress
	errChan      <-chan error
	onComplete   func()
}

// Open a graphsync request for data to the remote peer
func (c *dtChannel) open(
	ctx context.Context,
	chid datatransfer.ChannelID,
	dataSender peer.ID,
	root ipld.Link,
	stor ipld.Node,
	exts []graphsync.ExtensionData,
) (*gsReq, error) {
	c.lk.Lock()
	defer c.lk.Unlock()

	// If there is an existing graphsync request for this channelID
	if c.requestID != nil {
		// Cancel the existing graphsync request
		completed := c.completed
		errch := c.cancel(ctx)

		// Wait for the complete callback to be called
		err := waitForCompleteHook(ctx, completed)
		if err != nil {
			return nil, xerrors.Errorf("%s: waiting for cancelled graphsync request to complete: %w", chid, err)
		}

		// Wait for the cancel request method to complete
		select {
		case err = <-errch:
		case <-ctx.Done():
			err = xerrors.Errorf("timed out waiting for graphsync request to be cancelled")
		}
		if err != nil {
			return nil, xerrors.Errorf("%s: restarting graphsync request: %w", chid, err)
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
			c.markTransferComplete()
			log.Infow("closing the completion ch for data-transfer channel", "chid", chid)
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
	responseChan, errChan := c.t.gs.Request(ctx, dataSender, root, stor, exts...)

	// Wait for graphsync "request opened" callback
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case requestID := <-c.opened:
		// Mark the channel as open and save the Graphsync request key
		c.isOpen = true
		c.requestID = &requestID
	}

	return &gsReq{
		channelID:    chid,
		responseChan: responseChan,
		errChan:      errChan,
		onComplete:   onComplete,
	}, nil
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
func (c *dtChannel) gsReqOpened(requestID graphsync.RequestID, hookActions graphsync.OutgoingRequestHookActions) {
	// Tell graphsync to store the received blocks in the registered store
	if c.hasStore() {
		hookActions.UsePersistenceOption("data-transfer-" + c.channelID.String())
	}
	log.Infow("outgoing graphsync request", "peer", c.channelID.OtherParty(c.t.peerID), "graphsync request id", requestID, "data transfer channel id", c.channelID)
	// Save a mapping from the graphsync key to the channel ID so that
	// subsequent graphsync callbacks are associated with this channel
	c.t.requestIDToChannelID.set(requestID, false, c.channelID)

	c.opened <- requestID
}

// gsDataRequestRcvd is called when the transport receives an incoming request
// for data.
// Note: Must be called under the lock.
func (c *dtChannel) gsDataRequestRcvd(requestID graphsync.RequestID, hookActions graphsync.IncomingRequestHookActions) {
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
	log.Infow("incoming graphsync request", "peer", c.channelID.OtherParty(c.t.peerID), "graphsync request id", requestID, "data transfer channel id", c.channelID)
	c.t.requestIDToChannelID.set(requestID, true, c.channelID)

	c.isOpen = true
}

func (c *dtChannel) pause(ctx context.Context) error {
	c.lk.Lock()
	defer c.lk.Unlock()

	// Check if the channel was already cancelled
	if c.requestID == nil {
		log.Debugf("%s: channel was cancelled so not pausing channel", c.channelID)
		return nil
	}

	// If the requester cancelled, bail out
	if c.requesterCancelled {
		log.Debugf("%s: requester has cancelled so not pausing response", c.channelID)
		return nil
	}

	// Pause the response
	log.Debugf("%s: pausing response", c.channelID)
	return c.t.gs.Pause(ctx, *c.requestID)
}

func (c *dtChannel) resumable() bool {
	c.lk.RLock()
	defer c.lk.RUnlock()
	return c.requestID != nil && !c.transferComplete
}

func (c *dtChannel) resume(ctx context.Context, msg datatransfer.Message) error {
	c.lk.Lock()
	defer c.lk.Unlock()

	// Check if the channel was already cancelled
	if c.requestID == nil {
		log.Debugf("%s: channel was cancelled so not resuming channel", c.channelID)
		return nil
	}

	var extensions []graphsync.ExtensionData
	if msg != nil {
		var err error
		extensions, err = extension.ToExtensionData(msg, c.t.supportedExtensions)
		if err != nil {
			return err
		}
	}

	// If the requester cancelled, bail out
	if c.requesterCancelled {
		// If there was an associated message, we still want to send it to the
		// remote peer. We're not sending any message now, so instead queue up
		// the message to be sent next time the peer makes a request to us.
		c.pendingExtensions = append(c.pendingExtensions, extensions...)

		log.Debugf("%s: requester has cancelled so not unpausing response", c.channelID)
		return nil
	}

	// Record that the transfer has started
	c.xferStarted = true

	log.Debugf("%s: unpausing response", c.channelID)
	return c.t.gs.Unpause(ctx, *c.requestID, extensions...)
}

func (c *dtChannel) markTransferComplete() {
	c.lk.Lock()
	defer c.lk.Unlock()
	c.transferComplete = true
}

func (c *dtChannel) close(ctx context.Context) error {
	var errch chan error
	c.lk.Lock()
	{
		// Check if the channel was already cancelled
		if c.requestID != nil {
			errch = c.cancel(ctx)
		}
	}
	c.lk.Unlock()

	// Wait for the cancel message to complete
	select {
	case err := <-errch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Called when the responder gets a cancel message from the requester
func (c *dtChannel) onRequesterCancelled() {
	c.lk.Lock()
	defer c.lk.Unlock()

	c.requesterCancelled = true
}

func (c *dtChannel) hasStore() bool {
	c.storeLk.RLock()
	defer c.storeLk.RUnlock()

	return c.storeRegistered
}

// Use the given loader and storer to get / put blocks for the data-transfer.
// Note that each data-transfer channel uses a separate blockstore.
func (c *dtChannel) useStore(lsys ipld.LinkSystem) error {
	c.storeLk.Lock()
	defer c.storeLk.Unlock()

	// Register the channel's store with graphsync
	err := c.t.gs.RegisterPersistenceOption("data-transfer-"+c.channelID.String(), lsys)
	if err != nil {
		return err
	}

	c.storeRegistered = true

	return nil
}

func (c *dtChannel) updateReceivedCidsIfGreater(nextIdx int64) {
	c.lk.Lock()
	defer c.lk.Unlock()
	if c.receivedCidsTotal < nextIdx {
		c.receivedCidsTotal = nextIdx
	}
}

func (c *dtChannel) cleanup() {
	c.lk.Lock()
	defer c.lk.Unlock()

	log.Debugf("%s: cleaning up channel", c.channelID)

	if c.hasStore() {
		// Unregister the channel's store from graphsync
		opt := "data-transfer-" + c.channelID.String()
		err := c.t.gs.UnregisterPersistenceOption(opt)
		if err != nil {
			log.Errorf("failed to unregister persistence option %s: %s", opt, err)
		}
	}

	// Clean up mapping from gs key to channel ID
	c.t.requestIDToChannelID.deleteRefs(c.channelID)
}

func (c *dtChannel) shutdown(ctx context.Context) error {
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

// Cancel the graphsync request.
// Note: must be called under the lock.
func (c *dtChannel) cancel(ctx context.Context) chan error {
	errch := make(chan error, 1)

	// Check that the request has not already been cancelled
	if c.requesterCancelled || c.requestID == nil {
		errch <- nil
		return errch
	}

	// Clear the graphsync key to indicate that the request has been cancelled
	requestID := c.requestID
	c.requestID = nil

	go func() {
		log.Debugf("%s: cancelling request", c.channelID)
		err := c.t.gs.Cancel(ctx, *requestID)

		// Ignore "request not found" errors
		if err != nil && !xerrors.Is(graphsync.RequestNotFoundErr{}, err) {
			errch <- xerrors.Errorf("cancelling graphsync request for channel %s: %w", c.channelID, err)
		} else {
			errch <- nil
		}
	}()

	return errch
}
