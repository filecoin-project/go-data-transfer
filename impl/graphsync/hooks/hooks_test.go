package hooks_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/impl/graphsync/extension"
	"github.com/filecoin-project/go-data-transfer/impl/graphsync/hooks"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/filecoin-project/go-data-transfer/testutil"
	"github.com/ipfs/go-graphsync"
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/traversal"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"
)

func TestManager(t *testing.T) {
	testCases := map[string]struct {
		requestConfig  gsRequestConfig
		responseConfig gsResponseConfig
		updatedConfig  gsRequestConfig
		events         fakeEvents
		action         func(gsData *harness)
		check          func(t *testing.T, events *fakeEvents, gsData *harness)
	}{
		"gs outgoing request with recognized dt pull channel will record incoming blocks": {
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				require.True(t, events.OnDataReceivedCalled)
				require.NoError(t, gsData.incomingBlockHookActions.TerminationError)
			},
		},
		"gs outgoing request with recognized dt push channel will record incoming blocks": {
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.other})
				require.True(t, events.OnDataReceivedCalled)
				require.NoError(t, gsData.incomingBlockHookActions.TerminationError)
			},
		},
		"non-data-transfer gs request will not record incoming blocks and send updates": {
			requestConfig: gsRequestConfig{
				dtExtensionMissing: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{})
				require.False(t, events.OnDataReceivedCalled)
				require.NoError(t, gsData.incomingBlockHookActions.TerminationError)
			},
		},
		"gs request unrecognized opened channel will not record incoming blocks": {
			events: fakeEvents{
				OnChannelOpenedError: errors.New("Not recognized"),
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				require.False(t, events.OnDataReceivedCalled)
				require.NoError(t, gsData.incomingBlockHookActions.TerminationError)
			},
		},
		"gs incoming block will send update messages": {
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingBlockHook()
			},
			events: fakeEvents{
				DataReceivedMessage: testutil.NewDTRequest(t, datatransfer.TransferID(rand.Uint64())),
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				require.True(t, events.OnDataReceivedCalled)
				require.NoError(t, gsData.incomingBlockHookActions.TerminationError)
				assertHasOutgoingMessage(t, gsData.incomingBlockHookActions.SentExtensions, events.DataReceivedMessage)
			},
		},
		"gs incoming block with data receive error will halt request": {
			events: fakeEvents{
				OnDataReceivedError: errors.New("something went wrong"),
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				require.True(t, events.OnDataReceivedCalled)
				require.Error(t, gsData.incomingBlockHookActions.TerminationError)
			},
		},
		"outgoing gs request with recognized dt request can receive gs response": {
			responseConfig: gsResponseConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.Equal(t, 1, events.OnResponseReceivedCallCount)
				require.NoError(t, gsData.incomingResponseHookActions.TerminationError)
			},
		},
		"outgoing gs request with recognized dt request cannot receive gs response with dt request": {
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Error(t, gsData.incomingResponseHookActions.TerminationError)
			},
		},
		"outgoing gs request with recognized dt response can receive gs response": {
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.other})
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.NoError(t, gsData.incomingResponseHookActions.TerminationError)
			},
		},
		"outgoing gs request with recognized dt response cannot receive gs response with dt response": {
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			responseConfig: gsResponseConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.other})
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.Error(t, gsData.incomingResponseHookActions.TerminationError)
			},
		},
		"outgoing gs request with recognized dt request will error with malformed update": {
			responseConfig: gsResponseConfig{
				dtExtensionMalformed: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Error(t, gsData.incomingResponseHookActions.TerminationError)
			},
		},
		"outgoing gs request with recognized dt request will ignore non-data-transfer update": {
			responseConfig: gsResponseConfig{
				dtExtensionMissing: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.NoError(t, gsData.incomingResponseHookActions.TerminationError)
			},
		},
		"outgoing gs request with recognized dt response can send message on update": {
			events: fakeEvents{
				RequestReceivedResponse: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint64())),
			},
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.other})
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.NoError(t, gsData.incomingResponseHookActions.TerminationError)
				assertHasOutgoingMessage(t, gsData.incomingResponseHookActions.SentExtensions,
					events.RequestReceivedResponse)
			},
		},
		"outgoing gs request with recognized dt response err will error": {
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			events: fakeEvents{
				OnRequestReceivedErrors: []error{errors.New("something went wrong")},
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Error(t, gsData.incomingResponseHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt request will validate gs request & send dt response": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
			},
			events: fakeEvents{
				RequestReceivedResponse: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint64())),
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Equal(t, events.RequestReceivedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.other})
				dtRequestData, _ := gsData.request.Extension(extension.ExtensionDataTransfer)
				assertDecodesToMessage(t, dtRequestData, events.RequestReceivedRequest)
				require.True(t, gsData.incomingRequestHookActions.Validated)
				require.Equal(t, extension.ExtensionDataTransfer, gsData.incomingRequestHookActions.SentExtension.Name)
				assertDecodesToMessage(t, gsData.incomingRequestHookActions.SentExtension.Data, events.RequestReceivedResponse)
				require.NoError(t, gsData.incomingRequestHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt response will validate gs request": {
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.Equal(t, 1, events.OnResponseReceivedCallCount)
				require.Equal(t, events.ResponseReceivedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.self})
				dtResponseData, _ := gsData.request.Extension(extension.ExtensionDataTransfer)
				assertDecodesToMessage(t, dtResponseData, events.ResponseReceivedResponse)
				require.True(t, gsData.incomingRequestHookActions.Validated)
				require.NoError(t, gsData.incomingRequestHookActions.TerminationError)
			},
		},
		"malformed data transfer extension on incoming request will terminate": {
			requestConfig: gsRequestConfig{
				dtExtensionMalformed: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.False(t, gsData.incomingRequestHookActions.Validated)
				require.Error(t, gsData.incomingRequestHookActions.TerminationError)
			},
		},
		"unrecognized incoming dt request will terminate but send response": {
			events: fakeEvents{
				RequestReceivedResponse: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint64())),
				OnRequestReceivedErrors: []error{errors.New("something went wrong")},
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Equal(t, events.RequestReceivedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Initiator: gsData.other})
				dtRequestData, _ := gsData.request.Extension(extension.ExtensionDataTransfer)
				assertDecodesToMessage(t, dtRequestData, events.RequestReceivedRequest)
				require.False(t, gsData.incomingRequestHookActions.Validated)
				require.Equal(t, extension.ExtensionDataTransfer, gsData.incomingRequestHookActions.SentExtension.Name)
				assertDecodesToMessage(t, gsData.incomingRequestHookActions.SentExtension.Data, events.RequestReceivedResponse)
				require.Error(t, gsData.incomingRequestHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt request will record outgoing blocks": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.outgoingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnDataSentCalled)
				require.NoError(t, gsData.outgoingBlockHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt response will record outgoing blocks": {
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.outgoingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnResponseReceivedCallCount)
				require.True(t, events.OnDataSentCalled)
				require.NoError(t, gsData.outgoingBlockHookActions.TerminationError)
			},
		},
		"non-data-transfer request will not record outgoing blocks": {
			requestConfig: gsRequestConfig{
				dtExtensionMissing: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.outgoingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.False(t, events.OnDataSentCalled)
			},
		},
		"outgoing data send error will terminate request": {
			events: fakeEvents{
				OnDataSentError: errors.New("something went wrong"),
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.outgoingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnDataSentCalled)
				require.Error(t, gsData.outgoingBlockHookActions.TerminationError)
			},
		},
		"outgoing data send error == pause will pause request": {
			events: fakeEvents{
				OnDataSentError: hooks.ErrPause,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.outgoingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnDataSentCalled)
				require.True(t, gsData.outgoingBlockHookActions.Paused)
				require.NoError(t, gsData.outgoingBlockHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt request will send updates": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.outgoingBlockHook()
			},
			events: fakeEvents{
				DataSentMessage: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint64())),
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnDataSentCalled)
				require.NoError(t, gsData.outgoingBlockHookActions.TerminationError)
				assertHasOutgoingMessage(t, []graphsync.ExtensionData{gsData.outgoingBlockHookActions.SentExtension},
					events.DataSentMessage)
			},
		},
		"incoming gs request with recognized dt request can receive update": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 2, events.OnRequestReceivedCallCount)
				require.NoError(t, gsData.requestUpdatedHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt request cannot receive update with dt response": {
			updatedConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Error(t, gsData.requestUpdatedHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt response can receive update": {
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			updatedConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 2, events.OnResponseReceivedCallCount)
				require.NoError(t, gsData.requestUpdatedHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt response cannot receive update with dt request": {
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnResponseReceivedCallCount)
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.Error(t, gsData.requestUpdatedHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt request will error with malformed update": {
			updatedConfig: gsRequestConfig{
				dtExtensionMalformed: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Error(t, gsData.requestUpdatedHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt request will ignore non-data-transfer update": {
			updatedConfig: gsRequestConfig{
				dtExtensionMissing: true,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.NoError(t, gsData.requestUpdatedHookActions.TerminationError)
			},
		},
		"incoming gs request with recognized dt request can send message on update": {
			events: fakeEvents{
				RequestReceivedResponse: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint64())),
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 2, events.OnRequestReceivedCallCount)
				require.NoError(t, gsData.requestUpdatedHookActions.TerminationError)
				assertHasOutgoingMessage(t, []graphsync.ExtensionData{gsData.requestUpdatedHookActions.SentExtension},
					events.RequestReceivedResponse)
			},
		},
		"incoming gs request with recognized dt request err = ErrResume will resume processing": {
			events: fakeEvents{
				OnRequestReceivedErrors: []error{nil, hooks.ErrResume},
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 2, events.OnRequestReceivedCallCount)
				require.NoError(t, gsData.requestUpdatedHookActions.TerminationError)
				require.True(t, gsData.requestUpdatedHookActions.Unpaused)
			},
		},
		"recognized incoming request will record successful request completion": {
			responseConfig: gsResponseConfig{
				status: graphsync.RequestCompletedFull,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.responseCompletedListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnResponseCompletedCalled)
				require.True(t, events.ResponseSuccess)
			},
		},
		"recognized incoming request will record unsuccessful request completion": {
			responseConfig: gsResponseConfig{
				status: graphsync.RequestCompletedPartial,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.responseCompletedListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnResponseCompletedCalled)
				require.False(t, events.ResponseSuccess)
			},
		},
		"non-data-transfer request will not record request completed": {
			requestConfig: gsRequestConfig{
				dtExtensionMissing: true,
			},
			responseConfig: gsResponseConfig{
				status: graphsync.RequestCompletedPartial,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.responseCompletedListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.False(t, events.OnResponseCompletedCalled)
			},
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			peers := testutil.GeneratePeers(2)
			transferID := datatransfer.TransferID(rand.Uint64())
			requestID := graphsync.RequestID(rand.Int31())
			request := data.requestConfig.makeRequest(t, transferID, requestID)
			response := data.responseConfig.makeResponse(t, transferID, requestID)
			updatedRequest := data.updatedConfig.makeRequest(t, transferID, requestID)
			block := testutil.NewFakeBlockData()
			fgs := testutil.NewFakeGraphSync()
			gsData := &harness{
				fgs:                         fgs,
				self:                        peers[0],
				transferID:                  transferID,
				other:                       peers[1],
				request:                     request,
				response:                    response,
				updatedRequest:              updatedRequest,
				block:                       block,
				outgoingRequestHookActions:  &fakeOutgoingRequestHookActions{},
				outgoingBlockHookActions:    &fakeOutgoingBlockHookActions{},
				incomingBlockHookActions:    &fakeIncomingBlockHookActions{},
				incomingRequestHookActions:  &fakeIncomingRequestHookActions{},
				requestUpdatedHookActions:   &fakeRequestUpdatedActions{},
				incomingResponseHookActions: &fakeIncomingResponseHookActions{},
			}
			manager := hooks.NewManager(peers[0], &data.events)
			manager.RegisterHooks(fgs)
			data.action(gsData)
			data.check(t, &data.events, gsData)
		})
	}
}

type fakeEvents struct {
	ChannelOpenedChannelID      datatransfer.ChannelID
	RequestReceivedChannelID    datatransfer.ChannelID
	ResponseReceivedChannelID   datatransfer.ChannelID
	OnChannelOpenedError        error
	OnDataReceivedCalled        bool
	OnDataReceivedError         error
	OnDataSentCalled            bool
	OnDataSentError             error
	OnRequestReceivedCallCount  int
	OnRequestReceivedErrors     []error
	OnResponseReceivedCallCount int
	OnResponseReceivedErrors    []error
	OnResponseCompletedCalled   bool
	OnResponseCompletedErr      error
	ResponseSuccess             bool
	DataReceivedMessage         message.DataTransferMessage
	DataSentMessage             message.DataTransferMessage
	RequestReceivedRequest      message.DataTransferRequest
	RequestReceivedResponse     message.DataTransferResponse
	ResponseReceivedResponse    message.DataTransferResponse
}

func (fe *fakeEvents) OnChannelOpened(chid datatransfer.ChannelID) error {
	fe.ChannelOpenedChannelID = chid
	return fe.OnChannelOpenedError
}

func (fe *fakeEvents) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error) {
	fe.OnDataReceivedCalled = true
	return fe.DataReceivedMessage, fe.OnDataReceivedError
}

func (fe *fakeEvents) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error) {
	fe.OnDataSentCalled = true
	return fe.DataSentMessage, fe.OnDataSentError
}

func (fe *fakeEvents) OnRequestReceived(chid datatransfer.ChannelID, request message.DataTransferRequest) (message.DataTransferResponse, error) {
	fe.OnRequestReceivedCallCount++
	fe.RequestReceivedChannelID = chid
	fe.RequestReceivedRequest = request
	var err error
	if len(fe.OnRequestReceivedErrors) > 0 {
		err, fe.OnRequestReceivedErrors = fe.OnRequestReceivedErrors[0], fe.OnRequestReceivedErrors[1:]
	}
	return fe.RequestReceivedResponse, err
}

func (fe *fakeEvents) OnResponseReceived(chid datatransfer.ChannelID, response message.DataTransferResponse) error {
	fe.OnResponseReceivedCallCount++
	fe.ResponseReceivedResponse = response
	fe.ResponseReceivedChannelID = chid
	var err error
	if len(fe.OnResponseReceivedErrors) > 0 {
		err, fe.OnResponseReceivedErrors = fe.OnResponseReceivedErrors[0], fe.OnResponseReceivedErrors[1:]
	}
	return err
}

func (fe *fakeEvents) OnResponseCompleted(chid datatransfer.ChannelID, success bool) error {
	fe.OnResponseCompletedCalled = true
	fe.ResponseSuccess = success
	return fe.OnResponseCompletedErr
}

type fakeOutgoingRequestHookActions struct{}

func (fa *fakeOutgoingRequestHookActions) UsePersistenceOption(name string) {}
func (fa *fakeOutgoingRequestHookActions) UseLinkTargetNodeStyleChooser(_ traversal.LinkTargetNodeStyleChooser) {
}

type fakeIncomingBlockHookActions struct {
	TerminationError error
	SentExtensions   []graphsync.ExtensionData
}

func (fa *fakeIncomingBlockHookActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *fakeIncomingBlockHookActions) UpdateRequestWithExtensions(extensions ...graphsync.ExtensionData) {
	fa.SentExtensions = append(fa.SentExtensions, extensions...)
}

type fakeOutgoingBlockHookActions struct {
	TerminationError error
	SentExtension    graphsync.ExtensionData
	Paused           bool
}

func (fa *fakeOutgoingBlockHookActions) SendExtensionData(extension graphsync.ExtensionData) {
	fa.SentExtension = extension
}

func (fa *fakeOutgoingBlockHookActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *fakeOutgoingBlockHookActions) PauseResponse() {
	fa.Paused = true
}

type fakeIncomingRequestHookActions struct {
	TerminationError error
	Validated        bool
	SentExtension    graphsync.ExtensionData
}

func (fa *fakeIncomingRequestHookActions) SendExtensionData(ext graphsync.ExtensionData) {
	fa.SentExtension = ext
}

func (fa *fakeIncomingRequestHookActions) UsePersistenceOption(name string) {}

func (fa *fakeIncomingRequestHookActions) UseLinkTargetNodeStyleChooser(_ traversal.LinkTargetNodeStyleChooser) {
}

func (fa *fakeIncomingRequestHookActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *fakeIncomingRequestHookActions) ValidateRequest() {
	fa.Validated = true
}

type fakeRequestUpdatedActions struct {
	TerminationError error
	SentExtension    graphsync.ExtensionData
	Unpaused         bool
}

func (fa *fakeRequestUpdatedActions) SendExtensionData(extension graphsync.ExtensionData) {
	fa.SentExtension = extension
}

func (fa *fakeRequestUpdatedActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *fakeRequestUpdatedActions) UnpauseResponse() {
	fa.Unpaused = true
}

type fakeIncomingResponseHookActions struct {
	TerminationError error
	SentExtensions   []graphsync.ExtensionData
}

func (fa *fakeIncomingResponseHookActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *fakeIncomingResponseHookActions) UpdateRequestWithExtensions(extensions ...graphsync.ExtensionData) {
	fa.SentExtensions = append(fa.SentExtensions, extensions...)
}

type harness struct {
	fgs                         *testutil.FakeGraphSync
	transferID                  datatransfer.TransferID
	self                        peer.ID
	other                       peer.ID
	block                       graphsync.BlockData
	request                     graphsync.RequestData
	response                    graphsync.ResponseData
	updatedRequest              graphsync.RequestData
	outgoingRequestHookActions  *fakeOutgoingRequestHookActions
	incomingBlockHookActions    *fakeIncomingBlockHookActions
	outgoingBlockHookActions    *fakeOutgoingBlockHookActions
	incomingRequestHookActions  *fakeIncomingRequestHookActions
	requestUpdatedHookActions   *fakeRequestUpdatedActions
	incomingResponseHookActions *fakeIncomingResponseHookActions
}

func (ha *harness) outgoingRequestHook() {
	ha.fgs.OutgoingRequestHook(ha.other, ha.request, ha.outgoingRequestHookActions)
}
func (ha *harness) incomingBlockHook() {
	ha.fgs.IncomingBlockHook(ha.other, ha.response, ha.block, ha.incomingBlockHookActions)
}
func (ha *harness) outgoingBlockHook() {
	ha.fgs.OutgoingBlockHook(ha.other, ha.request, ha.block, ha.outgoingBlockHookActions)
}
func (ha *harness) incomingRequestHook() {
	ha.fgs.IncomingRequestHook(ha.other, ha.request, ha.incomingRequestHookActions)
}
func (ha *harness) requestUpdatedHook() {
	ha.fgs.RequestUpdatedHook(ha.other, ha.request, ha.updatedRequest, ha.requestUpdatedHookActions)
}
func (ha *harness) incomingResponseHOok() {
	ha.fgs.IncomingResponseHook(ha.other, ha.response, ha.incomingResponseHookActions)
}
func (ha *harness) responseCompletedListener() {
	ha.fgs.ResponseCompletedListener(ha.other, ha.request, ha.response.Status())
}

type dtConfig struct {
	dtExtensionMissing   bool
	dtIsResponse         bool
	dtExtensionMalformed bool
}

func (dtc *dtConfig) extensions(t *testing.T, transferID datatransfer.TransferID) map[graphsync.ExtensionName][]byte {
	extensions := make(map[graphsync.ExtensionName][]byte)
	if !dtc.dtExtensionMissing {
		if dtc.dtExtensionMalformed {
			extensions[extension.ExtensionDataTransfer] = testutil.RandomBytes(100)
		} else {
			var msg message.DataTransferMessage
			if dtc.dtIsResponse {
				msg = testutil.NewDTResponse(t, transferID)
			} else {
				msg = testutil.NewDTRequest(t, transferID)
			}
			buf := new(bytes.Buffer)
			err := msg.ToNet(buf)
			require.NoError(t, err)
			extensions[extension.ExtensionDataTransfer] = buf.Bytes()
		}
	}
	return extensions
}

type gsRequestConfig struct {
	dtExtensionMissing   bool
	dtIsResponse         bool
	dtExtensionMalformed bool
}

func (grc *gsRequestConfig) makeRequest(t *testing.T, transferID datatransfer.TransferID, requestID graphsync.RequestID) graphsync.RequestData {
	dtConfig := dtConfig{
		dtExtensionMissing:   grc.dtExtensionMissing,
		dtIsResponse:         grc.dtIsResponse,
		dtExtensionMalformed: grc.dtExtensionMalformed,
	}
	extensions := dtConfig.extensions(t, transferID)
	return testutil.NewFakeRequest(requestID, extensions)
}

type gsResponseConfig struct {
	dtExtensionMissing   bool
	dtIsResponse         bool
	dtExtensionMalformed bool
	status               graphsync.ResponseStatusCode
}

func (grc *gsResponseConfig) makeResponse(t *testing.T, transferID datatransfer.TransferID, requestID graphsync.RequestID) graphsync.ResponseData {
	dtConfig := dtConfig{
		dtExtensionMissing:   grc.dtExtensionMissing,
		dtIsResponse:         grc.dtIsResponse,
		dtExtensionMalformed: grc.dtExtensionMalformed,
	}
	extensions := dtConfig.extensions(t, transferID)
	return testutil.NewFakeResponse(requestID, extensions, grc.status)
}

func assertDecodesToMessage(t *testing.T, data []byte, expected message.DataTransferMessage) {
	buf := bytes.NewReader(data)
	actual, err := message.FromNet(buf)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func assertHasOutgoingMessage(t *testing.T, extensions []graphsync.ExtensionData, expected message.DataTransferMessage) {
	buf := new(bytes.Buffer)
	err := expected.ToNet(buf)
	require.NoError(t, err)
	expectedExt := graphsync.ExtensionData{
		Name: extension.ExtensionDataTransfer,
		Data: buf.Bytes(),
	}
	require.Contains(t, extensions, expectedExt)
}
