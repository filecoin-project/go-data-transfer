package graphsync_test

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"testing"
	"time"

	"github.com/ipfs/go-graphsync"
	"github.com/ipfs/go-graphsync/donotsendfirstblocks"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
	. "github.com/filecoin-project/go-data-transfer/v2/transport/graphsync"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/testharness"
)

func TestManager(t *testing.T) {
	testCases := map[string]struct {
		requestConfig  gsRequestConfig
		responseConfig gsResponseConfig
		updatedConfig  gsRequestConfig
		events         fakeEvents
		action         func(gsData *harness)
		check          func(t *testing.T, events *fakeEvents, gsData *harness)
		protocol       protocol.ID
	}{
		"gs outgoing request with recognized dt pull channel will record incoming blocks": {
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
				require.False(t, events.OnDataReceivedCalled)
				require.NoError(t, gsData.incomingBlockHookActions.TerminationError)
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
				require.True(t, events.OnDataReceivedCalled)
				require.Error(t, gsData.incomingBlockHookActions.TerminationError)
			},
		},
		"gs outgoing request with recognized dt request can receive request processing listener": {
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.outgoingRequestProcessingListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.True(t, events.TransferInitiatedCalled)
				require.Equal(t, events.TransferInitiatedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
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
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
				require.Equal(t, 0, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.NoError(t, gsData.incomingResponseHookActions.TerminationError)
			},
		},
		"outgoing gs request with recognized dt response can send message on update": {
			events: fakeEvents{
				RequestReceivedResponse: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint32())),
			},
			requestConfig: gsRequestConfig{
				dtIsResponse: true,
			},
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.incomingResponseHOok()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, events.ChannelOpenedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
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
				RequestReceivedResponse: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint32())),
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Equal(t, events.RequestReceivedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				dtRequestData, _ := gsData.request.Extension(extension.ExtensionDataTransfer1_1)
				assertDecodesToMessage(t, dtRequestData, events.RequestReceivedRequest)
				require.True(t, gsData.incomingRequestHookActions.Validated)
				assertHasExtensionMessage(t, extension.ExtensionDataTransfer1_1, gsData.incomingRequestHookActions.SentExtensions, events.RequestReceivedResponse)
				require.NoError(t, gsData.incomingRequestHookActions.TerminationError)

				channelsForPeer := gsData.transport.ChannelsForPeer(gsData.other)
				require.Equal(t, channelsForPeer, ChannelsForPeer{
					SendingChannels: map[datatransfer.ChannelID]ChannelGraphsyncRequests{
						events.RequestReceivedChannelID: {
							Current: gsData.request.ID(),
						},
					},
					ReceivingChannels: map[datatransfer.ChannelID]ChannelGraphsyncRequests{},
				})
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
				require.Equal(t, events.ResponseReceivedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
				dtResponseData, _ := gsData.request.Extension(extension.ExtensionDataTransfer1_1)
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
				RequestReceivedResponse: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint32())),
				OnRequestReceivedErrors: []error{errors.New("something went wrong")},
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.Equal(t, 0, events.OnResponseReceivedCallCount)
				require.Equal(t, events.RequestReceivedChannelID, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				dtRequestData, _ := gsData.request.Extension(extension.ExtensionDataTransfer1_1)
				assertDecodesToMessage(t, dtRequestData, events.RequestReceivedRequest)
				require.False(t, gsData.incomingRequestHookActions.Validated)
				assertHasExtensionMessage(t, extension.ExtensionIncomingRequest1_1, gsData.incomingRequestHookActions.SentExtensions, events.RequestReceivedResponse)
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
				require.True(t, events.OnDataQueuedCalled)
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
				require.True(t, events.OnDataQueuedCalled)
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
				require.False(t, events.OnDataQueuedCalled)
			},
		},
		"outgoing data queued error will terminate request": {
			events: fakeEvents{
				OnDataQueuedError: errors.New("something went wrong"),
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.outgoingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnDataQueuedCalled)
				require.Error(t, gsData.outgoingBlockHookActions.TerminationError)
			},
		},
		"outgoing data queued error == pause will pause request": {
			events: fakeEvents{
				OnDataQueuedError: datatransfer.ErrPause,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.outgoingBlockHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnDataQueuedCalled)
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
				OnDataQueuedMessage: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint32())),
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnDataQueuedCalled)
				require.NoError(t, gsData.outgoingBlockHookActions.TerminationError)
				assertHasExtensionMessage(t, extension.ExtensionOutgoingBlock1_1, gsData.outgoingBlockHookActions.SentExtensions,
					events.OnDataQueuedMessage)
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
				RequestReceivedResponse: testutil.NewDTResponse(t, datatransfer.TransferID(rand.Uint32())),
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestUpdatedHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 2, events.OnRequestReceivedCallCount)
				require.NoError(t, gsData.requestUpdatedHookActions.TerminationError)
				assertHasOutgoingMessage(t, gsData.requestUpdatedHookActions.SentExtensions,
					events.RequestReceivedResponse)
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
				require.True(t, events.OnChannelCompletedCalled)
				require.True(t, events.ChannelCompletedSuccess)
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
				require.True(t, events.OnChannelCompletedCalled)
				require.False(t, events.ChannelCompletedSuccess)
			},
		},
		"recognized incoming request will not record request cancellation": {
			responseConfig: gsResponseConfig{
				status: graphsync.RequestCancelled,
			},
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.responseCompletedListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.False(t, events.OnChannelCompletedCalled)
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
				require.False(t, events.OnChannelCompletedCalled)
			},
		},
		"recognized incoming request can be closed": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.CloseChannel(gsData.ctx, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				require.NoError(t, err)
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				gsData.fgs.AssertCancelReceived(gsData.ctx, t)
			},
		},
		"unrecognized request cannot be closed": {
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.CloseChannel(gsData.ctx, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				require.Error(t, err)
			},
		},
		"recognized incoming request that requestor cancelled will not close via graphsync": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestorCancelledListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.CloseChannel(gsData.ctx, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				require.NoError(t, err)
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				gsData.fgs.AssertNoCancelReceived(t)
			},
		},
		"recognized incoming request can be paused": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.PauseChannel(gsData.ctx, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				require.NoError(t, err)
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				gsData.fgs.AssertPauseReceived(gsData.ctx, t)
			},
		},
		"unrecognized request cannot be paused": {
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.PauseChannel(gsData.ctx, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				require.Error(t, err)
			},
		},
		"recognized incoming request that requestor cancelled will not pause via graphsync": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestorCancelledListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.PauseChannel(gsData.ctx, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				require.NoError(t, err)
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				gsData.fgs.AssertNoPauseReceived(t)
			},
		},

		"recognized incoming request can begin processing": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.incomingRequestProcessingListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.True(t, events.TransferInitiatedCalled)
				require.Equal(t, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other},
					events.TransferInitiatedChannelID)
			},
		},

		"recognized incoming request can be resumed": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.ResumeChannel(gsData.ctx,
					gsData.incoming,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other},
				)
				require.NoError(t, err)
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				gsData.fgs.AssertResumeReceived(gsData.ctx, t)
			},
		},

		"unrecognized request cannot be resumed": {
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.ResumeChannel(gsData.ctx,
					gsData.incoming,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other},
				)
				require.Error(t, err)
			},
		},
		"recognized incoming request that requestor cancelled will not resume via graphsync but will resume otherwise": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.requestorCancelledListener()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				err := gsData.transport.ResumeChannel(gsData.ctx,
					gsData.incoming,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other},
				)
				require.NoError(t, err)
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				gsData.fgs.AssertNoResumeReceived(t)
				gsData.incomingRequestHook()
				assertHasOutgoingMessage(t, gsData.incomingRequestHookActions.SentExtensions, gsData.incoming)
			},
		},
		"recognized incoming request will record network send error": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.networkErrorListener(errors.New("something went wrong"))
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnSendDataErrorCalled)
			},
		},
		"recognized outgoing request will record network send error": {
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.networkErrorListener(errors.New("something went wrong"))
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.True(t, events.OnSendDataErrorCalled)
			},
		},
		"recognized incoming request will record network receive error": {
			action: func(gsData *harness) {
				gsData.incomingRequestHook()
				gsData.receiverNetworkErrorListener(errors.New("something went wrong"))
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.Equal(t, 1, events.OnRequestReceivedCallCount)
				require.True(t, events.OnReceiveDataErrorCalled)
			},
		},
		"recognized outgoing request will record network receive error": {
			action: func(gsData *harness) {
				gsData.outgoingRequestHook()
				gsData.receiverNetworkErrorListener(errors.New("something went wrong"))
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				require.True(t, events.OnReceiveDataErrorCalled)
			},
		},
		"open channel adds block count to the DoNotSendFirstBlocks extension for v1.2 protocol": {
			action: func(gsData *harness) {
				channel := testutil.NewMockChannelState(testutil.MockChannelStateParams{ReceivedCidsTotal: 2})
				stor, _ := gsData.outgoing.Selector()

				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					channel,
					gsData.outgoing)
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				requestReceived := gsData.fgs.AssertRequestReceived(gsData.ctx, t)

				ext := requestReceived.Extensions
				require.Len(t, ext, 2)
				doNotSend := ext[1]

				name := doNotSend.Name
				require.Equal(t, graphsync.ExtensionsDoNotSendFirstBlocks, name)
				data := doNotSend.Data
				blockCount, err := donotsendfirstblocks.DecodeDoNotSendFirstBlocks(data)
				require.NoError(t, err)
				require.EqualValues(t, blockCount, 2)
			},
		},
		"ChannelsForPeer when request is open": {
			action: func(gsData *harness) {
				channel := testutil.NewMockChannelState(testutil.MockChannelStateParams{ReceivedCidsTotal: 2})
				stor, _ := gsData.outgoing.Selector()

				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					channel,
					gsData.outgoing)
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				gsData.fgs.AssertRequestReceived(gsData.ctx, t)

				channelsForPeer := gsData.transport.ChannelsForPeer(gsData.other)
				require.Equal(t, channelsForPeer, ChannelsForPeer{
					ReceivingChannels: map[datatransfer.ChannelID]ChannelGraphsyncRequests{
						events.ChannelOpenedChannelID: {
							Current: gsData.request.ID(),
						},
					},
					SendingChannels: map[datatransfer.ChannelID]ChannelGraphsyncRequests{},
				})
			},
		},
		"open channel cancels an existing request with the same channel ID": {
			action: func(gsData *harness) {
				channel := testutil.NewMockChannelState(testutil.MockChannelStateParams{ReceivedCidsTotal: 2})
				stor, _ := gsData.outgoing.Selector()
				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					channel,
					gsData.outgoing)

				go gsData.altOutgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					channel,
					gsData.outgoing)
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				gsData.fgs.AssertRequestReceived(gsData.ctx, t)
				gsData.fgs.AssertRequestReceived(gsData.ctx, t)

				ctxt, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				gsData.fgs.AssertCancelReceived(ctxt, t)

				channelsForPeer := gsData.transport.ChannelsForPeer(gsData.other)
				require.Equal(t, channelsForPeer, ChannelsForPeer{
					ReceivingChannels: map[datatransfer.ChannelID]ChannelGraphsyncRequests{
						events.ChannelOpenedChannelID: {
							Current:  gsData.altRequest.ID(),
							Previous: []graphsync.RequestID{gsData.request.ID()},
						},
					},
					SendingChannels: map[datatransfer.ChannelID]ChannelGraphsyncRequests{},
				})
			},
		},
		"OnChannelCompleted called when outgoing request completes successfully": {
			action: func(gsData *harness) {
				gsData.fgs.LeaveRequestsOpen()
				stor, _ := gsData.outgoing.Selector()

				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					nil,
					gsData.outgoing)
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				requestReceived := gsData.fgs.AssertRequestReceived(gsData.ctx, t)
				close(requestReceived.ResponseChan)
				close(requestReceived.ResponseErrChan)

				require.Eventually(t, func() bool {
					return events.OnChannelCompletedCalled == true
				}, 2*time.Second, 100*time.Millisecond)
				require.True(t, events.ChannelCompletedSuccess)
			},
		},
		"OnChannelCompleted called when outgoing request completes with error": {
			action: func(gsData *harness) {
				gsData.fgs.LeaveRequestsOpen()
				stor, _ := gsData.outgoing.Selector()

				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					nil,
					gsData.outgoing)
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				requestReceived := gsData.fgs.AssertRequestReceived(gsData.ctx, t)
				close(requestReceived.ResponseChan)
				requestReceived.ResponseErrChan <- graphsync.RequestFailedUnknownErr{}
				close(requestReceived.ResponseErrChan)

				require.Eventually(t, func() bool {
					return events.OnChannelCompletedCalled == true
				}, 2*time.Second, 100*time.Millisecond)
				require.False(t, events.ChannelCompletedSuccess)
			},
		},
		"OnChannelComplete when outgoing request cancelled by caller": {
			action: func(gsData *harness) {
				gsData.fgs.LeaveRequestsOpen()
				stor, _ := gsData.outgoing.Selector()

				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					nil,
					gsData.outgoing)
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				requestReceived := gsData.fgs.AssertRequestReceived(gsData.ctx, t)
				extensions := make(map[graphsync.ExtensionName]datamodel.Node)
				for _, ext := range requestReceived.Extensions {
					extensions[ext.Name] = ext.Data
				}
				request := testharness.NewFakeRequest(graphsync.NewRequestID(), extensions, graphsync.RequestTypeNew)
				gsData.fgs.OutgoingRequestHook(gsData.other, request, gsData.outgoingRequestHookActions)
				_ = gsData.transport.CloseChannel(gsData.ctx, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
				ctxt, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				gsData.fgs.AssertCancelReceived(ctxt, t)
			},
		},
		"request times out if we get request context cancelled error": {
			action: func(gsData *harness) {
				gsData.fgs.LeaveRequestsOpen()
				stor, _ := gsData.outgoing.Selector()

				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					nil,
					gsData.outgoing)
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				requestReceived := gsData.fgs.AssertRequestReceived(gsData.ctx, t)
				close(requestReceived.ResponseChan)
				requestReceived.ResponseErrChan <- graphsync.RequestClientCancelledErr{}
				close(requestReceived.ResponseErrChan)

				require.Eventually(t, func() bool {
					return events.OnRequestCancelledCalled == true
				}, 2*time.Second, 100*time.Millisecond)
				require.Equal(t, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self}, events.OnRequestCancelledChannelId)
			},
		},
		"request cancelled out if transport shuts down": {
			action: func(gsData *harness) {
				gsData.fgs.LeaveRequestsOpen()
				stor, _ := gsData.outgoing.Selector()

				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					nil,
					gsData.outgoing)
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				gsData.fgs.AssertRequestReceived(gsData.ctx, t)

				gsData.transport.Shutdown(gsData.ctx)

				ctxt, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				gsData.fgs.AssertCancelReceived(ctxt, t)

				require.Nil(t, gsData.fgs.IncomingRequestHook)
				require.Nil(t, gsData.fgs.CompletedResponseListener)
				require.Nil(t, gsData.fgs.IncomingBlockHook)
				require.Nil(t, gsData.fgs.OutgoingBlockHook)
				require.Nil(t, gsData.fgs.BlockSentListener)
				require.Nil(t, gsData.fgs.OutgoingRequestHook)
				require.Nil(t, gsData.fgs.IncomingResponseHook)
				require.Nil(t, gsData.fgs.RequestUpdatedHook)
				require.Nil(t, gsData.fgs.RequestorCancelledListener)
				require.Nil(t, gsData.fgs.NetworkErrorListener)
			},
		},
		"request pause works even if called when request is still pending": {
			action: func(gsData *harness) {
				gsData.fgs.LeaveRequestsOpen()
				stor, _ := gsData.outgoing.Selector()

				go gsData.outgoingRequestHook()
				_ = gsData.transport.OpenChannel(
					gsData.ctx,
					gsData.other,
					datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
					cidlink.Link{Cid: gsData.outgoing.BaseCid()},
					stor,
					nil,
					gsData.outgoing)

			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				requestReceived := gsData.fgs.AssertRequestReceived(gsData.ctx, t)
				assertHasOutgoingMessage(t, requestReceived.Extensions, gsData.outgoing)
				completed := make(chan struct{})
				go func() {
					err := gsData.transport.PauseChannel(context.Background(), datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
					require.NoError(t, err)
					close(completed)
				}()
				time.Sleep(100 * time.Millisecond)
				extensions := make(map[graphsync.ExtensionName]datamodel.Node)
				for _, ext := range requestReceived.Extensions {
					extensions[ext.Name] = ext.Data
				}
				request := testharness.NewFakeRequest(graphsync.NewRequestID(), extensions, graphsync.RequestTypeNew)
				gsData.fgs.OutgoingRequestHook(gsData.other, request, gsData.outgoingRequestHookActions)
				select {
				case <-gsData.ctx.Done():
					t.Fatal("never paused channel")
				case <-completed:
				}
			},
		},
		"UseStore can change store used for outgoing requests": {
			action: func(gsData *harness) {
				lsys := cidlink.DefaultLinkSystem()
				lsys.StorageReadOpener = func(ipld.LinkContext, ipld.Link) (io.Reader, error) {
					return nil, nil
				}
				lsys.StorageWriteOpener = func(ipld.LinkContext) (io.Writer, ipld.BlockWriteCommitter, error) {
					return nil, nil, nil
				}
				_ = gsData.transport.UseStore(datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self}, lsys)
				gsData.outgoingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				expectedChannel := "data-transfer-" + datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self}.String()
				gsData.fgs.AssertHasPersistenceOption(t, expectedChannel)
				require.Equal(t, expectedChannel, gsData.outgoingRequestHookActions.PersistenceOption)
				gsData.transport.CleanupChannel(datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self})
				gsData.fgs.AssertDoesNotHavePersistenceOption(t, expectedChannel)
			},
		},
		"UseStore can change store used for incoming requests": {
			action: func(gsData *harness) {
				lsys := cidlink.DefaultLinkSystem()
				lsys.StorageReadOpener = func(ipld.LinkContext, ipld.Link) (io.Reader, error) {
					return nil, nil
				}
				lsys.StorageWriteOpener = func(ipld.LinkContext) (io.Writer, ipld.BlockWriteCommitter, error) {
					return nil, nil, nil
				}
				_ = gsData.transport.UseStore(datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other}, lsys)
				gsData.incomingRequestHook()
			},
			check: func(t *testing.T, events *fakeEvents, gsData *harness) {
				expectedChannel := "data-transfer-" + datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other}.String()
				gsData.fgs.AssertHasPersistenceOption(t, expectedChannel)
				require.Equal(t, expectedChannel, gsData.incomingRequestHookActions.PersistenceOption)
				gsData.transport.CleanupChannel(datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other})
				gsData.fgs.AssertDoesNotHavePersistenceOption(t, expectedChannel)
			},
		},
	}

	ctx := context.Background()
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			peers := testutil.GeneratePeers(2)
			transferID := datatransfer.TransferID(rand.Uint32())
			requestID := graphsync.NewRequestID()
			request := data.requestConfig.makeRequest(t, transferID, requestID)
			altRequest := data.requestConfig.makeRequest(t, transferID, graphsync.NewRequestID())
			response := data.responseConfig.makeResponse(t, transferID, requestID)
			updatedRequest := data.updatedConfig.makeRequest(t, transferID, requestID)
			block := testharness.NewFakeBlockData(rand.Uint64(), int64(rand.Uint32()), true)
			fgs := testharness.NewFakeGraphSync()
			outgoing := testutil.NewDTRequest(t, transferID)
			incoming := testutil.NewDTResponse(t, transferID)
			transport := NewTransport(peers[0], fgs)
			gsData := &harness{
				ctx:                         ctx,
				outgoing:                    outgoing,
				incoming:                    incoming,
				transport:                   transport,
				fgs:                         fgs,
				self:                        peers[0],
				transferID:                  transferID,
				other:                       peers[1],
				altRequest:                  altRequest,
				request:                     request,
				response:                    response,
				updatedRequest:              updatedRequest,
				block:                       block,
				outgoingRequestHookActions:  &testharness.FakeOutgoingRequestHookActions{},
				outgoingBlockHookActions:    &testharness.FakeOutgoingBlockHookActions{},
				incomingBlockHookActions:    &testharness.FakeIncomingBlockHookActions{},
				incomingRequestHookActions:  &testharness.FakeIncomingRequestHookActions{},
				requestUpdatedHookActions:   &testharness.FakeRequestUpdatedActions{},
				incomingResponseHookActions: &testharness.FakeIncomingResponseHookActions{},
			}
			require.NoError(t, transport.SetEventHandler(&data.events))
			if data.action != nil {
				data.action(gsData)
			}
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
	OnRequestReceivedCallCount  int
	OnRequestReceivedErrors     []error
	OnResponseReceivedCallCount int
	OnResponseReceivedErrors    []error
	OnChannelCompletedCalled    bool
	OnChannelCompletedErr       error
	OnDataQueuedCalled          bool
	OnDataQueuedMessage         datatransfer.Message
	OnDataQueuedError           error

	OnRequestCancelledCalled    bool
	OnRequestCancelledChannelId datatransfer.ChannelID
	OnSendDataErrorCalled       bool
	OnSendDataErrorChannelID    datatransfer.ChannelID
	OnReceiveDataErrorCalled    bool
	OnReceiveDataErrorChannelID datatransfer.ChannelID
	OnContextAugmentFunc        func(context.Context) context.Context
	TransferInitiatedCalled     bool
	TransferInitiatedChannelID  datatransfer.ChannelID

	ChannelCompletedSuccess  bool
	RequestReceivedRequest   datatransfer.Request
	RequestReceivedResponse  datatransfer.Response
	ResponseReceivedResponse datatransfer.Response
}

func (fe *fakeEvents) OnDataQueued(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) (datatransfer.Message, error) {
	fe.OnDataQueuedCalled = true

	return fe.OnDataQueuedMessage, fe.OnDataQueuedError
}

func (fe *fakeEvents) OnRequestCancelled(chid datatransfer.ChannelID, err error) error {
	fe.OnRequestCancelledCalled = true
	fe.OnRequestCancelledChannelId = chid

	return nil
}

func (fe *fakeEvents) OnTransferInitiated(chid datatransfer.ChannelID) {
	fe.TransferInitiatedCalled = true
	fe.TransferInitiatedChannelID = chid
}

func (fe *fakeEvents) OnRequestDisconnected(chid datatransfer.ChannelID, err error) error {
	return nil
}

func (fe *fakeEvents) OnSendDataError(chid datatransfer.ChannelID, err error) error {
	fe.OnSendDataErrorCalled = true
	fe.OnSendDataErrorChannelID = chid
	return nil
}

func (fe *fakeEvents) OnReceiveDataError(chid datatransfer.ChannelID, err error) error {
	fe.OnReceiveDataErrorCalled = true
	fe.OnReceiveDataErrorChannelID = chid
	return nil
}

func (fe *fakeEvents) OnChannelOpened(chid datatransfer.ChannelID) error {
	fe.ChannelOpenedChannelID = chid
	return fe.OnChannelOpenedError
}

func (fe *fakeEvents) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) error {
	fe.OnDataReceivedCalled = true
	return fe.OnDataReceivedError
}

func (fe *fakeEvents) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) error {
	fe.OnDataSentCalled = true
	return nil
}

func (fe *fakeEvents) OnRequestReceived(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {
	fe.OnRequestReceivedCallCount++
	fe.RequestReceivedChannelID = chid
	fe.RequestReceivedRequest = request
	var err error
	if len(fe.OnRequestReceivedErrors) > 0 {
		err, fe.OnRequestReceivedErrors = fe.OnRequestReceivedErrors[0], fe.OnRequestReceivedErrors[1:]
	}
	return fe.RequestReceivedResponse, err
}

func (fe *fakeEvents) OnResponseReceived(chid datatransfer.ChannelID, response datatransfer.Response) error {
	fe.OnResponseReceivedCallCount++
	fe.ResponseReceivedResponse = response
	fe.ResponseReceivedChannelID = chid
	var err error
	if len(fe.OnResponseReceivedErrors) > 0 {
		err, fe.OnResponseReceivedErrors = fe.OnResponseReceivedErrors[0], fe.OnResponseReceivedErrors[1:]
	}
	return err
}

func (fe *fakeEvents) OnChannelCompleted(chid datatransfer.ChannelID, completeErr error) error {
	fe.OnChannelCompletedCalled = true
	fe.ChannelCompletedSuccess = completeErr == nil
	return fe.OnChannelCompletedErr
}

func (fe *fakeEvents) OnContextAugment(chid datatransfer.ChannelID) func(context.Context) context.Context {
	return fe.OnContextAugmentFunc
}

type harness struct {
	outgoing                    datatransfer.Request
	incoming                    datatransfer.Response
	ctx                         context.Context
	transport                   *Transport
	fgs                         *testharness.FakeGraphSync
	transferID                  datatransfer.TransferID
	self                        peer.ID
	other                       peer.ID
	block                       graphsync.BlockData
	request                     graphsync.RequestData
	altRequest                  graphsync.RequestData
	response                    graphsync.ResponseData
	updatedRequest              graphsync.RequestData
	outgoingRequestHookActions  *testharness.FakeOutgoingRequestHookActions
	incomingBlockHookActions    *testharness.FakeIncomingBlockHookActions
	outgoingBlockHookActions    *testharness.FakeOutgoingBlockHookActions
	incomingRequestHookActions  *testharness.FakeIncomingRequestHookActions
	requestUpdatedHookActions   *testharness.FakeRequestUpdatedActions
	incomingResponseHookActions *testharness.FakeIncomingResponseHookActions
}

func (ha *harness) outgoingRequestHook() {
	ha.fgs.OutgoingRequestHook(ha.other, ha.request, ha.outgoingRequestHookActions)
}

func (ha *harness) altOutgoingRequestHook() {
	ha.fgs.OutgoingRequestHook(ha.other, ha.altRequest, ha.outgoingRequestHookActions)
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
	ha.fgs.CompletedResponseListener(ha.other, ha.request, ha.response.Status())
}
func (ha *harness) requestorCancelledListener() {
	ha.fgs.RequestorCancelledListener(ha.other, ha.request)
}
func (ha *harness) networkErrorListener(err error) {
	ha.fgs.NetworkErrorListener(ha.other, ha.request, err)
}
func (ha *harness) receiverNetworkErrorListener(err error) {
	ha.fgs.ReceiverNetworkErrorListener(ha.other, err)
}
func (ha *harness) outgoingRequestProcessingListener() {
	ha.fgs.OutgoingRequestProcessingListener(ha.other, ha.request, 0)
}

func (ha *harness) incomingRequestProcessingListener() {
	ha.fgs.IncomingRequestProcessingListener(ha.other, ha.request, 0)
}

type dtConfig struct {
	dtExtensionMissing   bool
	dtIsResponse         bool
	dtExtensionMalformed bool
}

func (dtc *dtConfig) extensions(t *testing.T, transferID datatransfer.TransferID, extName graphsync.ExtensionName) map[graphsync.ExtensionName]datamodel.Node {
	extensions := make(map[graphsync.ExtensionName]datamodel.Node)
	if !dtc.dtExtensionMissing {
		if dtc.dtExtensionMalformed {
			extensions[extName] = basicnode.NewInt(10)
		} else {
			var msg datatransfer.Message
			if dtc.dtIsResponse {
				msg = testutil.NewDTResponse(t, transferID)
			} else {
				msg = testutil.NewDTRequest(t, transferID)
			}
			nd := msg.ToIPLD()
			extensions[extName] = nd
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
	extensions := dtConfig.extensions(t, transferID, extension.ExtensionDataTransfer1_1)
	return testharness.NewFakeRequest(requestID, extensions, graphsync.RequestTypeNew)
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
	extensions := dtConfig.extensions(t, transferID, extension.ExtensionDataTransfer1_1)
	return testharness.NewFakeResponse(requestID, extensions, grc.status)
}

func assertDecodesToMessage(t *testing.T, data datamodel.Node, expected datatransfer.Message) {
	actual, err := message.FromIPLD(data)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func assertHasOutgoingMessage(t *testing.T, extensions []graphsync.ExtensionData, expected datatransfer.Message) {
	nd := expected.ToIPLD()
	found := false
	for _, e := range extensions {
		if e.Name == extension.ExtensionDataTransfer1_1 {
			require.True(t, ipld.DeepEqual(nd, e.Data), "data matches")
			found = true
		}
	}
	if !found {
		require.Fail(t, "extension not found")
	}
}

func assertHasExtensionMessage(t *testing.T, name graphsync.ExtensionName, extensions []graphsync.ExtensionData, expected datatransfer.Message) {
	nd := expected.ToIPLD()
	found := false
	for _, e := range extensions {
		if e.Name == name {
			require.True(t, ipld.DeepEqual(nd, e.Data), "data matches")
			found = true
		}
	}
	if !found {
		require.Fail(t, "extension not found")
	}
}
