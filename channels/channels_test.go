package channels_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ipfs/go-datastore"
	dss "github.com/ipfs/go-datastore/sync"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
)

func TestChannels(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ds := dss.MutexWrap(datastore.NewMapDatastore())
	received := make(chan event)
	notifier := func(evt datatransfer.Event, chst datatransfer.ChannelState) {
		received <- event{evt, chst}
	}

	tid1 := datatransfer.TransferID(0)
	tid2 := datatransfer.TransferID(1)
	fv1 := testutil.NewTestTypedVoucher()
	fv2 := testutil.NewTestTypedVoucher()
	cids := testutil.GenerateCids(4)
	selector := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any).Matcher().Node()
	peers := testutil.GeneratePeers(4)

	channelList, err := channels.New(ds, notifier, &fakeEnv{}, peers[0])
	require.NoError(t, err)

	err = channelList.Start(ctx)
	require.NoError(t, err)
	t.Run("adding channels", func(t *testing.T) {
		chid, err := channelList.CreateNew(peers[0], tid1, cids[0], selector, fv1, peers[0], peers[0], peers[1])
		require.NoError(t, err)
		require.Equal(t, peers[0], chid.Initiator)
		require.Equal(t, tid1, chid.ID)

		// cannot add twice for same channel id
		_, err = channelList.CreateNew(peers[0], tid1, cids[1], selector, fv2, peers[0], peers[1], peers[0])
		require.Error(t, err)
		state := checkEvent(ctx, t, received, datatransfer.Open)
		require.Equal(t, datatransfer.Requested, state.Status())

		// can add for different id
		chid, err = channelList.CreateNew(peers[2], tid2, cids[1], selector, fv2, peers[3], peers[2], peers[3])
		require.NoError(t, err)
		require.Equal(t, peers[3], chid.Initiator)
		require.Equal(t, tid2, chid.ID)
		state = checkEvent(ctx, t, received, datatransfer.Open)
		require.Equal(t, datatransfer.Requested, state.Status())
		require.Equal(t, peers[2], state.SelfPeer())
		require.Equal(t, peers[3], state.OtherPeer())
	})

	t.Run("in progress channels", func(t *testing.T) {
		inProgress, err := channelList.InProgress()
		require.NoError(t, err)
		require.Len(t, inProgress, 2)
		require.Contains(t, inProgress, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.Contains(t, inProgress, datatransfer.ChannelID{Initiator: peers[3], Responder: peers[2], ID: tid2})
	})

	t.Run("get by id", func(t *testing.T) {
		state, err := channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		require.NotEqual(t, channels.EmptyChannelState, state)
		require.Equal(t, cids[0], state.BaseCID())
		require.Equal(t, selector, state.Selector())
		voucher, err := state.Voucher()
		require.NoError(t, err)
		require.True(t, fv1.Equals(voucher))
		require.Equal(t, peers[0], state.Sender())
		require.Equal(t, peers[1], state.Recipient())

		// empty if channel does not exist
		state, err = channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[1], Responder: peers[1], ID: tid1})
		require.Equal(t, nil, state)
		require.True(t, xerrors.As(err, new(*channels.ErrNotFound)))

		// works for other channel as well
		state, err = channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[3], Responder: peers[2], ID: tid2})
		require.NotEqual(t, nil, state)
		require.NoError(t, err)
		require.Equal(t, peers[2], state.SelfPeer())
	})

	t.Run("accept", func(t *testing.T) {
		state, err := channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		require.Equal(t, state.Status(), datatransfer.Requested)

		err = channelList.Accept(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.Accept)
		require.Equal(t, state.Status(), datatransfer.Ongoing)

		err = channelList.Accept(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1})
		require.True(t, xerrors.As(err, new(*channels.ErrNotFound)))
	})

	t.Run("transfer queued", func(t *testing.T) {
		state, err := channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		require.Equal(t, state.Status(), datatransfer.Ongoing)

		err = channelList.TransferRequestQueued(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.TransferRequestQueued)
		require.Equal(t, state.Status(), datatransfer.Ongoing)
	})

	t.Run("datasent/queued when transfer is already finished", func(t *testing.T) {
		ds := dss.MutexWrap(datastore.NewMapDatastore())

		channelList, err := channels.New(ds, notifier, &fakeEnv{}, peers[0])
		require.NoError(t, err)
		err = channelList.Start(ctx)
		require.NoError(t, err)

		chid, err := channelList.CreateNew(peers[0], tid1, cids[0], selector, fv1, peers[0], peers[0], peers[1])
		require.NoError(t, err)
		checkEvent(ctx, t, received, datatransfer.Open)
		require.NoError(t, channelList.Accept(chid))
		checkEvent(ctx, t, received, datatransfer.Accept)

		// move the channel to `TransferFinished` state.
		require.NoError(t, channelList.FinishTransfer(chid))
		state := checkEvent(ctx, t, received, datatransfer.FinishTransfer)
		require.Equal(t, datatransfer.TransferFinished, state.Status())

		// send a data-sent event and ensure it's a no-op
		err = channelList.DataSent(chid, cids[1], 1, 1, true)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.DataSent)
		require.Equal(t, datatransfer.TransferFinished, state.Status())

		// send a data-queued event and ensure it's a no-op.
		err = channelList.DataQueued(chid, cids[1], 1, 1, true)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.DataQueued)
		require.Equal(t, datatransfer.TransferFinished, state.Status())
	})

	t.Run("updating send/receive values", func(t *testing.T) {
		ds := dss.MutexWrap(datastore.NewMapDatastore())

		channelList, err := channels.New(ds, notifier, &fakeEnv{}, peers[0])
		require.NoError(t, err)
		err = channelList.Start(ctx)
		require.NoError(t, err)

		_, err = channelList.CreateNew(peers[0], tid1, cids[0], selector, fv1, peers[0], peers[0], peers[1])
		require.NoError(t, err)
		state := checkEvent(ctx, t, received, datatransfer.Open)
		require.Equal(t, datatransfer.Requested, state.Status())
		require.Equal(t, uint64(0), state.Received())
		require.Equal(t, uint64(0), state.Sent())

		err = channelList.DataReceived(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, cids[0], 50, 1, true)
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.DataReceivedProgress)
		state = checkEvent(ctx, t, received, datatransfer.DataReceived)
		require.Equal(t, uint64(50), state.Received())
		require.Equal(t, uint64(0), state.Sent())

		err = channelList.DataSent(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, cids[1], 100, 1, true)
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.DataSentProgress)
		state = checkEvent(ctx, t, received, datatransfer.DataSent)
		require.Equal(t, uint64(50), state.Received())
		require.Equal(t, uint64(100), state.Sent())

		// send block again has no effect
		err = channelList.DataSent(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, cids[1], 100, 1, true)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.DataSent)
		require.Equal(t, uint64(50), state.Received())
		require.Equal(t, uint64(100), state.Sent())

		// errors if channel does not exist
		err = channelList.DataReceived(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, cids[1], 200, 2, true)
		require.True(t, xerrors.As(err, new(*channels.ErrNotFound)))
		err = channelList.DataSent(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, cids[1], 200, 2, true)
		require.True(t, xerrors.As(err, new(*channels.ErrNotFound)))

		err = channelList.DataReceived(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, cids[1], 50, 2, true)
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.DataReceivedProgress)
		state = checkEvent(ctx, t, received, datatransfer.DataReceived)
		require.Equal(t, uint64(100), state.Received())
		require.Equal(t, uint64(100), state.Sent())

		err = channelList.DataSent(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, cids[1], 25, 2, false)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.DataSent)
		require.Equal(t, uint64(100), state.Received())
		require.Equal(t, uint64(100), state.Sent())

		err = channelList.DataReceived(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, cids[0], 50, 3, false)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.DataReceived)
		require.Equal(t, uint64(100), state.Received())
		require.Equal(t, uint64(100), state.Sent())
	})

	t.Run("data limit", func(t *testing.T) {
		ds := dss.MutexWrap(datastore.NewMapDatastore())

		channelList, err := channels.New(ds, notifier, &fakeEnv{}, peers[0])
		require.NoError(t, err)
		err = channelList.Start(ctx)
		require.NoError(t, err)

		_, err = channelList.CreateNew(peers[0], tid1, cids[0], selector, fv1, peers[1], peers[0], peers[1])
		require.NoError(t, err)
		state := checkEvent(ctx, t, received, datatransfer.Open)

		err = channelList.DataQueued(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, cids[0], 300, 1, true)
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.DataQueuedProgress)
		state = checkEvent(ctx, t, received, datatransfer.DataQueued)
		require.Equal(t, uint64(300), state.Queued())

		err = channelList.SetDataLimit(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, 400)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.SetDataLimit)
		require.Equal(t, state.DataLimit(), uint64(400))

		// send block again has no effect
		err = channelList.DataQueued(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, cids[0], 300, 1, true)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.DataQueued)
		require.Equal(t, uint64(300), state.Queued())

		err = channelList.DataQueued(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, cids[1], 200, 2, true)
		require.EqualError(t, err, datatransfer.ErrPause.Error())
		_ = checkEvent(ctx, t, received, datatransfer.DataQueuedProgress)
		_ = checkEvent(ctx, t, received, datatransfer.DataQueued)
		state = checkEvent(ctx, t, received, datatransfer.DataLimitExceeded)
		require.Equal(t, uint64(500), state.Queued())

		err = channelList.SetDataLimit(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, 700)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.SetDataLimit)
		require.Equal(t, state.DataLimit(), uint64(700))

		err = channelList.DataQueued(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, cids[2], 150, 3, true)
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.DataQueuedProgress)
		state = checkEvent(ctx, t, received, datatransfer.DataQueued)
		require.Equal(t, uint64(650), state.Queued())

		err = channelList.DataQueued(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, cids[3], 200, 4, true)
		require.EqualError(t, err, datatransfer.ErrPause.Error())
		_ = checkEvent(ctx, t, received, datatransfer.DataQueuedProgress)
		_ = checkEvent(ctx, t, received, datatransfer.DataQueued)
		state = checkEvent(ctx, t, received, datatransfer.DataLimitExceeded)
		require.Equal(t, uint64(850), state.Queued())
	})

	t.Run("pause/resume", func(t *testing.T) {
		state, err := channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		require.Equal(t, datatransfer.Ongoing, state.Status())

		err = channelList.PauseInitiator(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.PauseInitiator)
		require.Equal(t, datatransfer.InitiatorPaused, state.Status())

		err = channelList.PauseResponder(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.PauseResponder)
		require.Equal(t, datatransfer.BothPaused, state.Status())

		err = channelList.ResumeInitiator(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.ResumeInitiator)
		require.Equal(t, datatransfer.ResponderPaused, state.Status())

		err = channelList.ResumeResponder(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.ResumeResponder)
		require.Equal(t, datatransfer.Ongoing, state.Status())
	})

	t.Run("new vouchers & voucherResults", func(t *testing.T) {
		fv3 := testutil.NewTestTypedVoucher()
		fvr1 := testutil.NewTestTypedVoucher()

		state, err := channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		vouchers, err := state.Vouchers()
		require.NoError(t, err)
		require.Len(t, vouchers, 1)
		require.True(t, fv1.Equals(vouchers[0]))
		voucher, err := state.Voucher()
		require.NoError(t, err)
		require.True(t, fv1.Equals(voucher))
		voucher, err = state.LastVoucher()
		require.NoError(t, err)
		require.True(t, fv1.Equals(voucher))

		err = channelList.NewVoucher(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, fv3)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.NewVoucher)
		vouchers, err = state.Vouchers()
		require.NoError(t, err)
		require.Len(t, vouchers, 2)
		require.True(t, fv1.Equals(vouchers[0]))
		require.True(t, fv3.Equals(vouchers[1]))
		voucher, err = state.Voucher()
		require.NoError(t, err)
		require.True(t, fv1.Equals(voucher))
		voucher, err = state.LastVoucher()
		require.NoError(t, err)
		require.True(t, fv3.Equals(voucher))

		state, err = channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		results, err := state.VoucherResults()
		require.NoError(t, err)
		require.Equal(t, []datatransfer.TypedVoucher{}, results)

		err = channelList.NewVoucherResult(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, fvr1)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.NewVoucherResult)
		voucherResults, err := state.VoucherResults()
		require.NoError(t, err)
		require.Len(t, voucherResults, 1)
		require.True(t, fvr1.Equals(voucherResults[0]))
		voucherResult, err := state.LastVoucherResult()
		require.NoError(t, err)
		require.True(t, fvr1.Equals(voucherResult))
	})

	t.Run("test finality", func(t *testing.T) {
		state, err := channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		require.Equal(t, datatransfer.Ongoing, state.Status())

		err = channelList.Complete(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.Complete)
		require.Equal(t, datatransfer.Completing, state.Status())
		state = checkEvent(ctx, t, received, datatransfer.CleanupComplete)
		require.Equal(t, datatransfer.Completed, state.Status())

		state, err = channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[3], Responder: peers[2], ID: tid2})
		require.NoError(t, err)
		require.Equal(t, datatransfer.Requested, state.Status())

		err = channelList.Error(datatransfer.ChannelID{Initiator: peers[3], Responder: peers[2], ID: tid2}, errors.New("something went wrong"))
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.Error)
		require.Equal(t, datatransfer.Failing, state.Status())
		require.Equal(t, "something went wrong", state.Message())
		state = checkEvent(ctx, t, received, datatransfer.CleanupComplete)
		require.Equal(t, datatransfer.Failed, state.Status())

		chid, err := channelList.CreateNew(peers[0], tid2, cids[1], selector, fv2, peers[2], peers[1], peers[2])
		require.NoError(t, err)
		require.Equal(t, peers[2], chid.Initiator)
		require.Equal(t, tid2, chid.ID)
		state = checkEvent(ctx, t, received, datatransfer.Open)
		require.Equal(t, datatransfer.Requested, state.Status())

		err = channelList.Cancel(datatransfer.ChannelID{Initiator: peers[2], Responder: peers[1], ID: tid2})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.Cancel)
		require.Equal(t, datatransfer.Cancelling, state.Status())
		state = checkEvent(ctx, t, received, datatransfer.CleanupComplete)
		require.Equal(t, datatransfer.Cancelled, state.Status())
	})

	t.Run("test self peer and other peer", func(t *testing.T) {
		// sender is self peer
		chid, err := channelList.CreateNew(peers[1], tid1, cids[0], selector, fv1, peers[1], peers[1], peers[2])
		require.NoError(t, err)
		ch, err := channelList.GetByID(context.Background(), chid)
		require.NoError(t, err)
		require.Equal(t, peers[1], ch.SelfPeer())
		require.Equal(t, peers[2], ch.OtherPeer())

		// recipient is self peer
		chid, err = channelList.CreateNew(peers[2], datatransfer.TransferID(1001), cids[0], selector, fv1, peers[1], peers[2], peers[1])
		require.NoError(t, err)
		ch, err = channelList.GetByID(context.Background(), chid)
		require.NoError(t, err)
		require.Equal(t, peers[2], ch.SelfPeer())
		require.Equal(t, peers[1], ch.OtherPeer())
	})

	t.Run("test disconnected", func(t *testing.T) {
		ds := dss.MutexWrap(datastore.NewMapDatastore())
		received := make(chan event)
		notifier := func(evt datatransfer.Event, chst datatransfer.ChannelState) {
			received <- event{evt, chst}
		}
		channelList, err := channels.New(ds, notifier, &fakeEnv{}, peers[0])
		require.NoError(t, err)
		err = channelList.Start(ctx)
		require.NoError(t, err)

		chid, err := channelList.CreateNew(peers[3], tid1, cids[0], selector, fv1, peers[3], peers[0], peers[3])
		require.NoError(t, err)
		state := checkEvent(ctx, t, received, datatransfer.Open)
		require.Equal(t, datatransfer.Requested, state.Status())

		disconnectErr := xerrors.Errorf("disconnected")
		err = channelList.Disconnected(chid, disconnectErr)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.Disconnected)
		require.Equal(t, disconnectErr.Error(), state.Message())
	})

	t.Run("test self peer and other peer", func(t *testing.T) {
		peers := testutil.GeneratePeers(3)
		// sender is self peer
		chid, err := channelList.CreateNew(peers[1], tid1, cids[0], selector, fv1, peers[1], peers[1], peers[2])
		require.NoError(t, err)
		ch, err := channelList.GetByID(context.Background(), chid)
		require.NoError(t, err)
		require.Equal(t, peers[1], ch.SelfPeer())
		require.Equal(t, peers[2], ch.OtherPeer())

		// recipient is self peer
		chid, err = channelList.CreateNew(peers[2], datatransfer.TransferID(1001), cids[0], selector, fv1, peers[1], peers[2], peers[1])
		require.NoError(t, err)
		ch, err = channelList.GetByID(context.Background(), chid)
		require.NoError(t, err)
		require.Equal(t, peers[2], ch.SelfPeer())
		require.Equal(t, peers[1], ch.OtherPeer())
	})
}

func TestIsChannelTerminated(t *testing.T) {
	require.True(t, channels.IsChannelTerminated(datatransfer.Cancelled))
	require.True(t, channels.IsChannelTerminated(datatransfer.Failed))
	require.False(t, channels.IsChannelTerminated(datatransfer.Ongoing))
}

func TestIsChannelCleaningUp(t *testing.T) {
	require.True(t, channels.IsChannelCleaningUp(datatransfer.Cancelling))
	require.True(t, channels.IsChannelCleaningUp(datatransfer.Failing))
	require.True(t, channels.IsChannelCleaningUp(datatransfer.Completing))
	require.False(t, channels.IsChannelCleaningUp(datatransfer.Cancelled))
}

type event struct {
	event datatransfer.Event
	state datatransfer.ChannelState
}

func checkEvent(ctx context.Context, t *testing.T, received chan event, code datatransfer.EventCode) datatransfer.ChannelState {
	var evt event
	select {
	case evt = <-received:
	case <-ctx.Done():
		t.Fatal("did not receive event")
	}
	require.Equal(t, code, evt.event.Code)
	return evt.state
}

type fakeEnv struct {
}

func (fe *fakeEnv) Protect(id peer.ID, tag string) {
}

func (fe *fakeEnv) Unprotect(id peer.ID, tag string) bool {
	return false
}

func (fe *fakeEnv) ID() peer.ID {
	return peer.ID("")
}

func (fe *fakeEnv) CleanupChannel(chid datatransfer.ChannelID) {
}
