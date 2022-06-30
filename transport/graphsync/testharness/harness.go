package testharness

import (
	"context"
	"math/rand"
	"testing"

	"github.com/ipfs/go-graphsync"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
	dtgs "github.com/filecoin-project/go-data-transfer/v2/transport/graphsync"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
)

type harnessConfig struct {
	isPull           bool
	isResponder      bool
	makeEvents       func(gsData *GsTestHarness) *FakeEvents
	makeNetwork      func(gsData *GsTestHarness) *FakeNetwork
	transportOptions []dtgs.Option
}

type Option func(*harnessConfig)

func PullRequest() Option {
	return func(hc *harnessConfig) {
		hc.isPull = true
	}
}

func Responder() Option {
	return func(hc *harnessConfig) {
		hc.isResponder = true
	}
}

func Events(makeEvents func(gsData *GsTestHarness) *FakeEvents) Option {
	return func(hc *harnessConfig) {
		hc.makeEvents = makeEvents
	}
}

func Network(makeNetwork func(gsData *GsTestHarness) *FakeNetwork) Option {
	return func(hc *harnessConfig) {
		hc.makeNetwork = makeNetwork
	}
}

func TransportOptions(options []dtgs.Option) Option {
	return func(hc *harnessConfig) {
		hc.transportOptions = options
	}
}

func SetupHarness(ctx context.Context, options ...Option) *GsTestHarness {
	hc := &harnessConfig{}
	for _, option := range options {
		option(hc)
	}
	peers := testutil.GeneratePeers(2)
	transferID := datatransfer.TransferID(rand.Uint32())
	fgs := NewFakeGraphSync()
	fgs.LeaveRequestsOpen()
	voucher := testutil.NewTestTypedVoucher()
	baseCid := testutil.GenerateCids(1)[0]
	selector := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any).Matcher().Node()
	chid := datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferID}
	if hc.isResponder {
		chid = datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: transferID}
	}
	channel := testutil.NewMockChannelState(testutil.MockChannelStateParams{
		BaseCID:   baseCid,
		Voucher:   voucher,
		Selector:  selector,
		IsPull:    hc.isPull,
		Self:      peers[0],
		ChannelID: chid,
	})
	gsData := &GsTestHarness{
		Ctx:                         ctx,
		Fgs:                         fgs,
		Channel:                     channel,
		CompletedRequests:           make(chan datatransfer.ChannelID, 16),
		CompletedResponses:          make(chan datatransfer.ChannelID, 16),
		OutgoingRequestHookActions:  &FakeOutgoingRequestHookActions{},
		OutgoingBlockHookActions:    &FakeOutgoingBlockHookActions{},
		IncomingBlockHookActions:    &FakeIncomingBlockHookActions{},
		IncomingRequestHookActions:  &FakeIncomingRequestHookActions{},
		RequestUpdateHookActions:    &FakeRequestUpdatedActions{},
		IncomingResponseHookActions: &FakeIncomingResponseHookActions{},
	}
	if hc.makeEvents != nil {
		gsData.Events = hc.makeEvents(gsData)
	} else {
		gsData.Events = &FakeEvents{
			ReturnedChannelState: channel,
		}
	}
	if hc.makeNetwork != nil {
		gsData.DtNet = hc.makeNetwork(gsData)
	} else {
		gsData.DtNet = NewFakeNetwork(peers[0])
	}
	gsData.Transport = dtgs.NewTransport(gsData.Fgs, gsData.DtNet,
		append(hc.transportOptions,
			dtgs.RegisterCompletedRequestListener(gsData.completedRequestListener),
			dtgs.RegisterCompletedResponseListener(gsData.completedResponseListener))...)
	gsData.Transport.SetEventHandler(gsData.Events)
	return gsData
}

type GsTestHarness struct {
	Ctx                         context.Context
	Fgs                         *FakeGraphSync
	Channel                     *testutil.MockChannelState
	RequestID                   graphsync.RequestID
	AltRequestID                graphsync.RequestID
	Events                      *FakeEvents
	DtNet                       *FakeNetwork
	OutgoingRequestHookActions  *FakeOutgoingRequestHookActions
	IncomingBlockHookActions    *FakeIncomingBlockHookActions
	OutgoingBlockHookActions    *FakeOutgoingBlockHookActions
	IncomingRequestHookActions  *FakeIncomingRequestHookActions
	RequestUpdateHookActions    *FakeRequestUpdatedActions
	IncomingResponseHookActions *FakeIncomingResponseHookActions
	Transport                   *dtgs.Transport
	CompletedRequests           chan datatransfer.ChannelID
	CompletedResponses          chan datatransfer.ChannelID
}

func (th *GsTestHarness) completedRequestListener(chid datatransfer.ChannelID) {
	th.CompletedRequests <- chid
}
func (th *GsTestHarness) completedResponseListener(chid datatransfer.ChannelID) {
	th.CompletedResponses <- chid
}

func (th *GsTestHarness) NewRequest(t *testing.T) datatransfer.Request {
	vouch := th.Channel.Voucher()
	message, err := message.NewRequest(th.Channel.TransferID(), false, th.Channel.IsPull(), &vouch, th.Channel.BaseCID(), th.Channel.Selector())
	require.NoError(t, err)
	return message
}

func (th *GsTestHarness) RestartRequest(t *testing.T) datatransfer.Request {
	vouch := th.Channel.Voucher()
	message, err := message.NewRequest(th.Channel.TransferID(), true, th.Channel.IsPull(), &vouch, th.Channel.BaseCID(), th.Channel.Selector())
	require.NoError(t, err)
	return message
}

func (th *GsTestHarness) VoucherRequest() datatransfer.Request {
	newVouch := testutil.NewTestTypedVoucher()
	return message.VoucherRequest(th.Channel.TransferID(), &newVouch)
}

func (th *GsTestHarness) UpdateRequest(pause bool) datatransfer.Request {
	return message.UpdateRequest(th.Channel.TransferID(), pause)
}

func (th *GsTestHarness) Response() datatransfer.Response {
	voucherResult := testutil.NewTestTypedVoucher()
	return message.NewResponse(th.Channel.TransferID(), true, false, &voucherResult)
}

func (th *GsTestHarness) ValidationResultResponse(pause bool) datatransfer.Response {
	voucherResult := testutil.NewTestTypedVoucher()
	return message.ValidationResultResponse(types.VoucherResultMessage, th.Channel.TransferID(), datatransfer.ValidationResult{VoucherResult: &voucherResult, Accepted: true}, nil, pause)
}

func (th *GsTestHarness) RestartResponse(pause bool) datatransfer.Response {
	voucherResult := testutil.NewTestTypedVoucher()
	return message.ValidationResultResponse(types.RestartMessage, th.Channel.TransferID(), datatransfer.ValidationResult{VoucherResult: &voucherResult, Accepted: true}, nil, pause)
}

func (th *GsTestHarness) UpdateResponse(paused bool) datatransfer.Response {
	return message.UpdateResponse(th.Channel.TransferID(), true)
}

func (th *GsTestHarness) OutgoingRequestHook(request graphsync.RequestData) {
	th.Fgs.OutgoingRequestHook(th.Channel.OtherPeer(), request, th.OutgoingRequestHookActions)
}

func (th *GsTestHarness) OutgoingRequestProcessingListener(request graphsync.RequestData) {
	th.Fgs.OutgoingRequestProcessingListener(th.Channel.OtherPeer(), request, 0)
}

func (th *GsTestHarness) IncomingBlockHook(response graphsync.ResponseData, block graphsync.BlockData) {
	th.Fgs.IncomingBlockHook(th.Channel.OtherPeer(), response, block, th.IncomingBlockHookActions)
}

func (th *GsTestHarness) OutgoingBlockHook(request graphsync.RequestData, block graphsync.BlockData) {
	th.Fgs.OutgoingBlockHook(th.Channel.OtherPeer(), request, block, th.OutgoingBlockHookActions)
}

func (th *GsTestHarness) IncomingRequestHook(request graphsync.RequestData) {
	th.Fgs.IncomingRequestHook(th.Channel.OtherPeer(), request, th.IncomingRequestHookActions)
}

func (th *GsTestHarness) IncomingRequestProcessingListener(request graphsync.RequestData) {
	th.Fgs.IncomingRequestProcessingListener(th.Channel.OtherPeer(), request, 1)
}

func (th *GsTestHarness) IncomingResponseHook(response graphsync.ResponseData) {
	th.Fgs.IncomingResponseHook(th.Channel.OtherPeer(), response, th.IncomingResponseHookActions)
}

func (th *GsTestHarness) ResponseCompletedListener(request graphsync.RequestData, code graphsync.ResponseStatusCode) {
	th.Fgs.CompletedResponseListener(th.Channel.OtherPeer(), request, code)
}

func (th *GsTestHarness) RequestorCancelledListener(request graphsync.RequestData) {
	th.Fgs.RequestorCancelledListener(th.Channel.OtherPeer(), request)
}

/*
func (ha *GsTestHarness) networkErrorListener(err error) {
	ha.Fgs.NetworkErrorListener(ha.other, ha.request, err)
}
func (ha *GsTestHarness) receiverNetworkErrorListener(err error) {
	ha.Fgs.ReceiverNetworkErrorListener(ha.other, err)
}
*/

func (th *GsTestHarness) BlockSentListener(request graphsync.RequestData, block graphsync.BlockData) {
	th.Fgs.BlockSentListener(th.Channel.OtherPeer(), request, block)
}

func (ha *GsTestHarness) makeRequest(requestID graphsync.RequestID, messageNode datamodel.Node, requestType graphsync.RequestType) graphsync.RequestData {
	extensions := make(map[graphsync.ExtensionName]datamodel.Node)
	if messageNode != nil {
		extensions[extension.ExtensionDataTransfer1_1] = messageNode
	}
	return NewFakeRequest(requestID, extensions, requestType)
}

func (ha *GsTestHarness) makeResponse(requestID graphsync.RequestID, messageNode datamodel.Node, responseCode graphsync.ResponseStatusCode) graphsync.ResponseData {
	extensions := make(map[graphsync.ExtensionName]datamodel.Node)
	if messageNode != nil {
		extensions[extension.ExtensionDataTransfer1_1] = messageNode
	}
	return NewFakeResponse(requestID, extensions, responseCode)
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
