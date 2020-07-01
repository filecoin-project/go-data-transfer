package impl_test

import (
	"context"
	"testing"
	"time"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	. "github.com/filecoin-project/go-data-transfer/impl"
	"github.com/filecoin-project/go-data-transfer/testutil"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
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

	dt1 := NewDataTransfer(host1, tp1, gsData.StoredCounter1)
	dt2 := NewDataTransfer(host2, tp2, gsData.StoredCounter2)

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
	host2 := gsData.Host2

	root := gsData.LoadUnixFSFile(t, false)
	rootCid := root.(cidlink.Link).Cid
	tp1 := gsData.SetupGSTransportHost1()
	tp2 := gsData.SetupGSTransportHost2()

	dt1 := NewDataTransfer(host1, tp1, gsData.StoredCounter1)
	dt2 := NewDataTransfer(host2, tp2, gsData.StoredCounter2)

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

	_, err := dt2.OpenPullDataChannel(ctx, host1.ID(), &voucher, rootCid, gsData.AllSelector)
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
