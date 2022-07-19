package graphsync_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ipfs/go-graphsync"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/testharness"
)

func TestInitiatingPullRequestSuccessFlow(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	th := testharness.SetupHarness(ctx, testharness.PullRequest())
	var receivedRequest testharness.ReceivedGraphSyncRequest
	var request graphsync.RequestData
	t.Run("opens successfully", func(t *testing.T) {
		err := th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
		require.NoError(t, err)
		require.Len(t, th.DtNet.ProtectedPeers, 1)
		require.Equal(t, th.DtNet.ProtectedPeers[0], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
		require.Len(t, th.Fgs.ReceivedRequests, 1)
		receivedRequest = th.Fgs.ReceivedRequests[0]
		request = receivedRequest.ToRequestData(t)
		msg, err := extension.GetTransferData(request, []graphsync.ExtensionName{
			extension.ExtensionDataTransfer1_1,
		})
		require.NoError(t, err)
		require.Equal(t, th.NewRequest(t), msg)
	})
	t.Run("configures persistence", func(t *testing.T) {
		th.Transport.UseStore(th.Channel.ChannelID(), cidlink.DefaultLinkSystem())
		th.Fgs.AssertHasPersistenceOption(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()))
	})
	t.Run("receives outgoing request hook", func(t *testing.T) {
		th.OutgoingRequestHook(request)
		require.Equal(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()), th.OutgoingRequestHookActions.PersistenceOption)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportOpenedChannel{})
	})
	t.Run("receives outgoing processing listener", func(t *testing.T) {
		th.OutgoingRequestProcessingListener(request)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportInitiatedTransfer{})
	})
	dtResponse := th.Response()
	response := receivedRequest.Response(t, dtResponse, nil, graphsync.PartialResponse)
	t.Run("receives response", func(t *testing.T) {
		th.IncomingResponseHook(response)
		require.Equal(t, th.Events.ReceivedResponse, dtResponse)
	})

	t.Run("received block", func(t *testing.T) {
		block := testharness.NewFakeBlockData(12345, 1, true)
		th.IncomingBlockHook(response, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		block = testharness.NewFakeBlockData(12345, 2, true)
		th.IncomingBlockHook(response, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		// block not on wire has no effect
		block = testharness.NewFakeBlockData(12345, 3, false)
		th.IncomingBlockHook(response, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		// block with lower index has no effect
		block = testharness.NewFakeBlockData(67890, 1, true)
		th.IncomingBlockHook(response, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
	})

	t.Run("receive pause", func(t *testing.T) {
		dtPauseResponse := th.UpdateResponse(true)
		pauseResponse := receivedRequest.Response(t, nil, dtPauseResponse, graphsync.RequestPaused)
		th.IncomingResponseHook(pauseResponse)
		require.Equal(t, th.Events.ReceivedResponse, dtPauseResponse)
	})

	t.Run("send update", func(t *testing.T) {
		vRequest := th.VoucherRequest()
		th.Transport.SendMessage(ctx, th.Channel.ChannelID(), vRequest)
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: vRequest})
	})

	t.Run("receive resume", func(t *testing.T) {
		dtResumeResponse := th.UpdateResponse(false)
		pauseResponse := receivedRequest.Response(t, nil, dtResumeResponse, graphsync.PartialResponse)
		th.IncomingResponseHook(pauseResponse)
		require.Equal(t, th.Events.ReceivedResponse, dtResumeResponse)
	})

	t.Run("pause", func(t *testing.T) {
		th.Channel.SetInitiatorPaused(true)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateRequest(true))
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateRequest(true)})
		require.Len(t, th.Fgs.Pauses, 1)
		require.Equal(t, th.Fgs.Pauses[0], request.ID())
	})
	t.Run("pause again", func(t *testing.T) {
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateRequest(true))
		// should send message again
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateRequest(true)})
		// should not pause again
		require.Len(t, th.Fgs.Pauses, 1)
	})
	t.Run("resume", func(t *testing.T) {
		th.Channel.SetInitiatorPaused(false)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateRequest(false))
		require.Len(t, th.Fgs.Resumes, 1)
		resume := th.Fgs.Resumes[0]
		require.Equal(t, request.ID(), resume.RequestID)
		msg := resume.DTMessage(t)
		require.Equal(t, msg, th.UpdateRequest(false))
	})
	t.Run("resume again", func(t *testing.T) {
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateRequest(false))
		// should send message again
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateRequest(true)})
		// should not resume again
		require.Len(t, th.Fgs.Resumes, 1)
	})

	t.Run("restart request", func(t *testing.T) {
		restartIndex := int64(5)
		th.Channel.SetReceivedIndex(basicnode.NewInt(restartIndex))
		err := th.Transport.RestartChannel(ctx, th.Channel, th.RestartRequest(t))
		require.NoError(t, err)
		require.Len(t, th.DtNet.ProtectedPeers, 2)
		require.Equal(t, th.DtNet.ProtectedPeers[1], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
		require.Len(t, th.DtNet.ConnectWithRetryAttempts, 1)
		require.Equal(t, th.DtNet.ConnectWithRetryAttempts[0], testharness.ConnectWithRetryAttempt{th.Channel.OtherPeer(), "graphsync"})
		require.Len(t, th.Fgs.Cancels, 1)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportTransferCancelled{ErrorMessage: "graphsync request cancelled"})
		require.Equal(t, request.ID(), th.Fgs.Cancels[0])
		require.Len(t, th.Fgs.ReceivedRequests, 2)
		receivedRequest = th.Fgs.ReceivedRequests[1]
		request = receivedRequest.ToRequestData(t)
		msg, err := extension.GetTransferData(request, []graphsync.ExtensionName{
			extension.ExtensionDataTransfer1_1,
		})
		require.NoError(t, err)
		require.Equal(t, th.RestartRequest(t), msg)
		nd, has := request.Extension(graphsync.ExtensionsDoNotSendFirstBlocks)
		require.True(t, has)
		val, err := nd.AsInt()
		require.NoError(t, err)
		require.Equal(t, restartIndex, val)
	})

	t.Run("complete request", func(t *testing.T) {
		close(receivedRequest.ResponseChan)
		close(receivedRequest.ResponseErrChan)
		select {
		case <-th.CompletedRequests:
		case <-ctx.Done():
			t.Fatalf("did not complete request")
		}
		th.Events.AssertTransportEventEventually(t, th.Channel.ChannelID(), datatransfer.TransportCompletedTransfer{Success: true})
	})

	t.Run("cleanup request", func(t *testing.T) {
		th.Transport.CleanupChannel(th.Channel.ChannelID())
		require.Len(t, th.DtNet.UnprotectedPeers, 1)
		require.Equal(t, th.DtNet.UnprotectedPeers[0], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
	})
}

type ctxKey struct{}

func TestInitiatingPushRequestSuccessFlow(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	th := testharness.SetupHarness(ctx)
	t.Run("opens successfully", func(t *testing.T) {
		err := th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
		require.NoError(t, err)
		require.Len(t, th.DtNet.ProtectedPeers, 1)
		require.Equal(t, th.DtNet.ProtectedPeers[0], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.NewRequest(t)})
	})
	t.Run("configures persistence", func(t *testing.T) {
		th.Transport.UseStore(th.Channel.ChannelID(), cidlink.DefaultLinkSystem())
		th.Fgs.AssertHasPersistenceOption(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()))
	})
	dtResponse := th.Response()
	requestID := graphsync.NewRequestID()
	request := testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtResponse.ToIPLD()}, graphsync.RequestTypeNew)
	//response := receivedRequest.Response(t, dtResponse, nil, graphsync.PartialResponse)
	t.Run("receives incoming request hook", func(t *testing.T) {
		th.Events.ReturnedOnContextAugmentFunc = func(ctx context.Context) context.Context {
			return context.WithValue(ctx, ctxKey{}, "applesauce")
		}
		th.IncomingRequestHook(request)
		require.Equal(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()), th.IncomingRequestHookActions.PersistenceOption)
		require.True(t, th.IncomingRequestHookActions.Validated)
		require.False(t, th.IncomingBlockHookActions.Paused)
		require.NoError(t, th.IncomingRequestHookActions.TerminationError)
		th.IncomingRequestHookActions.AssertAugmentedContextKey(t, ctxKey{}, "applesauce")
		require.Equal(t, th.Events.ReceivedResponse, dtResponse)
	})

	t.Run("receives incoming processing listener", func(t *testing.T) {
		th.IncomingRequestProcessingListener(request)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportInitiatedTransfer{})
	})

	t.Run("queued block", func(t *testing.T) {
		block := testharness.NewFakeBlockData(12345, 1, true)
		th.OutgoingBlockHook(request, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		block = testharness.NewFakeBlockData(12345, 2, true)
		th.OutgoingBlockHook(request, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		// block not on wire has no effect
		block = testharness.NewFakeBlockData(12345, 3, false)
		th.OutgoingBlockHook(request, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		// block with lower index has no effect
		block = testharness.NewFakeBlockData(67890, 1, true)
		th.OutgoingBlockHook(request, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
	})

	t.Run("sent block", func(t *testing.T) {
		block := testharness.NewFakeBlockData(12345, 1, true)
		th.BlockSentListener(request, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportSentData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		block = testharness.NewFakeBlockData(12345, 2, true)
		th.BlockSentListener(request, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportSentData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		// block not on wire has no effect
		block = testharness.NewFakeBlockData(12345, 3, false)
		th.BlockSentListener(request, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportSentData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		// block with lower index has no effect
		block = testharness.NewFakeBlockData(67890, 1, true)
		th.BlockSentListener(request, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportSentData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
	})

	t.Run("receive pause", func(t *testing.T) {
		th.RequestorCancelledListener(request)
		dtPauseResponse := th.UpdateResponse(true)
		th.DtNet.Delegates[0].Receiver.ReceiveResponse(ctx, th.Channel.OtherPeer(), dtPauseResponse)
		require.Equal(t, th.Events.ReceivedResponse, dtPauseResponse)
	})

	t.Run("send update", func(t *testing.T) {
		vRequest := th.VoucherRequest()
		th.Transport.SendMessage(ctx, th.Channel.ChannelID(), vRequest)
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: vRequest})
	})

	t.Run("receive resume", func(t *testing.T) {
		dtResumeResponse := th.UpdateResponse(false)
		request = testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtResumeResponse.ToIPLD()}, graphsync.RequestTypeNew)
		// reset hook behavior
		th.IncomingRequestHookActions = &testharness.FakeIncomingRequestHookActions{}
		th.IncomingRequestHook(request)
		require.Equal(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()), th.IncomingRequestHookActions.PersistenceOption)
		require.True(t, th.IncomingRequestHookActions.Validated)
		require.False(t, th.IncomingBlockHookActions.Paused)
		require.NoError(t, th.IncomingRequestHookActions.TerminationError)
		th.IncomingRequestHookActions.AssertAugmentedContextKey(t, ctxKey{}, "applesauce")
		require.Equal(t, th.Events.ReceivedResponse, dtResumeResponse)
	})

	t.Run("pause", func(t *testing.T) {
		th.Channel.SetInitiatorPaused(true)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateRequest(true))
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateRequest(true)})
		require.Len(t, th.Fgs.Pauses, 1)
		require.Equal(t, th.Fgs.Pauses[0], request.ID())
	})
	t.Run("pause again", func(t *testing.T) {
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateRequest(true))
		// should send message again
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateRequest(true)})
		// should not pause again
		require.Len(t, th.Fgs.Pauses, 1)
	})
	t.Run("resume", func(t *testing.T) {
		th.Channel.SetInitiatorPaused(false)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateRequest(false))
		require.Len(t, th.Fgs.Resumes, 1)
		resume := th.Fgs.Resumes[0]
		require.Equal(t, request.ID(), resume.RequestID)
		msg := resume.DTMessage(t)
		require.Equal(t, msg, th.UpdateRequest(false))
	})
	t.Run("resume again", func(t *testing.T) {
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateRequest(false))
		// should send message again
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateRequest(true)})
		// should not resume again
		require.Len(t, th.Fgs.Resumes, 1)
	})

	t.Run("restart request", func(t *testing.T) {
		err := th.Transport.RestartChannel(ctx, th.Channel, th.RestartRequest(t))
		require.NoError(t, err)
		require.Len(t, th.DtNet.ProtectedPeers, 2)
		require.Equal(t, th.DtNet.ProtectedPeers[1], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
		require.Len(t, th.DtNet.ConnectWithRetryAttempts, 1)
		require.Equal(t, th.DtNet.ConnectWithRetryAttempts[0], testharness.ConnectWithRetryAttempt{th.Channel.OtherPeer(), "graphsync"})
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.NewRequest(t)})
	})

	t.Run("complete request", func(t *testing.T) {
		th.ResponseCompletedListener(request, graphsync.RequestCompletedFull)
		select {
		case <-th.CompletedResponses:
		case <-ctx.Done():
			t.Fatalf("did not complete request")
		}
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportCompletedTransfer{Success: true})
	})

	t.Run("cleanup request", func(t *testing.T) {
		th.Transport.CleanupChannel(th.Channel.ChannelID())
		require.Len(t, th.DtNet.UnprotectedPeers, 1)
		require.Equal(t, th.DtNet.UnprotectedPeers[0], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
	})
}

/*	"gs outgoing request with recognized dt push channel will record incoming blocks": {
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

	"incoming request can be queued": {
		action: func(gsData *harness) {
			gsData.incomingRequestQueuedHook()
		},
		check: func(t *testing.T, events *fakeEvents, gsData *harness) {
			require.True(t, events.TransferQueuedCalled)
			require.Equal(t, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.self, Initiator: gsData.other},
				events.TransferQueuedChannelID)
		},
	},

	"incoming request with dtResponse can be queued": {
		requestConfig: gsRequestConfig{
			dtIsResponse: true,
		},
		responseConfig: gsResponseConfig{
			dtIsResponse: true,
		},
		action: func(gsData *harness) {
			gsData.incomingRequestQueuedHook()
		},
		check: func(t *testing.T, events *fakeEvents, gsData *harness) {
			require.True(t, events.TransferQueuedCalled)
			require.Equal(t, datatransfer.ChannelID{ID: gsData.transferID, Responder: gsData.other, Initiator: gsData.self},
				events.TransferQueuedChannelID)
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
			cids := testutil.GenerateCids(2)
			channel := testutil.NewMockChannelState(testutil.MockChannelStateParams{ReceivedCids: cids})
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
			cids := testutil.GenerateCids(2)
			channel := testutil.NewMockChannelState(testutil.MockChannelStateParams{ReceivedCids: cids})
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
			cids := testutil.GenerateCids(2)
			channel := testutil.NewMockChannelState(testutil.MockChannelStateParams{ReceivedCids: cids})
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
			request := testutil.NewFakeRequest(graphsync.NewRequestID(), extensions)
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
			request := testutil.NewFakeRequest(graphsync.NewRequestID(), extensions)
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
	},*/
