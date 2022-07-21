package graphsync_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ipfs/go-graphsync"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/rand"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/testharness"
)

func TestTransferExceptions(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name       string
		parameters []testharness.Option
		test       func(t *testing.T, th *testharness.GsTestHarness)
	}{
		{
			name:       "error executing pull graphsync request",
			parameters: []testharness.Option{testharness.PullRequest()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
				receivedRequest := th.Fgs.ReceivedRequests[0]
				close(receivedRequest.ResponseChan)
				receivedRequest.ResponseErrChan <- errors.New("something went wrong")
				close(receivedRequest.ResponseErrChan)
				select {
				case <-th.CompletedRequests:
				case <-ctx.Done():
					t.Fatalf("did not complete request")
				}
				th.Events.AssertTransportEventEventually(t, th.Channel.ChannelID(), datatransfer.TransportCompletedTransfer{Success: false, ErrorMessage: fmt.Sprintf("channel %s: graphsync request failed to complete: something went wrong", th.Channel.ChannelID())})
			},
		},
		{
			name:       "unrecognized outgoing pull request",
			parameters: []testharness.Option{testharness.PullRequest()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				// open a channel
				th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
				// configure a store
				th.Transport.UseStore(th.Channel.ChannelID(), cidlink.DefaultLinkSystem())
				// setup a seperate request with different request ID but with contained message
				otherRequest := testharness.NewFakeRequest(graphsync.NewRequestID(), map[graphsync.ExtensionName]datamodel.Node{
					extension.ExtensionDataTransfer1_1: th.NewRequest(t).ToIPLD(),
				}, graphsync.RequestTypeNew)
				// run outgoing request hook on this request
				th.OutgoingRequestHook(otherRequest)
				// no channel opened
				th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportOpenedChannel{})
				// no store configuration
				require.Empty(t, th.OutgoingRequestHookActions.PersistenceOption)
				// run outgoing request processing listener
				th.OutgoingRequestProcessingListener(otherRequest)
				// no transfer initiated event
				th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportInitiatedTransfer{})
				dtResponse := th.Response()
				// create a response with the wrong request ID
				otherResponse := testharness.NewFakeResponse(otherRequest.ID(), map[graphsync.ExtensionName]datamodel.Node{
					extension.ExtensionIncomingRequest1_1: dtResponse.ToIPLD(),
				}, graphsync.PartialResponse)
				// run incoming response hook
				th.IncomingResponseHook(otherResponse)
				// no response received
				require.Nil(t, th.Events.ReceivedResponse)
				// run blook hook
				block := testharness.NewFakeBlockData(12345, 1, true)
				th.IncomingBlockHook(otherResponse, block)
				th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportReceivedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
			},
		},
		{
			name:       "error cancelling on restart request",
			parameters: []testharness.Option{testharness.PullRequest()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				// open a channel
				_ = th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
				th.Fgs.ReturnedCancelError = errors.New("something went wrong")
				err := th.Transport.RestartChannel(th.Ctx, th.Channel, th.RestartRequest(t))
				require.EqualError(t, err, fmt.Sprintf("%s: restarting graphsync request: cancelling graphsync request for channel %s: %s", th.Channel.ChannelID(), th.Channel.ChannelID(), "something went wrong"))
			},
		},
		{
			name:       "error reconnecting during restart",
			parameters: []testharness.Option{testharness.PullRequest()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				// open a channel
				_ = th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
				expectedErr := errors.New("something went wrong")
				th.DtNet.ReturnedConnectWithRetryError = expectedErr
				err := th.Transport.RestartChannel(th.Ctx, th.Channel, th.RestartRequest(t))
				require.ErrorIs(t, err, expectedErr)
			},
		},
		{
			name: "unrecognized incoming graphsync request dt response",
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				dtResponse := th.Response()
				requestID := graphsync.NewRequestID()
				request := testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtResponse.ToIPLD()}, graphsync.RequestTypeNew)
				th.IncomingRequestHook(request)
				require.False(t, th.IncomingRequestHookActions.Validated)
				require.Error(t, th.IncomingRequestHookActions.TerminationError)
				require.Equal(t, th.Events.ReceivedResponse, dtResponse)
			},
		},
		{
			name: "incoming graphsync request w/ dt response gets OnResponseReceived error",
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				_ = th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
				dtResponse := th.Response()
				requestID := graphsync.NewRequestID()
				request := testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtResponse.ToIPLD()}, graphsync.RequestTypeNew)
				th.Events.ReturnedResponseReceivedError = errors.New("something went wrong")
				th.IncomingRequestHook(request)
				require.False(t, th.IncomingRequestHookActions.Validated)
				require.EqualError(t, th.IncomingRequestHookActions.TerminationError, "something went wrong")
				require.Equal(t, th.Events.ReceivedResponse, dtResponse)
			},
		},
		{
			name:       "pull request cancelled",
			parameters: []testharness.Option{testharness.PullRequest()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				_ = th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
				require.Len(t, th.Fgs.ReceivedRequests, 1)
				receivedRequest := th.Fgs.ReceivedRequests[0]
				close(receivedRequest.ResponseChan)
				receivedRequest.ResponseErrChan <- graphsync.RequestClientCancelledErr{}
				close(receivedRequest.ResponseErrChan)
				th.Events.AssertTransportEventEventually(t, th.Channel.ChannelID(), datatransfer.TransportTransferCancelled{
					ErrorMessage: "graphsync request cancelled",
				})
			},
		},
		{
			name: "error opening sending push message",
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				th.DtNet.ReturnedSendMessageError = errors.New("something went wrong")
				err := th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
				require.EqualError(t, err, "something went wrong")
			},
		},
		{
			name: "unrecognized incoming graphsync push request",
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				// open a channel
				th.Transport.OpenChannel(th.Ctx, th.Channel, th.NewRequest(t))
				// configure a store
				th.Transport.UseStore(th.Channel.ChannelID(), cidlink.DefaultLinkSystem())
				voucherResult := testutil.NewTestTypedVoucher()
				otherRequest := testharness.NewFakeRequest(graphsync.NewRequestID(), map[graphsync.ExtensionName]datamodel.Node{
					extension.ExtensionDataTransfer1_1: message.NewResponse(datatransfer.TransferID(rand.Uint64()), true, false, &voucherResult).ToIPLD(),
				}, graphsync.RequestTypeNew)
				// run incoming request hook on new request
				th.IncomingRequestHook(otherRequest)
				// should error
				require.Error(t, th.IncomingRequestHookActions.TerminationError)
				// run incoming request processing listener
				th.IncomingRequestProcessingListener(otherRequest)
				// no transfer initiated event
				th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportInitiatedTransfer{})
				// run block queued hook
				block := testharness.NewFakeBlockData(12345, 1, true)
				th.OutgoingBlockHook(otherRequest, block)
				// no block queued event
				th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportQueuedData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
				// run block sent hook
				th.BlockSentListener(otherRequest, block)
				// no block sent event
				th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportSentData{Size: block.BlockSize(), Index: basicnode.NewInt(block.Index())})
				// run complete listener
				th.ResponseCompletedListener(otherRequest, graphsync.RequestCompletedFull)
				// no complete event
				th.Events.RefuteTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportCompletedTransfer{Success: true})
			},
		},
		{
			name: "channel update on unrecognized channel",
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				err := th.Transport.ChannelUpdated(th.Ctx, th.Channel.ChannelID(), th.NewRequest(t))
				require.Error(t, err)
			},
		},
		{
			name:       "incoming request errors in OnRequestReceived",
			parameters: []testharness.Option{testharness.PullRequest(), testharness.Responder()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				th.Transport.UseStore(th.Channel.ChannelID(), cidlink.DefaultLinkSystem())
				th.Fgs.AssertHasPersistenceOption(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()))
				requestID := graphsync.NewRequestID()
				dtRequest := th.NewRequest(t)
				request := testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtRequest.ToIPLD()}, graphsync.RequestTypeNew)
				voucherResult := testutil.NewTestTypedVoucher()
				dtResponse := message.NewResponse(th.Channel.TransferID(), false, false, &voucherResult)
				th.Events.ReturnedRequestReceivedResponse = dtResponse
				th.Events.ReturnedRequestReceivedError = errors.New("something went wrong")
				th.Events.ReturnedOnContextAugmentFunc = func(ctx context.Context) context.Context {
					return context.WithValue(ctx, ctxKey{}, "applesauce")
				}
				th.IncomingRequestHook(request)
				require.Equal(t, dtRequest, th.Events.ReceivedRequest)
				require.Empty(t, th.DtNet.ProtectedPeers)
				require.Empty(t, th.IncomingRequestHookActions.PersistenceOption)
				require.False(t, th.IncomingRequestHookActions.Validated)
				require.False(t, th.IncomingRequestHookActions.Paused)
				require.EqualError(t, th.IncomingRequestHookActions.TerminationError, "something went wrong")
				sentResponse := th.IncomingRequestHookActions.DTMessage(t)
				require.Equal(t, dtResponse, sentResponse)
				th.IncomingRequestHookActions.RefuteAugmentedContextKey(t, ctxKey{})
			},
		},
		{
			name:       "incoming gs request with contained push request errors",
			parameters: []testharness.Option{testharness.Responder()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				requestID := graphsync.NewRequestID()
				dtRequest := th.NewRequest(t)
				request := testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtRequest.ToIPLD()}, graphsync.RequestTypeNew)
				dtResponse := th.Response()
				th.Events.ReturnedRequestReceivedResponse = dtResponse
				th.IncomingRequestHook(request)
				require.EqualError(t, th.IncomingRequestHookActions.TerminationError, datatransfer.ErrUnsupported.Error())
			},
		},
		{
			name:       "incoming requests completes with error code for graphsync",
			parameters: []testharness.Option{testharness.PullRequest(), testharness.Responder()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				requestID := graphsync.NewRequestID()
				dtRequest := th.NewRequest(t)
				request := testharness.NewFakeRequest(requestID, map[graphsync.ExtensionName]datamodel.Node{extension.ExtensionDataTransfer1_1: dtRequest.ToIPLD()}, graphsync.RequestTypeNew)
				dtResponse := th.Response()
				th.Events.ReturnedRequestReceivedResponse = dtResponse
				th.IncomingRequestHook(request)

				th.ResponseCompletedListener(request, graphsync.RequestFailedUnknown)
				select {
				case <-th.CompletedResponses:
				case <-ctx.Done():
					t.Fatalf("did not complete request")
				}
				th.Events.AssertTransportEvent(t, th.Channel.ChannelID(), datatransfer.TransportCompletedTransfer{Success: false, ErrorMessage: fmt.Sprintf("graphsync response to peer %s did not complete: response status code %s", th.Channel.Recipient(), graphsync.RequestFailedUnknown.String())})

			},
		},
		{
			name:       "incoming push request message errors in OnRequestReceived",
			parameters: []testharness.Option{testharness.Responder()},
			test: func(t *testing.T, th *testharness.GsTestHarness) {
				th.Transport.UseStore(th.Channel.ChannelID(), cidlink.DefaultLinkSystem())
				th.Fgs.AssertHasPersistenceOption(t, fmt.Sprintf("data-transfer-%s", th.Channel.ChannelID().String()))
				voucherResult := testutil.NewTestTypedVoucher()
				dtResponse := message.NewResponse(th.Channel.TransferID(), false, false, &voucherResult)
				th.Events.ReturnedRequestReceivedResponse = dtResponse
				th.Events.ReturnedRequestReceivedError = errors.New("something went wrong")
				th.DtNet.Delegates[0].Receiver.ReceiveRequest(ctx, th.Channel.OtherPeer(), th.NewRequest(t))
				require.Equal(t, th.NewRequest(t), th.Events.ReceivedRequest)
				require.Empty(t, th.DtNet.ProtectedPeers)
				require.Empty(t, th.Fgs.ReceivedRequests)
				require.Len(t, th.DtNet.SentMessages, 1)
				require.Equal(t, testharness.FakeSentMessage{Message: dtResponse, TransportID: "graphsync", PeerID: th.Channel.OtherPeer()}, th.DtNet.SentMessages[0])
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			th := testharness.SetupHarness(ctx, testCase.parameters...)
			testCase.test(t, th)
		})
	}
}
