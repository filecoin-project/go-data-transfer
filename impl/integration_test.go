package impl_test

import (
	"bytes"
	"context"
	"math/rand"
	"testing"
	"time"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	. "github.com/filecoin-project/go-data-transfer/impl"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/filecoin-project/go-data-transfer/network"
	"github.com/filecoin-project/go-data-transfer/testutil"
	tp "github.com/filecoin-project/go-data-transfer/transport/graphsync"
	"github.com/filecoin-project/go-data-transfer/transport/graphsync/extension"
	"github.com/ipfs/go-graphsync"
	gsmsg "github.com/ipfs/go-graphsync/message"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataTransferPushRoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1 // initiator, data sender
	host2 := gsData.Host2 // data recipient

	root := gsData.LoadUnixFSFile(t, false)
	rootCid := root.(cidlink.Link).Cid
	tp1 := gsData.SetupGSTransportHost1()
	tp2 := gsData.SetupGSTransportHost2()

	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.DtNet1, tp1, gsData.StoredCounter1)
	require.NoError(t, err)
	dt1.Start(ctx)

	dt2, err := NewDataTransfer(gsData.DtDs2, gsData.DtNet2, tp2, gsData.StoredCounter2)
	require.NoError(t, err)
	dt2.Start(ctx)

	finished := make(chan struct{}, 2)
	errChan := make(chan struct{}, 2)
	opened := make(chan struct{}, 2)
	sent := make(chan uint64, 21)
	received := make(chan uint64, 21)
	var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Progress {
			if channelState.Received() > 0 {
				received <- channelState.Received()
			} else if channelState.Sent() > 0 {
				sent <- channelState.Sent()
			}
		}
		if event.Code == datatransfer.Complete {
			finished <- struct{}{}
		}
		if event.Code == datatransfer.Error {
			errChan <- struct{}{}
		}
		if event.Code == datatransfer.Open {
			opened <- struct{}{}
		}
	}
	dt1.SubscribeToEvents(subscriber)
	dt2.SubscribeToEvents(subscriber)
	voucher := testutil.FakeDTType{Data: "applesauce"}
	sv := newSV()
	sv.expectSuccessPull()
	require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))

	chid, err := dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, rootCid, gsData.AllSelector)
	require.NoError(t, err)
	opens := 0
	completes := 0
	sentIncrements := make([]uint64, 0, 21)
	receivedIncrements := make([]uint64, 0, 21)
	for opens < 2 || completes < 2 || len(sentIncrements) < 21 || len(receivedIncrements) < 21 {
		select {
		case <-ctx.Done():
			t.Fatal("Did not complete succcessful data transfer")
		case <-finished:
			completes++
		case <-opened:
			opens++
		case sentIncrement := <-sent:
			sentIncrements = append(sentIncrements, sentIncrement)
		case receivedIncrement := <-received:
			receivedIncrements = append(receivedIncrements, receivedIncrement)
		case <-errChan:
			t.Fatal("received error on data transfer")
		}
	}
	require.Equal(t, sentIncrements, receivedIncrements)
	gsData.VerifyFileTransferred(t, root, true)
	assert.Equal(t, chid.Initiator, host1.ID())
}

func TestDataTransferPullRoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host1 := gsData.Host1

	root := gsData.LoadUnixFSFile(t, false)
	rootCid := root.(cidlink.Link).Cid
	tp1 := gsData.SetupGSTransportHost1()
	tp2 := gsData.SetupGSTransportHost2()

	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.DtNet1, tp1, gsData.StoredCounter1)
	require.NoError(t, err)
	dt1.Start(ctx)

	dt2, err := NewDataTransfer(gsData.DtDs2, gsData.DtNet2, tp2, gsData.StoredCounter2)
	require.NoError(t, err)
	dt2.Start(ctx)

	finished := make(chan struct{}, 2)
	errChan := make(chan struct{}, 2)
	opened := make(chan struct{}, 2)
	sent := make(chan uint64, 21)
	received := make(chan uint64, 21)
	var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Progress {
			if channelState.Received() > 0 {
				received <- channelState.Received()
			} else if channelState.Sent() > 0 {
				sent <- channelState.Sent()
			}
		}
		if event.Code == datatransfer.Complete {
			finished <- struct{}{}
		}
		if event.Code == datatransfer.Error {
			errChan <- struct{}{}
		}
		if event.Code == datatransfer.Open {
			opened <- struct{}{}
		}
	}
	dt1.SubscribeToEvents(subscriber)
	dt2.SubscribeToEvents(subscriber)
	voucher := testutil.FakeDTType{Data: "applesauce"}
	sv := newSV()
	sv.expectSuccessPull()
	require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))

	_, err = dt2.OpenPullDataChannel(ctx, host1.ID(), &voucher, rootCid, gsData.AllSelector)
	require.NoError(t, err)
	opens := 0
	completes := 0
	sentIncrements := make([]uint64, 0, 21)
	receivedIncrements := make([]uint64, 0, 21)
	for opens < 2 || completes < 2 || len(sentIncrements) < 21 || len(receivedIncrements) < 21 {
		select {
		case <-ctx.Done():
			t.Fatal("Did not complete succcessful data transfer")
		case <-finished:
			completes++
		case <-opened:
			opens++
		case sentIncrement := <-sent:
			sentIncrements = append(sentIncrements, sentIncrement)
		case receivedIncrement := <-received:
			receivedIncrements = append(receivedIncrements, receivedIncrement)
		case <-errChan:
			t.Fatal("received error on data transfer")
		}
	}
	require.Equal(t, sentIncrements, receivedIncrements)
	gsData.VerifyFileTransferred(t, root, true)
}

func TestDataTransferSubscribing(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t)
	host2 := gsData.Host2

	tp1 := gsData.SetupGSTransportHost1()
	tp2 := gsData.SetupGSTransportHost2()
	sv := newSV()
	sv.stubErrorPull()
	sv.stubErrorPush()
	dt2, err := NewDataTransfer(gsData.DtDs2, gsData.DtNet2, tp2, gsData.StoredCounter2)
	require.NoError(t, err)
	dt2.Start(ctx)
	require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))
	voucher := testutil.FakeDTType{Data: "applesauce"}
	baseCid := testutil.GenerateCids(1)[0]

	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.DtNet1, tp1, gsData.StoredCounter1)
	require.NoError(t, err)
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
	_, err = dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, baseCid, gsData.AllSelector)
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
	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.DtNet1, tp1, gsData.StoredCounter1)
	require.NoError(t, err)
	dt1.Start(ctx)
	voucherResult := testutil.NewFakeDTType()
	err = dt1.RegisterVoucherResultType(voucherResult)
	require.NoError(t, err)

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
		response, err := message.NewResponse(requestReceived.TransferID(), true, false, false, voucherResult.Type(), voucherResult)
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
	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.DtNet1, tp1, gsData.StoredCounter1)
	require.NoError(t, err)
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

		dt1, err := NewDataTransfer(gsData.DtDs2, gsData.DtNet2, tp2, gsData.StoredCounter2)
		require.NoError(t, err)
		dt1.Start(ctx)

		require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))

		voucher := testutil.NewFakeDTType()
		request, err := message.NewRequest(id, true, voucher.Type(), voucher, testutil.GenerateCids(1)[0], gsData.AllSelector)
		require.NoError(t, err)
		buf := new(bytes.Buffer)
		err = request.ToNet(buf)
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
		dt1, err := NewDataTransfer(gsData.DtDs2, gsData.DtNet2, tp2, gsData.StoredCounter2)
		require.NoError(t, err)
		dt1.Start(ctx)

		require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))
		voucher := testutil.NewFakeDTType()
		dtRequest, err := message.NewRequest(id, true, voucher.Type(), voucher, testutil.GenerateCids(1)[0], gsData.AllSelector)
		require.NoError(t, err)

		buf := new(bytes.Buffer)
		err = dtRequest.ToNet(buf)
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
