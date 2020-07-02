package graphsync

import (
	"bytes"
	"context"
	"errors"
	"sync"

	"github.com/filecoin-project/go-data-transfer/transport"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/filecoin-project/go-data-transfer/transport/graphsync/extension"
	"github.com/ipfs/go-graphsync"
	ipld "github.com/ipld/go-ipld-prime"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/prometheus/common/log"
)

type graphsyncKey struct {
	requestID graphsync.RequestID
	p         peer.ID
}

type responseProgress struct {
	currentSent uint64
	maximumSent uint64
}

// Transport manages graphsync hooks for data transfer, translating from
// graphsync hooks to semantic data transfer events
type Transport struct {
	events              transport.Events
	gs                  graphsync.GraphExchange
	peerID              peer.ID
	dataLock            sync.RWMutex
	graphsyncRequestMap map[graphsyncKey]datatransfer.ChannelID
	channelIDMap        map[datatransfer.ChannelID]graphsyncKey
	contextCancelMap    map[datatransfer.ChannelID]func()
	responseProgressMap map[datatransfer.ChannelID]*responseProgress
}

// NewTransport makes a new hooks manager with the given hook events interface
func NewTransport(peerID peer.ID, gs graphsync.GraphExchange) *Transport {
	return &Transport{
		gs:                  gs,
		peerID:              peerID,
		graphsyncRequestMap: make(map[graphsyncKey]datatransfer.ChannelID),
		contextCancelMap:    make(map[datatransfer.ChannelID]func()),
		channelIDMap:        make(map[datatransfer.ChannelID]graphsyncKey),
		responseProgressMap: make(map[datatransfer.ChannelID]*responseProgress),
	}
}

// OpenChannel initiates an outgoing request for the other peer to send data
// to us on this channel
// Note: from a data transfer symantic standpoint, it doesn't matter if the
// request is push or pull -- OpenChannel is called by the party that is
// intending to receive data
func (t *Transport) OpenChannel(ctx context.Context,
	dataSender peer.ID,
	channelID datatransfer.ChannelID,
	root ipld.Link,
	stor ipld.Node,
	msg message.DataTransferMessage) error {
	if t.events == nil {
		return transport.ErrHandlerNotSet
	}
	buf := new(bytes.Buffer)
	err := msg.ToNet(buf)
	if err != nil {
		return err
	}
	extData := buf.Bytes()
	internalCtx, internalCancel := context.WithCancel(ctx)
	t.dataLock.Lock()
	t.contextCancelMap[channelID] = internalCancel
	t.dataLock.Unlock()
	_, errChan := t.gs.Request(internalCtx, dataSender, root, stor,
		graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer,
			Data: extData,
		})
	go t.executeGsRequest(channelID, errChan)
	return nil
}

func (t *Transport) executeGsRequest(channelID datatransfer.ChannelID, errChan <-chan error) {
	var lastError error
	for err := range errChan {
		lastError = err
	}
	err := t.events.OnResponseCompleted(channelID, lastError == nil)
	if err != nil {
		log.Error(err)
	}
	t.dataLock.Lock()
	gsKey, ok := t.channelIDMap[channelID]
	delete(t.channelIDMap, channelID)
	delete(t.contextCancelMap, channelID)
	if ok {
		delete(t.graphsyncRequestMap, gsKey)
	}
	t.dataLock.Unlock()
}

// PauseChannel paused the given channel ID
func (t *Transport) PauseChannel(ctx context.Context,
	chid datatransfer.ChannelID,
) error {
	if t.events == nil {
		return transport.ErrHandlerNotSet
	}
	t.dataLock.RLock()
	gsKey, ok := t.channelIDMap[chid]
	t.dataLock.RUnlock()
	if !ok {
		return transport.ErrChannelNotFound
	}
	if gsKey.p == t.peerID {
		return t.gs.PauseRequest(gsKey.requestID)
	}
	return t.gs.PauseResponse(gsKey.p, gsKey.requestID)
}

// ResumeChannel resumes the given channel
func (t *Transport) ResumeChannel(ctx context.Context,
	msg message.DataTransferMessage,
	chid datatransfer.ChannelID,
) error {
	if t.events == nil {
		return transport.ErrHandlerNotSet
	}
	t.dataLock.RLock()
	gsKey, ok := t.channelIDMap[chid]
	t.dataLock.RUnlock()
	if !ok {
		return transport.ErrChannelNotFound
	}
	var extensions []graphsync.ExtensionData
	if msg != nil {
		buf := new(bytes.Buffer)
		err := msg.ToNet(buf)
		if err != nil {
			return err
		}
		extData := buf.Bytes()
		extensions = append(extensions, graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer,
			Data: extData,
		})
	}
	if gsKey.p == t.peerID {
		return t.gs.UnpauseRequest(gsKey.requestID, extensions...)
	}
	return t.gs.UnpauseResponse(gsKey.p, gsKey.requestID, extensions...)
}

// CloseChannel closes the given channel
func (t *Transport) CloseChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	if t.events == nil {
		return transport.ErrHandlerNotSet
	}
	t.dataLock.RLock()
	defer t.dataLock.RUnlock()
	gsKey, ok := t.channelIDMap[chid]
	if !ok {
		return transport.ErrChannelNotFound
	}
	if gsKey.p == t.peerID {
		cancelFn, ok := t.contextCancelMap[chid]
		if !ok {
			return transport.ErrChannelNotFound
		}
		cancelFn()
	} else {
		t.gs.CancelResponse(gsKey.p, gsKey.requestID)
	}
	return nil
}

// SetEventHandler sets the handler for events on channels
func (t *Transport) SetEventHandler(events transport.Events) error {
	if t.events != nil {
		return transport.ErrHandlerAlreadySet
	}
	t.events = events
	t.gs.RegisterIncomingRequestHook(t.gsReqRecdHook)
	t.gs.RegisterCompletedResponseListener(t.gsCompletedResponseListener)
	t.gs.RegisterIncomingBlockHook(t.gsIncomingBlockHook)
	t.gs.RegisterOutgoingBlockHook(t.gsOutgoingBlockHook)
	t.gs.RegisterOutgoingRequestHook(t.gsOutgoingRequestHook)
	t.gs.RegisterIncomingResponseHook(t.gsIncomingResponseHook)
	t.gs.RegisterRequestUpdatedHook(t.gsRequestUpdatedHook)
	return nil
}

func (t *Transport) gsOutgoingRequestHook(p peer.ID, request graphsync.RequestData, hookActions graphsync.OutgoingRequestHookActions) {
	message, _ := extension.GetTransferData(request)

	// extension not found; probably not our request.
	if message == nil {
		return
	}

	var initiator peer.ID
	if message.IsRequest() {
		initiator = t.peerID
	} else {
		initiator = p
	}
	chid := datatransfer.ChannelID{Initiator: initiator, ID: message.TransferID()}
	err := t.events.OnChannelOpened(chid)
	if err != nil {
		return
	}
	// record the outgoing graphsync request to map it to channel ID going forward
	t.dataLock.Lock()
	t.graphsyncRequestMap[graphsyncKey{request.ID(), t.peerID}] = chid
	t.channelIDMap[chid] = graphsyncKey{request.ID(), t.peerID}
	t.dataLock.Unlock()
}

func (t *Transport) gsIncomingBlockHook(p peer.ID, response graphsync.ResponseData, block graphsync.BlockData, hookActions graphsync.IncomingBlockHookActions) {
	t.dataLock.RLock()
	chid, ok := t.graphsyncRequestMap[graphsyncKey{response.RequestID(), t.peerID}]
	t.dataLock.RUnlock()

	if !ok {
		return
	}

	var extensions []graphsync.ExtensionData
	msg, err := t.events.OnDataReceived(chid, block.Link(), block.BlockSize())
	if err != nil && err != transport.ErrPause {
		hookActions.TerminateWithError(err)
		return
	}

	if err == transport.ErrPause {
		hookActions.PauseRequest()
	}

	if msg != nil {
		extension, err := extension.ToExtensionData(msg)
		if err != nil {
			hookActions.TerminateWithError(err)
			return
		}
		extensions = append(extensions, extension)
	}

	if len(extensions) > 0 {
		hookActions.UpdateRequestWithExtensions(extensions...)
	}
}

func (t *Transport) gsOutgoingBlockHook(p peer.ID, request graphsync.RequestData, block graphsync.BlockData, hookActions graphsync.OutgoingBlockHookActions) {
	t.dataLock.RLock()
	chid, ok := t.graphsyncRequestMap[graphsyncKey{request.ID(), p}]
	if !ok {
		t.dataLock.RUnlock()
		return
	}
	rp := t.responseProgressMap[chid]
	t.dataLock.RUnlock()
	rp.currentSent += block.BlockSize()
	if rp.currentSent <= rp.maximumSent {
		return
	}
	rp.maximumSent = rp.currentSent

	msg, err := t.events.OnDataSent(chid, block.Link(), block.BlockSize())
	if err != nil && err != transport.ErrPause {
		hookActions.TerminateWithError(err)
		return
	}

	if err == transport.ErrPause {
		hookActions.PauseResponse()
	}

	if msg != nil {
		extension, err := extension.ToExtensionData(msg)
		if err != nil {
			hookActions.TerminateWithError(err)
			return
		}
		hookActions.SendExtensionData(extension)
	}
}

// gsReqRecdHook is a graphsync.OnRequestReceivedHook hook
// if an incoming request does not match a previous push request, it returns an error.
func (t *Transport) gsReqRecdHook(p peer.ID, request graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {

	// if this is a push request the sender is us.
	msg, err := extension.GetTransferData(request)
	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	// extension not found; probably not our request.
	if msg == nil {
		return
	}

	var chid datatransfer.ChannelID
	var responseMessage message.DataTransferMessage
	if msg.IsRequest() {
		// when a DT request comes in on graphsync, it's a pull
		chid = datatransfer.ChannelID{ID: msg.TransferID(), Initiator: p}
		request := msg.(message.DataTransferRequest)
		responseMessage, err = t.events.OnRequestReceived(chid, request)
	} else {
		// when a DT response comes in on graphsync, it's a push
		chid = datatransfer.ChannelID{ID: msg.TransferID(), Initiator: t.peerID}
		response := msg.(message.DataTransferResponse)
		err = t.events.OnResponseReceived(chid, response)
	}

	if responseMessage != nil {
		extension, extensionErr := extension.ToExtensionData(responseMessage)
		if extensionErr != nil {
			hookActions.TerminateWithError(err)
			return
		}
		hookActions.SendExtensionData(extension)
	}

	if err != nil && err != transport.ErrPause {
		hookActions.TerminateWithError(err)
		return
	}

	if err == transport.ErrPause {
		hookActions.PauseResponse()
	}

	t.dataLock.Lock()
	t.graphsyncRequestMap[graphsyncKey{request.ID(), p}] = chid
	t.channelIDMap[chid] = graphsyncKey{request.ID(), p}
	existing := t.responseProgressMap[chid]
	if existing != nil {
		existing.currentSent = 0
	} else {
		t.responseProgressMap[chid] = &responseProgress{}
	}
	t.dataLock.Unlock()

	hookActions.ValidateRequest()
}

// gsCompletedResponseListener is a graphsync.OnCompletedResponseListener. We use it learn when the data transfer is complete
// for the side that is responding to a graphsync request
func (t *Transport) gsCompletedResponseListener(p peer.ID, request graphsync.RequestData, status graphsync.ResponseStatusCode) {
	t.dataLock.RLock()
	chid, ok := t.graphsyncRequestMap[graphsyncKey{request.ID(), p}]
	t.dataLock.RUnlock()

	if !ok {
		return
	}

	success := status == graphsync.RequestCompletedFull
	err := t.events.OnResponseCompleted(chid, success)
	if err != nil {
		log.Error(err)
	}
	t.dataLock.Lock()
	delete(t.channelIDMap, chid)
	delete(t.graphsyncRequestMap, graphsyncKey{request.ID(), p})
	delete(t.responseProgressMap, chid)
	t.dataLock.Unlock()
}

func (t *Transport) gsRequestUpdatedHook(p peer.ID, request graphsync.RequestData, update graphsync.RequestData, hookActions graphsync.RequestUpdatedHookActions) {

	t.dataLock.RLock()
	chid, ok := t.graphsyncRequestMap[graphsyncKey{request.ID(), p}]
	t.dataLock.RUnlock()

	if !ok {
		return
	}

	responseMessage, err := t.processExtension(chid, update, p)

	if responseMessage != nil {
		extension, extensionErr := extension.ToExtensionData(responseMessage)
		if extensionErr != nil {
			hookActions.TerminateWithError(err)
			return
		}
		hookActions.SendExtensionData(extension)
	}

	if err != nil {
		hookActions.TerminateWithError(err)
	}

}

// gsIncomingResponseHook is a graphsync.OnIncomingResponseHook. We use it to pass on responses
func (t *Transport) gsIncomingResponseHook(p peer.ID, response graphsync.ResponseData, hookActions graphsync.IncomingResponseHookActions) {

	t.dataLock.RLock()
	chid, ok := t.graphsyncRequestMap[graphsyncKey{response.RequestID(), t.peerID}]
	t.dataLock.RUnlock()

	if !ok {
		return
	}

	responseMessage, err := t.processExtension(chid, response, p)

	if responseMessage != nil {
		extension, extensionErr := extension.ToExtensionData(responseMessage)
		if extensionErr != nil {
			hookActions.TerminateWithError(err)
			return
		}
		hookActions.UpdateRequestWithExtensions(extension)
	}

	if err != nil {
		hookActions.TerminateWithError(err)
	}
}

func (t *Transport) processExtension(chid datatransfer.ChannelID, gsMsg extension.GsExtended, p peer.ID) (message.DataTransferMessage, error) {

	// if this is a push request the sender is us.
	msg, err := extension.GetTransferData(gsMsg)
	if err != nil {
		return nil, err
	}

	// extension not found; probably not our request.
	if msg == nil {
		return nil, nil
	}

	if msg.IsRequest() {

		// only accept request message updates when original message was also request
		if (chid != datatransfer.ChannelID{ID: msg.TransferID(), Initiator: p}) {
			return nil, errors.New("received request on response channel")
		}
		dtRequest := msg.(message.DataTransferRequest)
		return t.events.OnRequestReceived(chid, dtRequest)
	}

	// only accept response message updates when original message was also response
	if (chid != datatransfer.ChannelID{ID: msg.TransferID(), Initiator: t.peerID}) {
		return nil, errors.New("received response on request channel")
	}

	dtResponse := msg.(message.DataTransferResponse)
	return nil, t.events.OnResponseReceived(chid, dtResponse)
}
