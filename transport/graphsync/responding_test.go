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

func TestRespondingPullSuccessFlow(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	th := testharness.SetupHarness(ctx, testharness.PullRequest(), testharness.Responder())

	// this actually happens in the request received event handler itself in a real life case, but here we just run it before
	t.Run("configures persistence", func(t *testing.T) {
		th.Transport.UseStore(th.Channel.ChannelID(), cidlink.DefaultLinkSystem())
		th.Fgs.AssertHasPersistenceOption(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()))
	})
	requestID := graphsync.NewRequestID()
	dtRequest := th.NewRequest(t)
	request := testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtRequest.ToIPLD()}, graphsync.RequestTypeNew)

	// this the actual start of request processing
	t.Run("received and responds successfully", func(t *testing.T) {
		dtResponse := th.Response()
		th.Events.ReturnedRequestReceivedResponse = dtResponse
		th.Channel.SetResponderPaused(true)
		th.Channel.SetDataLimit(10000)
		th.Events.ReturnedOnContextAugmentFunc = func(ctx context.Context) context.Context {
			return context.WithValue(ctx, ctxKey{}, "applesauce")
		}
		th.IncomingRequestHook(request)
		require.Equal(t, dtRequest, th.Events.ReceivedRequest)
		require.Len(t, th.DtNet.ProtectedPeers, 1)
		require.Equal(t, th.DtNet.ProtectedPeers[0], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
		require.Equal(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()), th.IncomingRequestHookActions.PersistenceOption)
		require.True(t, th.IncomingRequestHookActions.Validated)
		require.True(t, th.IncomingRequestHookActions.Paused)
		require.NoError(t, th.IncomingRequestHookActions.TerminationError)
		sentResponse := th.IncomingRequestHookActions.DTMessage(t)
		require.Equal(t, dtResponse, sentResponse)
		th.IncomingRequestHookActions.AssertAugmentedContextKey(t, ctxKey{}, "applesauce")
	})

	t.Run("receives incoming processing listener", func(t *testing.T) {
		th.IncomingRequestProcessingListener(request)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportInitiatedTransfer{})
	})

	t.Run("unpause request", func(t *testing.T) {
		th.Channel.SetResponderPaused(false)
		dtValidationResponse := th.ValidationResultResponse(false)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), dtValidationResponse)
		require.Len(t, th.Fgs.Resumes, 1)
		require.Equal(t, dtValidationResponse, th.Fgs.Resumes[0].DTMessage(t))
	})

	t.Run("queued block / data limits", func(t *testing.T) {
		// consume first block
		block := testharness.NewFakeBlockData(8000, 1, true)
		th.OutgoingBlockHook(request, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})

		// consume second block -- should hit data limit
		block = testharness.NewFakeBlockData(3000, 2, true)
		th.OutgoingBlockHook(request, block)
		require.True(t, th.OutgoingBlockHookActions.Paused)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReachedDataLimit{})

		// reset data limit
		th.Channel.SetResponderPaused(false)
		th.Channel.SetDataLimit(20000)
		dtValidationResponse := th.ValidationResultResponse(false)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), dtValidationResponse)
		require.Len(t, th.Fgs.Resumes, 2)
		require.Equal(t, dtValidationResponse, th.Fgs.Resumes[1].DTMessage(t))

		// block not on wire has no effect
		block = testharness.NewFakeBlockData(12345, 3, false)
		th.OutgoingBlockHook(request, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		// block with lower index has no effect
		block = testharness.NewFakeBlockData(67890, 1, true)
		th.OutgoingBlockHook(request, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})

		// consume third block
		block = testharness.NewFakeBlockData(5000, 4, true)
		th.OutgoingBlockHook(request, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})

		// consume fourth block should hit data limit again
		block = testharness.NewFakeBlockData(5000, 5, true)
		th.OutgoingBlockHook(request, block)
		require.True(t, th.OutgoingBlockHookActions.Paused)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReachedDataLimit{})

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
		dtPauseRequest := th.UpdateRequest(true)
		th.Events.ReturnedRequestReceivedResponse = nil
		th.DtNet.Delegates[0].Receiver.ReceiveRequest(ctx, th.Channel.OtherPeer(), dtPauseRequest)
		require.Equal(t, th.Events.ReceivedRequest, dtPauseRequest)
	})

	t.Run("receive resume", func(t *testing.T) {
		dtResumeRequest := th.UpdateRequest(false)
		request = testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtResumeRequest.ToIPLD()}, graphsync.RequestTypeNew)
		// reset hook behavior
		th.IncomingRequestHookActions = &testharness.FakeIncomingRequestHookActions{}
		th.IncomingRequestHook(request)
		// only protect on new and restart requests
		require.Len(t, th.DtNet.ProtectedPeers, 1)
		require.Equal(t, th.DtNet.ProtectedPeers[0], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
		require.Equal(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()), th.IncomingRequestHookActions.PersistenceOption)
		require.True(t, th.IncomingRequestHookActions.Validated)
		require.False(t, th.IncomingBlockHookActions.Paused)
		require.NoError(t, th.IncomingRequestHookActions.TerminationError)
		th.IncomingRequestHookActions.AssertAugmentedContextKey(t, ctxKey{}, "applesauce")
		require.Equal(t, th.Events.ReceivedRequest, dtResumeRequest)
	})

	t.Run("pause", func(t *testing.T) {
		th.Channel.SetResponderPaused(true)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateResponse(true))
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateResponse(true)})
		require.Len(t, th.Fgs.Pauses, 1)
		require.Equal(t, th.Fgs.Pauses[0], request.ID())
	})

	t.Run("pause again", func(t *testing.T) {
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateResponse(true))
		// should send message again
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateResponse(true)})
		// should not pause again
		require.Len(t, th.Fgs.Pauses, 1)
	})

	t.Run("resume", func(t *testing.T) {
		th.Channel.SetResponderPaused(false)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateResponse(true))
		require.Len(t, th.Fgs.Resumes, 3)
		resume := th.Fgs.Resumes[2]
		require.Equal(t, request.ID(), resume.RequestID)
		msg := resume.DTMessage(t)
		require.Equal(t, msg, th.UpdateResponse(false))
	})
	t.Run("resume again", func(t *testing.T) {
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateResponse(false))
		// should send message again
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateResponse(true)})
		// should not resume again
		require.Len(t, th.Fgs.Resumes, 3)
	})

	t.Run("restart request", func(t *testing.T) {
		dtRestartRequest := th.RestartRequest(t)
		request := testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtRestartRequest.ToIPLD()}, graphsync.RequestTypeNew)
		th.IncomingRequestHook(request)
		// protect again for a restart
		require.Len(t, th.DtNet.ProtectedPeers, 2)
		require.Equal(t, th.DtNet.ProtectedPeers[1], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
		require.Equal(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()), th.IncomingRequestHookActions.PersistenceOption)
		require.True(t, th.IncomingRequestHookActions.Validated)
		require.False(t, th.IncomingRequestHookActions.Paused)
		require.NoError(t, th.IncomingRequestHookActions.TerminationError)
		th.IncomingRequestHookActions.AssertAugmentedContextKey(t, ctxKey{}, "applesauce")
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

func TestRespondingPushSuccessFlow(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	th := testharness.SetupHarness(ctx, testharness.Responder())
	var receivedRequest testharness.ReceivedGraphSyncRequest
	var request graphsync.RequestData

	contextAugmentedCalls := []struct{}{}
	th.Events.ReturnedOnContextAugmentFunc = func(ctx context.Context) context.Context {
		contextAugmentedCalls = append(contextAugmentedCalls, struct{}{})
		return ctx
	}
	t.Run("configures persistence", func(t *testing.T) {
		th.Transport.UseStore(th.Channel.ChannelID(), cidlink.DefaultLinkSystem())
		th.Fgs.AssertHasPersistenceOption(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()))
	})
	t.Run("receive new request", func(t *testing.T) {
		dtResponse := th.Response()
		th.Events.ReturnedRequestReceivedResponse = dtResponse
		th.Channel.SetResponderPaused(true)
		th.Channel.SetDataLimit(10000)

		th.DtNet.Delegates[0].Receiver.ReceiveRequest(ctx, th.Channel.OtherPeer(), th.NewRequest(t))
		require.Equal(t, th.NewRequest(t), th.Events.ReceivedRequest)
		require.Len(t, th.DtNet.ProtectedPeers, 1)
		require.Equal(t, th.DtNet.ProtectedPeers[0], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
		require.Len(t, th.Fgs.ReceivedRequests, 1)
		receivedRequest = th.Fgs.ReceivedRequests[0]
		request = receivedRequest.ToRequestData(t)
		msg, err := extension.GetTransferData(request, []graphsync.ExtensionName{
			extension.ExtensionDataTransfer1_1,
		})
		require.NoError(t, err)
		require.Equal(t, dtResponse, msg)
		require.Len(t, th.Fgs.Pauses, 1)
		require.Equal(t, request.ID(), th.Fgs.Pauses[0])
		require.Len(t, contextAugmentedCalls, 1)
	})

	t.Run("unpause request", func(t *testing.T) {
		th.Channel.SetResponderPaused(false)
		dtValidationResponse := th.ValidationResultResponse(false)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), dtValidationResponse)
		require.Len(t, th.Fgs.Resumes, 1)
		require.Equal(t, dtValidationResponse, th.Fgs.Resumes[0].DTMessage(t))
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
	response := receivedRequest.Response(t, nil, nil, graphsync.PartialResponse)
	t.Run("received block / data limits", func(t *testing.T) {
		th.IncomingResponseHook(response)
		// consume first block
		block := testharness.NewFakeBlockData(8000, 1, true)
		th.IncomingBlockHook(response, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})

		// consume second block -- should hit data limit
		block = testharness.NewFakeBlockData(3000, 2, true)
		th.IncomingBlockHook(response, block)
		require.True(t, th.IncomingBlockHookActions.Paused)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReachedDataLimit{})

		// reset data limit
		th.Channel.SetResponderPaused(false)
		th.Channel.SetDataLimit(20000)
		dtValidationResponse := th.ValidationResultResponse(false)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), dtValidationResponse)
		require.Len(t, th.Fgs.Resumes, 2)
		require.Equal(t, dtValidationResponse, th.Fgs.Resumes[1].DTMessage(t))

		// block not on wire has no effect
		block = testharness.NewFakeBlockData(12345, 3, false)
		th.IncomingBlockHook(response, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		// block with lower index has no effect
		block = testharness.NewFakeBlockData(67890, 1, true)
		th.OutgoingBlockHook(request, block)
		th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})

		// consume third block
		block = testharness.NewFakeBlockData(5000, 4, true)
		th.IncomingBlockHook(response, block)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})

		// consume fourth block should hit data limit again
		block = testharness.NewFakeBlockData(5000, 5, true)
		th.IncomingBlockHook(response, block)
		require.True(t, th.IncomingBlockHookActions.Paused)
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
		th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReachedDataLimit{})

	})

	t.Run("receive pause", func(t *testing.T) {
		dtPauseRequest := th.UpdateRequest(true)
		pauseResponse := receivedRequest.Response(t, nil, dtPauseRequest, graphsync.RequestPaused)
		th.IncomingResponseHook(pauseResponse)
		th.Events.ReturnedRequestReceivedResponse = nil
		require.Equal(t, th.Events.ReceivedRequest, dtPauseRequest)
	})

	t.Run("receive resume", func(t *testing.T) {
		dtResumeRequest := th.UpdateRequest(false)
		pauseResponse := receivedRequest.Response(t, nil, dtResumeRequest, graphsync.PartialResponse)
		th.IncomingResponseHook(pauseResponse)
		require.Equal(t, th.Events.ReceivedRequest, dtResumeRequest)
	})

	t.Run("pause", func(t *testing.T) {
		th.Channel.SetResponderPaused(true)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateResponse(true))
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateResponse(true)})
		require.Len(t, th.Fgs.Pauses, 1)
		require.Equal(t, th.Fgs.Pauses[0], request.ID())
	})
	t.Run("pause again", func(t *testing.T) {
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateResponse(true))
		// should send message again
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateResponse(true)})
		// should not pause again
		require.Len(t, th.Fgs.Pauses, 1)
	})
	t.Run("resume", func(t *testing.T) {
		th.Channel.SetResponderPaused(false)
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateResponse(false))
		require.Len(t, th.Fgs.Resumes, 3)
		resume := th.Fgs.Resumes[2]
		require.Equal(t, request.ID(), resume.RequestID)
		msg := resume.DTMessage(t)
		require.Equal(t, msg, th.UpdateResponse(false))
	})
	t.Run("resume again", func(t *testing.T) {
		th.Transport.ChannelUpdated(ctx, th.Channel.ChannelID(), th.UpdateResponse(false))
		// should send message again
		th.DtNet.AssertSentMessage(t, testharness.FakeSentMessage{PeerID: th.Channel.OtherPeer(), TransportID: "graphsync", Message: th.UpdateResponse(false)})
		// should not resume again
		require.Len(t, th.Fgs.Resumes, 3)
	})

	t.Run("restart request", func(t *testing.T) {
		restartIndex := int64(5)
		th.Channel.SetReceivedIndex(basicnode.NewInt(restartIndex))
		dtResponse := th.RestartResponse(false)
		th.Events.ReturnedRequestReceivedResponse = dtResponse
		th.DtNet.Delegates[0].Receiver.ReceiveRequest(ctx, th.Channel.OtherPeer(), th.NewRequest(t))
		require.Len(t, th.DtNet.ProtectedPeers, 2)
		require.Equal(t, th.DtNet.ProtectedPeers[1], testharness.TaggedPeer{th.Channel.OtherPeer(), th.Channel.ChannelID().String()})
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
		require.Equal(t, dtResponse, msg)
		nd, has := request.Extension(graphsync.ExtensionsDoNotSendFirstBlocks)
		require.True(t, has)
		val, err := nd.AsInt()
		require.NoError(t, err)
		require.Equal(t, restartIndex, val)
		require.Len(t, contextAugmentedCalls, 2)
	})

	t.Run("complete request", func(t *testing.T) {
		close(receivedRequest.ResponseChan)
		close(receivedRequest.ResponseErrChan)
		select {
		case <-th.CompletedRequests:
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
