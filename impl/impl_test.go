package impl_test

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-graphsync"
	gsmsg "github.com/ipfs/go-graphsync/message"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/channels"
	. "github.com/filecoin-project/go-data-transfer/impl"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/filecoin-project/go-data-transfer/network"
	"github.com/filecoin-project/go-data-transfer/testutil"
	tp "github.com/filecoin-project/go-data-transfer/transport/graphsync"
	"github.com/filecoin-project/go-data-transfer/transport/graphsync/extension"
)

type receivedMessage struct {
	message message.DataTransferMessage
	sender  peer.ID
}

// Receiver is an interface for receiving messages from the GraphSyncNetwork.
type receiver struct {
	messageReceived chan receivedMessage
}

func (r *receiver) ReceiveRequest(
	ctx context.Context,
	sender peer.ID,
	incoming message.DataTransferRequest) {

	select {
	case <-ctx.Done():
	case r.messageReceived <- receivedMessage{incoming, sender}:
	}
}

func (r *receiver) ReceiveResponse(
	ctx context.Context,
	sender peer.ID,
	incoming message.DataTransferResponse) {

	select {
	case <-ctx.Done():
	case r.messageReceived <- receivedMessage{incoming, sender}:
	}
}

func (r *receiver) ReceiveError(err error) {
}

func TestDataTransferOneWay(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1
	host2 := gsData.Host2
	// setup receiving peer to just record message coming in
	dtnet2 := network.NewFromLibp2pHost(host2)
	r := &receiver{
		messageReceived: make(chan receivedMessage),
	}
	dtnet2.SetDelegate(r)

	gs := testutil.NewFakeGraphSync()
	tp := tp.NewTransport(host1.ID(), gs)
	dt := NewDataTransfer(host1, tp, gsData.StoredCounter1)
	dt.Start(ctx)

	t.Run("OpenPushDataTransfer", func(t *testing.T) {
		ssb := builder.NewSelectorSpecBuilder(basicnode.Style.Any)

		// this is the selector for "get the whole DAG"
		// TODO: support storage deals with custom payload selectors
		stor := ssb.ExploreRecursive(selector.RecursionLimitNone(),
			ssb.ExploreAll(ssb.ExploreRecursiveEdge())).Node()

		voucher := testutil.NewFakeDTType()
		baseCid := testutil.GenerateCids(1)[0]
		channelID, err := dt.OpenPushDataChannel(ctx, host2.ID(), voucher, baseCid, stor)
		require.NoError(t, err)
		require.NotNil(t, channelID)
		require.Equal(t, channelID.Initiator, host1.ID())
		require.NoError(t, err)

		var messageReceived receivedMessage
		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case messageReceived = <-r.messageReceived:
		}

		sender := messageReceived.sender
		require.Equal(t, sender, host1.ID())

		received := messageReceived.message
		require.True(t, received.IsRequest())
		receivedRequest, ok := received.(message.DataTransferRequest)
		require.True(t, ok)

		require.Equal(t, receivedRequest.TransferID(), channelID.ID)
		require.Equal(t, receivedRequest.BaseCid(), baseCid)
		require.False(t, receivedRequest.IsCancel())
		require.False(t, receivedRequest.IsPull())
		receivedSelector, err := receivedRequest.Selector()
		require.NoError(t, err)
		require.Equal(t, receivedSelector, stor)
		testutil.AssertFakeDTVoucher(t, receivedRequest, voucher)
	})

	t.Run("OpenPullDataTransfer", func(t *testing.T) {
		ssb := builder.NewSelectorSpecBuilder(basicnode.Style.Any)

		stor := ssb.ExploreRecursive(selector.RecursionLimitNone(),
			ssb.ExploreAll(ssb.ExploreRecursiveEdge())).Node()

		voucher := testutil.NewFakeDTType()
		baseCid := testutil.GenerateCids(1)[0]
		channelID, err := dt.OpenPullDataChannel(ctx, host2.ID(), voucher, baseCid, stor)
		require.NoError(t, err)
		require.NotNil(t, channelID)
		require.Equal(t, channelID.Initiator, host1.ID())
		require.NoError(t, err)
		gsRequest := gs.AssertRequestReceived(ctx, t)
		received := gsRequest.DTMessage(t)
		require.True(t, received.IsRequest())
		receivedRequest, ok := received.(message.DataTransferRequest)
		require.True(t, ok)

		require.Equal(t, receivedRequest.TransferID(), channelID.ID)
		require.Equal(t, receivedRequest.BaseCid(), baseCid)
		require.False(t, receivedRequest.IsCancel())
		require.True(t, receivedRequest.IsPull())
		receivedSelector, err := receivedRequest.Selector()
		require.NoError(t, err)
		require.Equal(t, receivedSelector, stor)
		testutil.AssertFakeDTVoucher(t, receivedRequest, voucher)
	})

	t.Run("SendVoucher", func(t *testing.T) {
		t.Run("with no channel open", func(t *testing.T) {
			voucher := testutil.NewFakeDTType()
			err := dt.SendVoucher(ctx, datatransfer.ChannelID{Initiator: host1.ID(), ID: 999999}, voucher)
			require.EqualError(t, err, channels.ErrNotFound.Error())
		})
		t.Run("with channel open", func(t *testing.T) {
			baseVoucher := testutil.NewFakeDTType()
			baseCid := testutil.GenerateCids(1)[0]

			t.Run("push succeeds", func(t *testing.T) {
				channelID, err := dt.OpenPushDataChannel(ctx, host2.ID(), baseVoucher, baseCid, gsData.AllSelector)
				require.NoError(t, err)
				require.NotNil(t, channelID)
				select {
				case <-ctx.Done():
					t.Fatal("did not receive message sent")
				case <-r.messageReceived:
				}
				voucher := testutil.NewFakeDTType()
				err = dt.SendVoucher(ctx, channelID, voucher)
				require.NoError(t, err)
				var messageReceived receivedMessage
				select {
				case <-ctx.Done():
					t.Fatal("did not receive message sent")
				case messageReceived = <-r.messageReceived:
				}
				received := messageReceived.message
				require.True(t, received.IsRequest())
				require.True(t, received.IsUpdate())
				receivedRequest, ok := received.(message.DataTransferRequest)
				require.True(t, ok)
				require.False(t, receivedRequest.IsCancel())
				testutil.AssertFakeDTVoucher(t, receivedRequest, voucher)
			})

			t.Run("pull succeeds", func(t *testing.T) {
				channelID, err := dt.OpenPullDataChannel(ctx, host2.ID(), baseVoucher, baseCid, gsData.AllSelector)
				require.NoError(t, err)
				gsRequest := gs.AssertRequestReceived(ctx, t)
				_ = gsRequest.DTMessage(t)
				voucher := testutil.NewFakeDTType()
				err = dt.SendVoucher(ctx, channelID, voucher)
				require.NoError(t, err)
				var messageReceived receivedMessage
				select {
				case <-ctx.Done():
					t.Fatal("did not receive message sent")
				case messageReceived = <-r.messageReceived:
				}
				received := messageReceived.message
				require.True(t, received.IsRequest())
				require.True(t, received.IsUpdate())
				receivedRequest, ok := received.(message.DataTransferRequest)
				require.True(t, ok)
				require.False(t, receivedRequest.IsCancel())
				testutil.AssertFakeDTVoucher(t, receivedRequest, voucher)
			})
		})
	})
}

type receivedValidation struct {
	isPull   bool
	other    peer.ID
	voucher  datatransfer.Voucher
	baseCid  cid.Cid
	selector ipld.Node
}

type fakeValidator struct {
	ctx                 context.Context
	validationsReceived chan receivedValidation
}

func (fv *fakeValidator) ValidatePush(
	sender peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.VoucherResult, error) {

	select {
	case <-fv.ctx.Done():
	case fv.validationsReceived <- receivedValidation{false, sender, voucher, baseCid, selector}:
	}
	return nil, nil
}

func (fv *fakeValidator) ValidatePull(
	receiver peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.VoucherResult, error) {

	select {
	case <-fv.ctx.Done():
	case fv.validationsReceived <- receivedValidation{true, receiver, voucher, baseCid, selector}:
	}
	return nil, nil
}

func TestDataTransferValidation(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1
	host2 := gsData.Host2
	dtnet1 := network.NewFromLibp2pHost(host1)
	r := &receiver{
		messageReceived: make(chan receivedMessage),
	}
	dtnet1.SetDelegate(r)

	gs2 := testutil.NewFakeGraphSync()
	tp2 := tp.NewTransport(host2.ID(), gs2)
	fv := &fakeValidator{ctx, make(chan receivedValidation)}

	id := datatransfer.TransferID(rand.Int31())
	dt2 := NewDataTransfer(host2, tp2, gsData.StoredCounter2)
	dt2.Start(ctx)

	err := dt2.RegisterVoucherType(&testutil.FakeDTType{}, fv)
	require.NoError(t, err)

	t.Run("ValidatePush", func(t *testing.T) {

		// create push request
		voucher, baseCid, request := createDTRequest(t, false, id, gsData.AllSelector)

		require.NoError(t, dtnet1.SendMessage(ctx, host2.ID(), request))

		var validation receivedValidation
		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case validation = <-fv.validationsReceived:
			assert.False(t, validation.isPull)
		}

		// verify response sent in graphsync
		gs2.AssertRequestReceived(ctx, t).DTMessage(t)

		assert.False(t, validation.isPull)
		assert.Equal(t, host1.ID(), validation.other)
		assert.Equal(t, &voucher, validation.voucher)
		assert.Equal(t, baseCid, validation.baseCid)
		assert.Equal(t, gsData.AllSelector, validation.selector)
	})

	t.Run("ValidatePull", func(t *testing.T) {

		// create pull request
		voucher, baseCid, request := createDTRequest(t, true, id, gsData.AllSelector)
		require.NoError(t, dtnet1.SendMessage(ctx, host2.ID(), request))

		var validation receivedValidation
		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case validation = <-fv.validationsReceived:
		}
		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case _ = <-r.messageReceived:
		}

		assert.True(t, validation.isPull)
		assert.Equal(t, validation.other, host1.ID())
		assert.Equal(t, &voucher, validation.voucher)
		assert.Equal(t, baseCid, validation.baseCid)
		assert.Equal(t, gsData.AllSelector, validation.selector)
	})
}

func createDTRequest(t *testing.T, isPull bool, id datatransfer.TransferID, selector ipld.Node) (testutil.FakeDTType, cid.Cid, message.DataTransferRequest) {
	voucher := &testutil.FakeDTType{Data: "applesauce"}
	baseCid := testutil.GenerateCids(1)[0]
	request, err := message.NewRequest(id, isPull, voucher.Type(), voucher, baseCid, selector)
	require.NoError(t, err)
	return *voucher, baseCid, request
}

type stubbedValidator struct {
	didPush    bool
	didPull    bool
	expectPush bool
	expectPull bool
	pushError  error
	pullError  error
}

func newSV() *stubbedValidator {
	return &stubbedValidator{false, false, false, false, nil, nil}
}

func (sv *stubbedValidator) ValidatePush(
	sender peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.VoucherResult, error) {
	sv.didPush = true
	return nil, sv.pushError
}

func (sv *stubbedValidator) ValidatePull(
	receiver peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.VoucherResult, error) {
	sv.didPull = true
	return nil, sv.pullError
}

func (sv *stubbedValidator) stubErrorPush() {
	sv.pushError = errors.New("something went wrong")
}

func (sv *stubbedValidator) stubSuccessPush() {
	sv.pullError = nil
}

func (sv *stubbedValidator) expectSuccessPush() {
	sv.expectPush = true
	sv.stubSuccessPush()
}

func (sv *stubbedValidator) expectErrorPush() {
	sv.expectPush = true
	sv.stubErrorPush()
}

func (sv *stubbedValidator) stubErrorPull() {
	sv.pullError = errors.New("something went wrong")
}

func (sv *stubbedValidator) stubSuccessPull() {
	sv.pullError = nil
}

func (sv *stubbedValidator) expectSuccessPull() {
	sv.expectPull = true
	sv.stubSuccessPull()
}

func (sv *stubbedValidator) expectErrorPull() {
	sv.expectPull = true
	sv.stubErrorPull()
}

func (sv *stubbedValidator) verifyExpectations(t *testing.T) {
	if sv.expectPush {
		require.True(t, sv.didPush)
	}
	if sv.expectPull {
		require.True(t, sv.didPull)
	}
}

func TestGraphsyncImpl_RegisterVoucherType(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1

	gs1 := testutil.NewFakeGraphSync()
	tp1 := tp.NewTransport(host1.ID(), gs1)
	dt := NewDataTransfer(host1, tp1, gsData.StoredCounter1)
	dt.Start(ctx)
	fv := &fakeValidator{ctx, make(chan receivedValidation)}

	// a voucher type can be registered
	assert.NoError(t, dt.RegisterVoucherType(&testutil.FakeDTType{}, fv))

	// it cannot be re-registered
	assert.EqualError(t, dt.RegisterVoucherType(&testutil.FakeDTType{}, fv), "error registering voucher type: identifier already registered: FakeDTType")

	// it must be registered as a pointer
	assert.EqualError(t, dt.RegisterVoucherType(testutil.FakeDTType{}, fv),
		"error registering voucher type: registering entry type FakeDTType: type must be a pointer")
}

func TestDataTransferSubscribing(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1
	host2 := gsData.Host2

	tp1 := gsData.SetupGSTransportHost1()
	tp2 := gsData.SetupGSTransportHost2()
	sv := newSV()
	sv.stubErrorPull()
	sv.stubErrorPush()
	dt2 := NewDataTransfer(host2, tp2, gsData.StoredCounter2)
	dt2.Start(ctx)
	require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))
	voucher := testutil.FakeDTType{Data: "applesauce"}
	baseCid := testutil.GenerateCids(1)[0]

	dt1 := NewDataTransfer(host1, tp1, gsData.StoredCounter1)
	dt1.Start(ctx)
	subscribe1Calls := make(chan struct{}, 1)
	subscribe1 := func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			subscribe1Calls <- struct{}{}
		}
	}
	subscribe2Calls := make(chan struct{}, 1)
	subscribe2 := func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			subscribe2Calls <- struct{}{}
		}
	}
	unsub1 := dt1.SubscribeToEvents(subscribe1)
	unsub2 := dt1.SubscribeToEvents(subscribe2)
	_, err := dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, baseCid, gsData.AllSelector)
	require.NoError(t, err)
	select {
	case <-ctx.Done():
		t.Fatal("subscribed events not received")
	case <-subscribe1Calls:
	}
	select {
	case <-ctx.Done():
		t.Fatal("subscribed events not received")
	case <-subscribe2Calls:
	}
	unsub1()
	unsub2()

	subscribe3Calls := make(chan struct{}, 1)
	subscribe3 := func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			subscribe3Calls <- struct{}{}
		}
	}
	subscribe4Calls := make(chan struct{}, 1)
	subscribe4 := func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			subscribe4Calls <- struct{}{}
		}
	}
	unsub3 := dt1.SubscribeToEvents(subscribe3)
	unsub4 := dt1.SubscribeToEvents(subscribe4)
	_, err = dt1.OpenPullDataChannel(ctx, host2.ID(), &voucher, baseCid, gsData.AllSelector)
	require.NoError(t, err)
	select {
	case <-ctx.Done():
		t.Fatal("subscribed events not received")
	case <-subscribe1Calls:
		t.Fatal("received channel that should have been unsubscribed")
	case <-subscribe2Calls:
		t.Fatal("received channel that should have been unsubscribed")
	case <-subscribe3Calls:
	}
	select {
	case <-ctx.Done():
		t.Fatal("subscribed events not received")
	case <-subscribe1Calls:
		t.Fatal("received channel that should have been unsubscribed")
	case <-subscribe2Calls:
		t.Fatal("received channel that should have been unsubscribed")
	case <-subscribe4Calls:
	}
	unsub3()
	unsub4()
}

func TestDataTransferInitiatingPushGraphsyncRequests(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1
	host2 := gsData.Host2

	gs2 := testutil.NewFakeGraphSync()
	tp2 := tp.NewTransport(host2.ID(), gs2)
	// setup receiving peer to just record message coming in
	dtnet1 := network.NewFromLibp2pHost(host1)
	r := &receiver{
		messageReceived: make(chan receivedMessage),
	}
	dtnet1.SetDelegate(r)

	id := datatransfer.TransferID(rand.Int31())

	_, baseCid, request := createDTRequest(t, false, id, gsData.AllSelector)

	t.Run("with successful validation", func(t *testing.T) {
		sv := newSV()
		sv.expectSuccessPush()

		dt2 := NewDataTransfer(host2, tp2, gsData.StoredCounter2)
		dt2.Start(ctx)

		require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))

		require.NoError(t, dtnet1.SendMessage(ctx, host2.ID(), request))
		requestReceived := gs2.AssertRequestReceived(ctx, t)

		sv.verifyExpectations(t)

		receiver := requestReceived.P
		require.Equal(t, receiver, host1.ID())

		cl, ok := requestReceived.Root.(cidlink.Link)
		require.True(t, ok)
		require.Equal(t, baseCid, cl.Cid)

		require.Equal(t, gsData.AllSelector, requestReceived.Selector)

	})

	t.Run("with error validation", func(t *testing.T) {
		sv := newSV()
		sv.expectErrorPush()

		dt2 := NewDataTransfer(host2, tp2, gsData.StoredCounter2)
		dt2.Start(ctx)

		require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))

		require.NoError(t, dtnet1.SendMessage(ctx, host2.ID(), request))
		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case <-r.messageReceived:
		}
		sv.verifyExpectations(t)

		// no graphsync request should be scheduled
		gs2.AssertNoRequestReceived(t)

	})

}

func TestDataTransferInitiatingPullGraphsyncRequests(t *testing.T) {
	ctx := context.Background()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1 // initiates the pull request
	host2 := gsData.Host2 // sends the data

	voucher := testutil.FakeDTType{Data: "applesauce"}
	baseCid := testutil.GenerateCids(1)[0]

	t.Run("does schedule graphsync request if it is a pull request", func(t *testing.T) {
		gs1Init := testutil.NewFakeGraphSync()
		tp1 := tp.NewTransport(host1.ID(), gs1Init)
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		dtInit := NewDataTransfer(host1, tp1, gsData.StoredCounter1)
		dtInit.Start(ctx)

		_, err := dtInit.OpenPullDataChannel(ctx, host2.ID(), &voucher, baseCid, gsData.AllSelector)
		require.NoError(t, err)
		requestReceived := gs1Init.AssertRequestReceived(ctx, t)

		receiver := requestReceived.P
		require.Equal(t, receiver, host2.ID())

		cl, ok := requestReceived.Root.(cidlink.Link)
		require.True(t, ok)
		require.Equal(t, baseCid.String(), cl.Cid.String())

		require.Equal(t, gsData.AllSelector, requestReceived.Selector)

	})

	t.Run("does not schedule graphsync request if is push request", func(t *testing.T) {
		gs1 := testutil.NewFakeGraphSync()
		tp1 := tp.NewTransport(host1.ID(), gs1)

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		dt1 := NewDataTransfer(host1, tp1, gsData.StoredCounter1)
		dt1.Start(ctx)

		_, err := dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, baseCid, gsData.AllSelector)
		require.NoError(t, err)

		// no graphsync request should be scheduled
		gs1.AssertNoRequestReceived(t)
	})
}

type receivedGraphSyncMessage struct {
	message gsmsg.GraphSyncMessage
	p       peer.ID
}

type fakeGraphSyncReceiver struct {
	receivedMessages chan receivedGraphSyncMessage
}

func (fgsr *fakeGraphSyncReceiver) ReceiveMessage(ctx context.Context, sender peer.ID, incoming gsmsg.GraphSyncMessage) {
	select {
	case <-ctx.Done():
	case fgsr.receivedMessages <- receivedGraphSyncMessage{incoming, sender}:
	}
}

func (fgsr *fakeGraphSyncReceiver) ReceiveError(_ error) {
}
func (fgsr *fakeGraphSyncReceiver) Connected(p peer.ID) {
}
func (fgsr *fakeGraphSyncReceiver) Disconnected(p peer.ID) {
}

func (fgsr *fakeGraphSyncReceiver) consumeResponses(ctx context.Context, t *testing.T) graphsync.ResponseStatusCode {
	var gsMessageReceived receivedGraphSyncMessage
	for {
		select {
		case <-ctx.Done():
			t.Fail()
		case gsMessageReceived = <-fgsr.receivedMessages:
			responses := gsMessageReceived.message.Responses()
			if (len(responses) > 0) && gsmsg.IsTerminalResponseCode(responses[0].Status()) {
				return responses[0].Status()
			}
		}
	}
}

func TestRespondingToPushGraphsyncRequests(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1 // initiator and data sender
	host2 := gsData.Host2 // data recipient, makes graphsync request for data
	voucher := testutil.NewFakeDTType()
	link := gsData.LoadUnixFSFile(t, false)

	// setup receiving peer to just record message coming in
	dtnet2 := network.NewFromLibp2pHost(host2)
	r := &receiver{
		messageReceived: make(chan receivedMessage),
	}
	dtnet2.SetDelegate(r)

	gsr := &fakeGraphSyncReceiver{
		receivedMessages: make(chan receivedGraphSyncMessage),
	}
	gsData.GsNet2.SetDelegate(gsr)

	tp1 := gsData.SetupGSTransportHost1()
	dt1 := NewDataTransfer(host1, tp1, gsData.StoredCounter1)
	dt1.Start(ctx)

	t.Run("when request is initiated", func(t *testing.T) {
		_, err := dt1.OpenPushDataChannel(ctx, host2.ID(), voucher, link.(cidlink.Link).Cid, gsData.AllSelector)
		require.NoError(t, err)

		var messageReceived receivedMessage
		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case messageReceived = <-r.messageReceived:
		}
		requestReceived := messageReceived.message.(message.DataTransferRequest)

		var buf bytes.Buffer
		response, err := message.NewResponse(requestReceived.TransferID(), true, false, false, voucher.Type(), voucher)
		require.NoError(t, err)
		err = response.ToNet(&buf)
		require.NoError(t, err)
		extData := buf.Bytes()

		request := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()), graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer,
			Data: extData,
		})
		gsmessage := gsmsg.New()
		gsmessage.AddRequest(request)
		require.NoError(t, gsData.GsNet2.SendMessage(ctx, host1.ID(), gsmessage))

		status := gsr.consumeResponses(ctx, t)
		require.False(t, gsmsg.IsTerminalFailureCode(status))
	})

	t.Run("when no request is initiated", func(t *testing.T) {
		var buf bytes.Buffer
		response, err := message.NewResponse(datatransfer.TransferID(rand.Uint64()), true, false, false, voucher.Type(), voucher)
		require.NoError(t, err)
		err = response.ToNet(&buf)
		require.NoError(t, err)
		extData := buf.Bytes()

		request := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()), graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer,
			Data: extData,
		})
		gsmessage := gsmsg.New()
		gsmessage.AddRequest(request)
		require.NoError(t, gsData.GsNet2.SendMessage(ctx, host1.ID(), gsmessage))

		status := gsr.consumeResponses(ctx, t)
		require.True(t, gsmsg.IsTerminalFailureCode(status))
	})
}

func TestResponseHookWhenExtensionNotFound(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1 // initiator and data sender
	host2 := gsData.Host2 // data recipient, makes graphsync request for data
	voucher := testutil.FakeDTType{Data: "applesauce"}
	link := gsData.LoadUnixFSFile(t, false)

	// setup receiving peer to just record message coming in
	dtnet2 := network.NewFromLibp2pHost(host2)
	r := &receiver{
		messageReceived: make(chan receivedMessage),
	}
	dtnet2.SetDelegate(r)

	gsr := &fakeGraphSyncReceiver{
		receivedMessages: make(chan receivedGraphSyncMessage),
	}
	gsData.GsNet2.SetDelegate(gsr)

	gs1 := gsData.SetupGraphsyncHost1()
	tp1 := tp.NewTransport(host1.ID(), gs1)
	dt1 := NewDataTransfer(host1, tp1, gsData.StoredCounter1)
	dt1.Start(ctx)

	t.Run("when it's not our extension, does not error and does not validate", func(t *testing.T) {
		//register a hook that validates the request so we don't fail in gs because the request
		//never gets processed
		validateHook := func(p peer.ID, req graphsync.RequestData, ha graphsync.IncomingRequestHookActions) {
			ha.ValidateRequest()
		}
		gs1.RegisterIncomingRequestHook(validateHook)

		_, err := dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, link.(cidlink.Link).Cid, gsData.AllSelector)
		require.NoError(t, err)

		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case <-r.messageReceived:
		}

		request := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()))
		gsmessage := gsmsg.New()
		gsmessage.AddRequest(request)
		require.NoError(t, gsData.GsNet2.SendMessage(ctx, host1.ID(), gsmessage))

		status := gsr.consumeResponses(ctx, t)
		assert.False(t, gsmsg.IsTerminalFailureCode(status))
	})
}

func TestRespondingToPullGraphsyncRequests(t *testing.T) {
	//create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host2 := gsData.Host2 // data sender

	// setup receiving peer to just record message coming in
	gsr := &fakeGraphSyncReceiver{
		receivedMessages: make(chan receivedGraphSyncMessage),
	}
	gsData.GsNet1.SetDelegate(gsr)

	tp2 := gsData.SetupGSTransportHost2()

	link := gsData.LoadUnixFSFile(t, true)

	id := datatransfer.TransferID(rand.Int31())

	t.Run("When a pull request is initiated and validated", func(t *testing.T) {
		sv := newSV()
		sv.expectSuccessPull()

		dt1 := NewDataTransfer(host2, tp2, gsData.StoredCounter2)
		dt1.Start(ctx)

		require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))

		_, _, request := createDTRequest(t, true, id, gsData.AllSelector)
		buf := new(bytes.Buffer)
		err := request.ToNet(buf)
		require.NoError(t, err)
		extData := buf.Bytes()

		gsRequest := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()), graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer,
			Data: extData,
		})

		// initiator requests data over graphsync network
		gsmessage := gsmsg.New()
		gsmessage.AddRequest(gsRequest)
		require.NoError(t, gsData.GsNet1.SendMessage(ctx, host2.ID(), gsmessage))
		status := gsr.consumeResponses(ctx, t)
		require.False(t, gsmsg.IsTerminalFailureCode(status))
	})

	t.Run("When request is initiated, but fails validation", func(t *testing.T) {
		sv := newSV()
		sv.expectErrorPull()
		dt1 := NewDataTransfer(host2, tp2, gsData.StoredCounter2)
		dt1.Start(ctx)

		require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))

		_, _, dtRequest := createDTRequest(t, true, id, gsData.AllSelector)

		buf := new(bytes.Buffer)
		err := dtRequest.ToNet(buf)
		require.NoError(t, err)
		extData := buf.Bytes()
		request := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()), graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer,
			Data: extData,
		})
		gsmessage := gsmsg.New()
		gsmessage.AddRequest(request)

		// non-initiator requests data over graphsync network, but should not get it
		// because there was no previous request
		require.NoError(t, gsData.GsNet1.SendMessage(ctx, host2.ID(), gsmessage))
		status := gsr.consumeResponses(ctx, t)
		require.True(t, gsmsg.IsTerminalFailureCode(status))
	})
}
