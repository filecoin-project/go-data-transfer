package graphsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/ipfs/go-graphsync"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/dtchannel"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
)

// gsOutgoingRequestHook is called when a graphsync request is made
func (t *Transport) gsOutgoingRequestHook(p peer.ID, request graphsync.RequestData, hookActions graphsync.OutgoingRequestHookActions) {

	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	// Start tracking the channel if we're not already
	ch, err := t.getDTChannel(chid)

	if err != nil {
		// There was an error opening the channel, bail out
		log.Errorf("processing OnChannelOpened for %s: %s", chid, err)
		t.CleanupChannel(chid)
		return
	}

	// A data transfer channel was opened
	t.events.OnTransportEvent(chid, datatransfer.TransportOpenedChannel{})

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

	if ch.UpdateReceivedIndexIfGreater(block.Index()) && block.BlockSizeOnWire() != 0 {

		t.events.OnTransportEvent(chid, datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})

		if ch.UpdateProgress(block.BlockSizeOnWire()) {
			t.events.OnTransportEvent(chid, datatransfer.TransportReachedDataLimit{})
			hookActions.PauseRequest()
		}
	}

}

func (t *Transport) gsBlockSentListener(p peer.ID, request graphsync.RequestData, block graphsync.BlockData) {
	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	ch, err := t.getDTChannel(chid)
	if err != nil {
		log.Errorf("sent hook error: %s, for channel %s", err, chid)
		return
	}

	if ch.UpdateSentIndexIfGreater(block.Index()) && block.BlockSizeOnWire() != 0 {
		t.events.OnTransportEvent(chid, datatransfer.TransportSentData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
	}
}

func (t *Transport) gsOutgoingBlockHook(p peer.ID, request graphsync.RequestData, block graphsync.BlockData, hookActions graphsync.OutgoingBlockHookActions) {

	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	ch, err := t.getDTChannel(chid)
	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	if ch.UpdateQueuedIndexIfGreater(block.Index()) && block.BlockSizeOnWire() != 0 {
		t.events.OnTransportEvent(chid, datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})

		if ch.UpdateProgress(block.BlockSizeOnWire()) {
			t.events.OnTransportEvent(chid, datatransfer.TransportReachedDataLimit{})
			hookActions.PauseResponse()
		}
	}
}

// gsReqQueuedHook is called when graphsync enqueues an incoming request for data
func (t *Transport) gsRequestProcessingListener(p peer.ID, request graphsync.RequestData, requestCount int) {

	chid, ok := t.requestIDToChannelID.load(request.ID())
	if !ok {
		return
	}

	t.events.OnTransportEvent(chid, datatransfer.TransportInitiatedTransfer{})
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

		// graphsync never receives dt push requests as new graphsync requests -- is so, we should error
		isNewOrRestart := (request.IsNew() || request.IsRestart())
		if isNewOrRestart && !request.IsPull() {
			hookActions.TerminateWithError(datatransfer.ErrUnsupported)
			return
		}

		responseMessage, err = t.events.OnRequestReceived(chid, request)

		// if we're going to accept this new/restart request, protect connection
		if isNewOrRestart && err == nil {
			t.dtNet.Protect(p, chid.String())
		}

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

	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	hookActions.AugmentContext(t.events.OnContextAugment(chid))

	chst, err := t.events.ChannelState(context.TODO(), chid)
	if err != nil {
		hookActions.TerminateWithError(err)
	}

	var ch *dtchannel.Channel
	if msg.IsRequest() {
		ch = t.trackDTChannel(chid)
	} else {
		ch, err = t.getDTChannel(chid)
		if err != nil {
			hookActions.TerminateWithError(err)
			return
		}
	}
	t.requestIDToChannelID.set(request.ID(), true, chid)
	ch.GsDataRequestRcvd(p, request.ID(), chst, hookActions)
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

	var completeEvent datatransfer.TransportCompletedTransfer
	if status == graphsync.RequestCompletedFull {
		completeEvent.Success = true
	} else {
		completeEvent.ErrorMessage = fmt.Sprintf("graphsync response to peer %s did not complete: response status code %s", p, status.String())
	}

	// Used by the tests to listen for when a response completes
	if t.completedResponseListener != nil {
		t.completedResponseListener(chid)
	}

	t.events.OnTransportEvent(chid, completeEvent)

}

// gsIncomingResponseHook is a graphsync.OnIncomingResponseHook. We use it to pass on responses
func (t *Transport) gsIncomingResponseHook(p peer.ID, response graphsync.ResponseData, hookActions graphsync.IncomingResponseHookActions) {
	chid, ok := t.requestIDToChannelID.load(response.RequestID())
	if !ok {
		return
	}
	responseMessage, err := t.processExtension(chid, response, p, incomingReqExtensions)

	if responseMessage != nil {
		t.dtNet.SendMessage(context.TODO(), p, transportID, responseMessage)
	}

	if err != nil {
		hookActions.TerminateWithError(err)
	}

	// In a case where the transfer sends blocks immediately this extension may contain both a
	// response message and a revalidation request so we trigger OnResponseReceived again for this
	// specific extension name
	responseMessage, err = t.processExtension(chid, response, p, outgoingBlkExtensions)

	if responseMessage != nil {
		t.dtNet.SendMessage(context.TODO(), p, transportID, responseMessage)
	}

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

	t.events.OnTransportEvent(chid, datatransfer.TransportErrorSendingData{ErrorMessage: gserr.Error()})
}

// Called when there is a graphsync error receiving data
func (t *Transport) gsNetworkReceiveErrorListener(p peer.ID, gserr error) {
	// Fire a receive data error on all ongoing graphsync transfers with that
	// peer
	t.requestIDToChannelID.forEach(func(k graphsync.RequestID, sending bool, chid datatransfer.ChannelID) {
		if chid.Initiator != p && chid.Responder != p {
			return
		}

		t.events.OnTransportEvent(chid, datatransfer.TransportErrorReceivingData{ErrorMessage: gserr.Error()})
	})
}
