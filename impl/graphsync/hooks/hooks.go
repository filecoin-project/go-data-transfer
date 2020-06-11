package hooks

import (
	"errors"
	"sync"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/impl/graphsync/extension"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/ipfs/go-graphsync"
	ipld "github.com/ipld/go-ipld-prime"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/prometheus/common/log"
)

// ErrPause is a special error that the DataReceived / DataSent hooks can
// use to pause the channel
var ErrPause = errors.New("pause channel")

// ErrResume is a special error that the ResponseReceived / RequestReceived hooks can
// use to unpause the channel
var ErrResume = errors.New("resume channel")

// Events are semantic data transfer events that happen as a result of graphsync hooks
type Events interface {
	// OnChannelOpened is called when we ask the other peer to send us data on the
	// given channel ID
	// return values are:
	// - nil = this channel is recognized
	// - error = ignore incoming data for this channel
	OnChannelOpened(chid datatransfer.ChannelID) error
	// OnResponseReceived is called when we receive a response to a request
	// - nil = continue receiving data
	// - error = cancel this request
	OnResponseReceived(chid datatransfer.ChannelID, msg message.DataTransferResponse) error
	// OnDataReceive is called when we receive data for the given channel ID
	// return values are:
	// - nil = continue receiving data
	// - error = cancel this request
	OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error)
	// OnDataSent is called when we send data for the given channel ID
	// return values are:
	// - nil = continue sending data
	// - error = cancel this request
	OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error)
	// OnRequestReceived is called when we receive a new request to send data
	// for the given channel ID
	// return values are:
	// - nil = proceed with sending data
	// - error = cancel this request
	OnRequestReceived(chid datatransfer.ChannelID, msg message.DataTransferRequest) (message.DataTransferResponse, error)
	// OnResponseCompleted is called when we finish sending data for the given channel ID
	// Error returns are logged but otherwise have not effect
	OnResponseCompleted(chid datatransfer.ChannelID, success bool) error
}

type graphsyncKey struct {
	requestID graphsync.RequestID
	p         peer.ID
}

// Manager manages graphsync hooks for data transfer, translating from
// graphsync hooks to semantic data transfer events
type Manager struct {
	events                Events
	peerID                peer.ID
	graphsyncRequestMapLk sync.RWMutex
	graphsyncRequestMap   map[graphsyncKey]datatransfer.ChannelID
}

// NewManager makes a new hooks manager with the given hook events interface
func NewManager(peerID peer.ID, hookEvents Events) *Manager {
	return &Manager{
		events:              hookEvents,
		peerID:              peerID,
		graphsyncRequestMap: make(map[graphsyncKey]datatransfer.ChannelID),
	}
}

// RegisterHooks registers graphsync hooks for the hooks manager
func (hm *Manager) RegisterHooks(gs graphsync.GraphExchange) {
	gs.RegisterIncomingRequestHook(hm.gsReqRecdHook)
	gs.RegisterCompletedResponseListener(hm.gsCompletedResponseListener)
	gs.RegisterIncomingBlockHook(hm.gsIncomingBlockHook)
	gs.RegisterOutgoingBlockHook(hm.gsOutgoingBlockHook)
	gs.RegisterOutgoingRequestHook(hm.gsOutgoingRequestHook)
	gs.RegisterIncomingResponseHook(hm.gsIncomingResponseHook)
	gs.RegisterRequestUpdatedHook(hm.gsRequestUpdatedHook)
}

func (hm *Manager) gsOutgoingRequestHook(p peer.ID, request graphsync.RequestData, hookActions graphsync.OutgoingRequestHookActions) {
	message, _ := extension.GetTransferData(request)

	// extension not found; probably not our request.
	if message == nil {
		return
	}

	var initiator peer.ID
	if message.IsRequest() {
		initiator = hm.peerID
	} else {
		initiator = p
	}
	chid := datatransfer.ChannelID{Initiator: initiator, ID: message.TransferID()}
	err := hm.events.OnChannelOpened(chid)
	if err != nil {
		return
	}
	// record the outgoing graphsync request to map it to channel ID going forward
	hm.graphsyncRequestMapLk.Lock()
	hm.graphsyncRequestMap[graphsyncKey{request.ID(), hm.peerID}] = chid
	hm.graphsyncRequestMapLk.Unlock()
}

func (hm *Manager) gsIncomingBlockHook(p peer.ID, response graphsync.ResponseData, block graphsync.BlockData, hookActions graphsync.IncomingBlockHookActions) {
	hm.graphsyncRequestMapLk.RLock()
	chid, ok := hm.graphsyncRequestMap[graphsyncKey{response.RequestID(), hm.peerID}]
	hm.graphsyncRequestMapLk.RUnlock()

	if !ok {
		return
	}

	var extensions []graphsync.ExtensionData
	msg, err := hm.events.OnDataReceived(chid, block.Link(), block.BlockSize())
	if err != nil {
		hookActions.TerminateWithError(err)
		return
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

func (hm *Manager) gsOutgoingBlockHook(p peer.ID, request graphsync.RequestData, block graphsync.BlockData, hookActions graphsync.OutgoingBlockHookActions) {
	hm.graphsyncRequestMapLk.RLock()
	chid, ok := hm.graphsyncRequestMap[graphsyncKey{request.ID(), p}]
	hm.graphsyncRequestMapLk.RUnlock()

	if !ok {
		return
	}

	msg, err := hm.events.OnDataSent(chid, block.Link(), block.BlockSize())
	if err != nil && err != ErrPause {
		hookActions.TerminateWithError(err)
		return
	}

	if err == ErrPause {
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
func (hm *Manager) gsReqRecdHook(p peer.ID, request graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {

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
		responseMessage, err = hm.events.OnRequestReceived(chid, request)
	} else {
		// when a DT response comes in on graphsync, it's a push
		chid = datatransfer.ChannelID{ID: msg.TransferID(), Initiator: hm.peerID}
		response := msg.(message.DataTransferResponse)
		err = hm.events.OnResponseReceived(chid, response)
	}

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
		return
	}

	hm.graphsyncRequestMapLk.Lock()
	hm.graphsyncRequestMap[graphsyncKey{request.ID(), p}] = chid
	hm.graphsyncRequestMapLk.Unlock()

	hookActions.ValidateRequest()
}

// gsCompletedResponseListener is a graphsync.OnCompletedResponseListener. We use it learn when the data transfer is complete
// for the side that is responding to a graphsync request
func (hm *Manager) gsCompletedResponseListener(p peer.ID, request graphsync.RequestData, status graphsync.ResponseStatusCode) {
	hm.graphsyncRequestMapLk.RLock()
	chid, ok := hm.graphsyncRequestMap[graphsyncKey{request.ID(), p}]
	hm.graphsyncRequestMapLk.RUnlock()

	if !ok {
		return
	}

	success := status == graphsync.RequestCompletedFull
	err := hm.events.OnResponseCompleted(chid, success)
	if err != nil {
		log.Error(err)
	}
}

func (hm *Manager) gsRequestUpdatedHook(p peer.ID, request graphsync.RequestData, update graphsync.RequestData, hookActions graphsync.RequestUpdatedHookActions) {

	hm.graphsyncRequestMapLk.RLock()
	chid, ok := hm.graphsyncRequestMap[graphsyncKey{request.ID(), p}]
	hm.graphsyncRequestMapLk.RUnlock()

	if !ok {
		return
	}

	msg, err := extension.GetTransferData(update)
	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	// extension not found; probably not our request.
	if msg == nil {
		return
	}

	var responseMessage message.DataTransferMessage
	if msg.IsRequest() {
		// only accept request message updates when original message was also request
		if (chid != datatransfer.ChannelID{ID: msg.TransferID(), Initiator: p}) {
			err = errors.New("received request on response channel")
		} else {
			dtRequest := msg.(message.DataTransferRequest)
			responseMessage, err = hm.events.OnRequestReceived(chid, dtRequest)
		}
	} else {
		// only accept response message updates when original message was also response
		if (chid != datatransfer.ChannelID{ID: msg.TransferID(), Initiator: hm.peerID}) {
			err = errors.New("received response on request channel")
		} else {
			dtResponse := msg.(message.DataTransferResponse)
			err = hm.events.OnResponseReceived(chid, dtResponse)
		}
	}

	if responseMessage != nil {
		extension, extensionErr := extension.ToExtensionData(responseMessage)
		if extensionErr != nil {
			hookActions.TerminateWithError(err)
			return
		}
		hookActions.SendExtensionData(extension)
	}

	if err == ErrResume {
		hookActions.UnpauseResponse()
		return
	}

	if err != nil {
		hookActions.TerminateWithError(err)
	}

}

// gsIncomingResponseHook is a graphsync.OnIncomingResponseHook. We use it to pass on responses
func (hm *Manager) gsIncomingResponseHook(p peer.ID, response graphsync.ResponseData, hookActions graphsync.IncomingResponseHookActions) {

	hm.graphsyncRequestMapLk.RLock()
	chid, ok := hm.graphsyncRequestMap[graphsyncKey{response.RequestID(), hm.peerID}]
	hm.graphsyncRequestMapLk.RUnlock()

	if !ok {
		return
	}

	// if this is a push request the sender is us.
	msg, err := extension.GetTransferData(response)
	if err != nil {
		hookActions.TerminateWithError(err)
		return
	}

	// extension not found; probably not our request.
	if msg == nil {
		return
	}

	var responseMessage message.DataTransferMessage
	if msg.IsRequest() {

		// only accept request message updates when original message was also request
		if (chid != datatransfer.ChannelID{ID: msg.TransferID(), Initiator: p}) {
			err = errors.New("received request on response channel")
		} else {
			dtRequest := msg.(message.DataTransferRequest)
			responseMessage, err = hm.events.OnRequestReceived(chid, dtRequest)
		}
	} else {

		// only accept response message updates when original message was also response
		if (chid != datatransfer.ChannelID{ID: msg.TransferID(), Initiator: hm.peerID}) {
			err = errors.New("received response on request channel")
		} else {
			dtResponse := msg.(message.DataTransferResponse)
			err = hm.events.OnResponseReceived(chid, dtResponse)
		}
	}

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
