package graphsync

import (
	"errors"

	"github.com/ipfs/go-graphsync"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
)

// gsOutgoingRequestHook is called when a graphsync request is made
func (t *Transport) gsOutgoingRequestHook(p peer.ID, request graphsync.RequestData, hookActions graphsync.OutgoingRequestHookActions) {
	message, _ := extension.GetTransferData(request, t.supportedExtensions)

	// extension not found; probably not our request.
	if message == nil {
		return
	}

	// A graphsync request is made when either
	// - The local node opens a data-transfer pull channel, so the local node
	//   sends a graphsync request to ask the remote peer for the data
	// - The remote peer opened a data-transfer push channel, and in response
	//   the local node sends a graphsync request to ask for the data
	var initiator peer.ID
	var responder peer.ID
	if message.IsRequest() {
		// This is a pull request so the data-transfer initiator is the local node
		initiator = t.peerID
		responder = p
	} else {
		// This is a push response so the data-transfer initiator is the remote
		// peer: They opened the push channel, we respond by sending a
		// graphsync request for the data
		initiator = p
		responder = t.peerID
	}
	chid := datatransfer.ChannelID{Initiator: initiator, Responder: responder, ID: message.TransferID()}

	// A data transfer channel was opened
	err := t.events.OnChannelOpened(chid)
	if err != nil {
		// There was an error opening the channel, bail out
		log.Errorf("processing OnChannelOpened for %s: %s", chid, err)
		t.CleanupChannel(chid)
		return
	}

	// Start tracking the channel if we're not already
	ch := t.trackDTChannel(chid)

	// Signal that the channel has been opened
	ch.GsReqOpened(p, request.ID(), hookActions)
}

// gsIncomingBlockHook is called when a block is received
func (t *Transport) gsIncomingBlockHook(p peer.ID, response graphsync.ResponseData, block graphsync.BlockData, hookActions graphsync.IncomingBlockHookActions) {
	chid, ok := t.requestIDToChannelID.load(response.RequestID())
	if !ok {
		return
	}

	ch, err := t.getDTChannel(chid)
	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	ch.UpdateReceivedCidsIfGreater(block.Index())

	err = t.events.OnDataReceived(chid, block.Link(), block.BlockSize(), block.Index(), block.BlockSizeOnWire() != 0)
	if err != nil && err != datatransfer.ErrPause {
		hookActions.TerminateWithError(err)
		return
	}

	if err == datatransfer.ErrPause {
		ch.MarkPaused()
		hookActions.PauseRequest()
	}
}

func (t *Transport) gsBlockSentHook(p peer.ID, request graphsync.RequestData, block graphsync.BlockData) {
	// When a data transfer is restarted, the requester sends a list of CIDs
	// that it already has. Graphsync calls the sent hook for all blocks even
	// if they are in the list (meaning, they aren't actually sent over the
	// wire). So here we check if the block was actually sent
	// over the wire before firing the data sent event.
	if block.BlockSizeOnWire() == 0 {
		return
	}

	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	if err := t.events.OnDataSent(chid, block.Link(), block.BlockSize(), block.Index(), block.BlockSizeOnWire() != 0); err != nil {
		log.Errorf("failed to process data sent: %+v", err)
	}
}

func (t *Transport) gsOutgoingBlockHook(p peer.ID, request graphsync.RequestData, block graphsync.BlockData, hookActions graphsync.OutgoingBlockHookActions) {
	// When a data transfer is restarted, the requester sends a list of CIDs
	// that it already has. Graphsync calls the outgoing block hook for all
	// blocks even if they are in the list (meaning, they aren't actually going
	// to be sent over the wire). So here we check if the block is actually
	// going to be sent over the wire before firing the data queued event.
	if block.BlockSizeOnWire() == 0 {
		return
	}

	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	ch, err := t.getDTChannel(chid)
	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	// OnDataQueued is called when a block is queued to be sent to the remote
	// peer. It can return ErrPause to pause the response (eg if payment is
	// required) and it can return a message that will be sent with the block
	// (eg to ask for payment).
	msg, err := t.events.OnDataQueued(chid, block.Link(), block.BlockSize(), block.Index(), block.BlockSizeOnWire() != 0)
	if err != nil && err != datatransfer.ErrPause {
		hookActions.TerminateWithError(err)
		return
	}

	if err == datatransfer.ErrPause {
		ch.MarkPaused()
		hookActions.PauseResponse()
	}

	if msg != nil {
		// gsOutgoingBlockHook uses a unique extension name so it can be attached with data from a different hook
		// outgoingBlkExtensions also includes the default extension name so it remains compatible with all data-transfer protocol versions out there
		extensions, err := extension.ToExtensionData(msg, outgoingBlkExtensions)
		if err != nil {
			hookActions.TerminateWithError(err)
			return
		}
		for _, extension := range extensions {
			hookActions.SendExtensionData(extension)
		}
	}
}

// gsReqQueuedHook is called when graphsync enqueues an incoming request for data
func (t *Transport) gsReqQueuedHook(p peer.ID, request graphsync.RequestData, hookActions graphsync.RequestQueuedHookActions) {
	msg, err := extension.GetTransferData(request, t.supportedExtensions)
	if err != nil {
		log.Errorf("failed GetTransferData, req=%+v, err=%s", request, err)
	}
	// extension not found; probably not our request.
	if msg == nil {
		return
	}

	var chid datatransfer.ChannelID
	if msg.IsRequest() {
		// when a data transfer request comes in on graphsync, the remote peer
		// initiated a pull
		chid = datatransfer.ChannelID{ID: msg.TransferID(), Initiator: p, Responder: t.peerID}
		dtRequest := msg.(datatransfer.Request)
		if dtRequest.IsNew() {
			log.Infof("%s, pull request queued, req_id=%d", chid, request.ID())
			t.events.OnTransferQueued(chid)
		} else {
			log.Infof("%s, pull restart request queued, req_id=%d", chid, request.ID())
		}
	} else {
		// when a data transfer response comes in on graphsync, this node
		// initiated a push, and the remote peer responded with a request
		// for data
		chid = datatransfer.ChannelID{ID: msg.TransferID(), Initiator: t.peerID, Responder: p}
		response := msg.(datatransfer.Response)
		if response.IsNew() {
			log.Infof("%s, GS pull request queued in response to our push, req_id=%d", chid, request.ID())
			t.events.OnTransferQueued(chid)
		} else {
			log.Infof("%s, GS pull request queued in response to our restart push, req_id=%d", chid, request.ID())
		}
	}
	augmentContext := t.events.OnContextAugment(chid)
	if augmentContext != nil {
		hookActions.AugmentContext(augmentContext)
	}
}

// gsReqRecdHook is called when graphsync receives an incoming request for data
func (t *Transport) gsReqRecdHook(p peer.ID, request graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
	// if this is a push request the sender is us.
	msg, err := extension.GetTransferData(request, t.supportedExtensions)
	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	// extension not found; probably not our request.
	if msg == nil {
		return
	}

	// An incoming graphsync request for data is received when either
	// - The remote peer opened a data-transfer pull channel, so the local node
	//   receives a graphsync request for the data
	// - The local node opened a data-transfer push channel, and in response
	//   the remote peer sent a graphsync request for the data, and now the
	//   local node receives that request for data
	var chid datatransfer.ChannelID
	var responseMessage datatransfer.Message
	if msg.IsRequest() {
		// when a data transfer request comes in on graphsync, the remote peer
		// initiated a pull
		chid = datatransfer.ChannelID{ID: msg.TransferID(), Initiator: p, Responder: t.peerID}

		log.Debugf("%s: received request for data (pull), req_id=%d", chid, request.ID())

		request := msg.(datatransfer.Request)
		responseMessage, err = t.events.OnRequestReceived(chid, request)
	} else {
		// when a data transfer response comes in on graphsync, this node
		// initiated a push, and the remote peer responded with a request
		// for data
		chid = datatransfer.ChannelID{ID: msg.TransferID(), Initiator: t.peerID, Responder: p}

		log.Debugf("%s: received request for data (push), req_id=%d", chid, request.ID())

		response := msg.(datatransfer.Response)
		err = t.events.OnResponseReceived(chid, response)
	}

	// If we need to send a response, add the response message as an extension
	if responseMessage != nil {
		// gsReqRecdHook uses a unique extension name so it can be attached with data from a different hook
		// incomingReqExtensions also includes default extension name so it remains compatible with previous data-transfer
		// protocol versions out there.
		extensions, extensionErr := extension.ToExtensionData(responseMessage, incomingReqExtensions)
		if extensionErr != nil {
			hookActions.TerminateWithError(err)
			return
		}
		for _, extension := range extensions {
			hookActions.SendExtensionData(extension)
		}
	}

	if err != nil && err != datatransfer.ErrPause {
		hookActions.TerminateWithError(err)
		return
	}

	// Check if the callback indicated that the channel should be paused
	// immediately (eg because data is still being unsealed)
	paused := false
	if err == datatransfer.ErrPause {
		log.Debugf("%s: pausing graphsync response", chid)

		paused = true
		hookActions.PauseResponse()
	}
	ch := t.trackDTChannel(chid)
	t.requestIDToChannelID.set(request.ID(), true, chid)

	ch.GsDataRequestRcvd(p, request.ID(), paused, hookActions)

	hookActions.ValidateRequest()
}

// gsCompletedResponseListener is a graphsync.OnCompletedResponseListener. We use it learn when the data transfer is complete
// for the side that is responding to a graphsync request
func (t *Transport) gsCompletedResponseListener(p peer.ID, request graphsync.RequestData, status graphsync.ResponseStatusCode) {
	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	if status == graphsync.RequestCancelled {
		return
	}

	ch, err := t.getDTChannel(chid)
	if err != nil {
		return
	}
	ch.MarkTransferComplete()

	var completeErr error
	if status != graphsync.RequestCompletedFull {
		completeErr = xerrors.Errorf("graphsync response to peer %s did not complete: response status code %s", p, status.String())
	}

	// Used by the tests to listen for when a response completes
	if t.completedResponseListener != nil {
		t.completedResponseListener(chid)
	}

	err = t.events.OnChannelCompleted(chid, completeErr)
	if err != nil {
		log.Error(err)
	}

}

func (t *Transport) gsRequestUpdatedHook(p peer.ID, request graphsync.RequestData, update graphsync.RequestData, hookActions graphsync.RequestUpdatedHookActions) {
	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	responseMessage, err := t.processExtension(chid, update, p, t.supportedExtensions)

	if responseMessage != nil {
		extensions, extensionErr := extension.ToExtensionData(responseMessage, t.supportedExtensions)
		if extensionErr != nil {
			hookActions.TerminateWithError(err)
			return
		}
		for _, extension := range extensions {
			hookActions.SendExtensionData(extension)
		}
	}

	if err != nil && err != datatransfer.ErrPause {
		hookActions.TerminateWithError(err)
	}

}

// gsIncomingResponseHook is a graphsync.OnIncomingResponseHook. We use it to pass on responses
func (t *Transport) gsIncomingResponseHook(p peer.ID, response graphsync.ResponseData, hookActions graphsync.IncomingResponseHookActions) {
	chid, ok := t.requestIDToChannelID.load(response.RequestID())
	if !ok {
		return
	}
	responseMessage, err := t.processExtension(chid, response, p, incomingReqExtensions)

	if responseMessage != nil {
		extensions, extensionErr := extension.ToExtensionData(responseMessage, t.supportedExtensions)
		if extensionErr != nil {
			hookActions.TerminateWithError(err)
			return
		}
		for _, extension := range extensions {
			hookActions.UpdateRequestWithExtensions(extension)
		}
	}

	if err != nil {
		hookActions.TerminateWithError(err)
	}

	// In a case where the transfer sends blocks immediately this extension may contain both a
	// response message and a revalidation request so we trigger OnResponseReceived again for this
	// specific extension name
	_, err = t.processExtension(chid, response, p, []graphsync.ExtensionName{extension.ExtensionOutgoingBlock1_1})

	if err != nil {
		hookActions.TerminateWithError(err)
	}
}

func (t *Transport) processExtension(chid datatransfer.ChannelID, gsMsg extension.GsExtended, p peer.ID, exts []graphsync.ExtensionName) (datatransfer.Message, error) {

	// if this is a push request the sender is us.
	msg, err := extension.GetTransferData(gsMsg, exts)
	if err != nil {
		return nil, err
	}

	// extension not found; probably not our request.
	if msg == nil {
		return nil, nil
	}

	if msg.IsRequest() {

		// only accept request message updates when original message was also request
		if (chid != datatransfer.ChannelID{ID: msg.TransferID(), Initiator: p, Responder: t.peerID}) {
			return nil, errors.New("received request on response channel")
		}
		dtRequest := msg.(datatransfer.Request)
		return t.events.OnRequestReceived(chid, dtRequest)
	}

	// only accept response message updates when original message was also response
	if (chid != datatransfer.ChannelID{ID: msg.TransferID(), Initiator: t.peerID, Responder: p}) {
		return nil, errors.New("received response on request channel")
	}

	dtResponse := msg.(datatransfer.Response)
	return nil, t.events.OnResponseReceived(chid, dtResponse)
}

func (t *Transport) gsRequestorCancelledListener(p peer.ID, request graphsync.RequestData) {
	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	ch, err := t.getDTChannel(chid)
	if err != nil {
		if !xerrors.Is(datatransfer.ErrChannelNotFound, err) {
			log.Errorf("requestor cancelled: getting channel %s: %s", chid, err)
		}
		return
	}

	log.Debugf("%s: requester cancelled data-transfer", chid)
	ch.OnRequesterCancelled()
}

// Called when there is a graphsync error sending data
func (t *Transport) gsNetworkSendErrorListener(p peer.ID, request graphsync.RequestData, gserr error) {
	// Fire an error if the graphsync request was made by this node or the remote peer
	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	err := t.events.OnSendDataError(chid, gserr)
	if err != nil {
		log.Errorf("failed to fire transport send error %s: %s", gserr, err)
	}
}

// Called when there is a graphsync error receiving data
func (t *Transport) gsNetworkReceiveErrorListener(p peer.ID, gserr error) {
	// Fire a receive data error on all ongoing graphsync transfers with that
	// peer
	t.requestIDToChannelID.forEach(func(k graphsync.RequestID, sending bool, chid datatransfer.ChannelID) {
		if chid.Initiator != p && chid.Responder != p {
			return
		}

		err := t.events.OnReceiveDataError(chid, gserr)
		if err != nil {
			log.Errorf("failed to fire transport receive error %s: %s", gserr, err)
		}
	})
}
