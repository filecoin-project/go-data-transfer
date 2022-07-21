package channels_test

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dss "github.com/ipfs/go-datastore/sync"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	versioning "github.com/filecoin-project/go-ds-versioning/pkg"
	versionedds "github.com/filecoin-project/go-ds-versioning/pkg/datastore"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels"
	"github.com/filecoin-project/go-data-transfer/v2/channels/internal"
	"github.com/filecoin-project/go-data-transfer/v2/channels/internal/migrations"
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
		chid, _, err := channelList.CreateNew(peers[0], tid1, cids[0], selector, fv1, peers[0], peers[0], peers[1])
		require.NoError(t, err)
		require.Equal(t, peers[0], chid.Initiator)
		require.Equal(t, tid1, chid.ID)

		// cannot add twice for same channel id
		_, _, err = channelList.CreateNew(peers[0], tid1, cids[1], selector, fv2, peers[0], peers[1], peers[0])
		require.Error(t, err)
		state := checkEvent(ctx, t, received, datatransfer.Open)
		require.Equal(t, datatransfer.Requested, state.Status())

		// can add for different id
		chid, _, err = channelList.CreateNew(peers[2], tid2, cids[1], selector, fv2, peers[3], peers[2], peers[3])
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
		voucher := state.Voucher()
		require.True(t, fv1.Equals(voucher))
		require.Equal(t, peers[0], state.Sender())
		require.Equal(t, peers[1], state.Recipient())

		// empty if channel does not exist
		state, err = channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[1], Responder: peers[1], ID: tid1})
		require.Equal(t, nil, state)
		require.True(t, errors.Is(err, datatransfer.ErrChannelNotFound))

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
		require.Equal(t, state.Status(), datatransfer.Queued)

		err = channelList.Accept(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1})
		require.True(t, errors.Is(err, datatransfer.ErrChannelNotFound))
	})

	t.Run("transfer initiated", func(t *testing.T) {
		state, err := channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		require.Equal(t, state.Status(), datatransfer.Queued)

		err = channelList.TransferInitiated(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.TransferInitiated)
		require.Equal(t, state.Status(), datatransfer.Ongoing)
	})

	t.Run("updating send/receive values", func(t *testing.T) {
		ds := dss.MutexWrap(datastore.NewMapDatastore())

		channelList, err := channels.New(ds, notifier, &fakeEnv{}, peers[0])
		require.NoError(t, err)
		err = channelList.Start(ctx)
		require.NoError(t, err)

		_, _, err = channelList.CreateNew(peers[0], tid1, cids[0], selector, fv1, peers[0], peers[0], peers[1])
		require.NoError(t, err)
		state := checkEvent(ctx, t, received, datatransfer.Open)
		require.Equal(t, datatransfer.Requested, state.Status())
		require.Equal(t, uint64(0), state.Received())
		require.Equal(t, uint64(0), state.Sent())

		err = channelList.TransferInitiated(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.TransferInitiated)

		err = channelList.DataReceived(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, 50, basicnode.NewInt(1))
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.DataReceivedProgress)
		state = checkEvent(ctx, t, received, datatransfer.DataReceived)
		require.Equal(t, uint64(50), state.Received())
		require.Equal(t, uint64(0), state.Sent())

		err = channelList.DataSent(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, 100, basicnode.NewInt(1))
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.DataSentProgress)
		state = checkEvent(ctx, t, received, datatransfer.DataSent)
		require.Equal(t, uint64(50), state.Received())
		require.Equal(t, uint64(100), state.Sent())

		// errors if channel does not exist
		err = channelList.DataReceived(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, 200, basicnode.NewInt(2))
		require.True(t, errors.Is(err, datatransfer.ErrChannelNotFound))
		err = channelList.DataSent(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, 200, basicnode.NewInt(2))
		require.True(t, errors.Is(err, datatransfer.ErrChannelNotFound))

		err = channelList.DataReceived(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, 50, basicnode.NewInt(2))
		require.NoError(t, err)
		_ = checkEvent(ctx, t, received, datatransfer.DataReceivedProgress)
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

		_, _, err = channelList.CreateNew(peers[0], tid1, cids[0], selector, fv1, peers[1], peers[0], peers[1])
		require.NoError(t, err)
		state := checkEvent(ctx, t, received, datatransfer.Open)

		err = channelList.SetDataLimit(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, 400)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.SetDataLimit)
		require.Equal(t, state.DataLimit(), uint64(400))

		err = channelList.DataLimitExceeded(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.DataLimitExceeded)
		require.True(t, state.ResponderPaused())

		err = channelList.SetDataLimit(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1}, 700)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.SetDataLimit)
		require.Equal(t, state.DataLimit(), uint64(700))

		err = channelList.ResumeResponder(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1})
		state = checkEvent(ctx, t, received, datatransfer.ResumeResponder)
		require.False(t, state.ResponderPaused())

		err = channelList.PauseInitiator(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1})
		state = checkEvent(ctx, t, received, datatransfer.PauseInitiator)
		require.True(t, state.InitiatorPaused())

		err = channelList.DataLimitExceeded(datatransfer.ChannelID{Initiator: peers[1], Responder: peers[0], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.DataLimitExceeded)
		require.True(t, state.BothPaused())

	})

	t.Run("pause/resume", func(t *testing.T) {
		state, err := channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		require.Equal(t, datatransfer.Ongoing, state.Status())

		err = channelList.PauseInitiator(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.PauseInitiator)
		require.True(t, state.InitiatorPaused())
		require.False(t, state.BothPaused())

		err = channelList.PauseResponder(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.PauseResponder)
		require.True(t, state.BothPaused())

		err = channelList.ResumeInitiator(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.ResumeInitiator)
		require.True(t, state.ResponderPaused())
		require.False(t, state.BothPaused())

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
		vouchers := state.Vouchers()
		require.Len(t, vouchers, 1)
		require.True(t, fv1.Equals(vouchers[0]))
		voucher := state.Voucher()
		require.True(t, fv1.Equals(voucher))
		voucher = state.LastVoucher()
		require.True(t, fv1.Equals(voucher))

		err = channelList.NewVoucher(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, fv3)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.NewVoucher)
		vouchers = state.Vouchers()
		require.Len(t, vouchers, 2)
		require.True(t, fv1.Equals(vouchers[0]))
		require.True(t, fv3.Equals(vouchers[1]))
		voucher = state.Voucher()
		require.True(t, fv1.Equals(voucher))
		voucher = state.LastVoucher()
		require.True(t, fv3.Equals(voucher))

		state, err = channelList.GetByID(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1})
		require.NoError(t, err)
		results := state.VoucherResults()
		require.Equal(t, []datatransfer.TypedVoucher{}, results)

		err = channelList.NewVoucherResult(datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}, fvr1)
		require.NoError(t, err)
		state = checkEvent(ctx, t, received, datatransfer.NewVoucherResult)
		voucherResults := state.VoucherResults()
		require.Len(t, voucherResults, 1)
		require.True(t, fvr1.Equals(voucherResults[0]))
		voucherResult := state.LastVoucherResult()
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

		chid, _, err := channelList.CreateNew(peers[0], tid2, cids[1], selector, fv2, peers[2], peers[1], peers[2])
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
		chid, _, err := channelList.CreateNew(peers[1], tid1, cids[0], selector, fv1, peers[1], peers[1], peers[2])
		require.NoError(t, err)
		ch, err := channelList.GetByID(context.Background(), chid)
		require.NoError(t, err)
		require.Equal(t, peers[1], ch.SelfPeer())
		require.Equal(t, peers[2], ch.OtherPeer())

		// recipient is self peer
		chid, _, err = channelList.CreateNew(peers[2], datatransfer.TransferID(1001), cids[0], selector, fv1, peers[1], peers[2], peers[1])
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

		chid, _, err := channelList.CreateNew(peers[3], tid1, cids[0], selector, fv1, peers[3], peers[0], peers[3])
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
		chid, _, err := channelList.CreateNew(peers[1], tid1, cids[0], selector, fv1, peers[1], peers[1], peers[2])
		require.NoError(t, err)
		ch, err := channelList.GetByID(context.Background(), chid)
		require.NoError(t, err)
		require.Equal(t, peers[1], ch.SelfPeer())
		require.Equal(t, peers[2], ch.OtherPeer())

		// recipient is self peer
		chid, _, err = channelList.CreateNew(peers[2], datatransfer.TransferID(1001), cids[0], selector, fv1, peers[1], peers[2], peers[1])
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

func TestMigrations(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	ds := dss.MutexWrap(datastore.NewMapDatastore())
	received := make(chan event)
	notifier := func(evt datatransfer.Event, chst datatransfer.ChannelState) {
		received <- event{evt, chst}
	}
	numChannels := 5
	transferIDs := make([]datatransfer.TransferID, numChannels)
	initiators := make([]peer.ID, numChannels)
	responders := make([]peer.ID, numChannels)
	baseCids := make([]cid.Cid, numChannels)

	totalSizes := make([]uint64, numChannels)
	sents := make([]uint64, numChannels)
	receiveds := make([]uint64, numChannels)

	messages := make([]string, numChannels)
	vouchers := make([]datatransfer.TypedVoucher, numChannels)
	voucherResults := make([]datatransfer.TypedVoucher, numChannels)
	sentIndex := make([]int64, numChannels)
	receivedIndex := make([]int64, numChannels)
	queuedIndex := make([]int64, numChannels)
	allSelector := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any).Matcher().Node()
	selfPeer := testutil.GeneratePeers(1)[0]

	list, err := migrations.GetChannelStateMigrations(selfPeer)
	require.NoError(t, err)
	vds, up := versionedds.NewVersionedDatastore(ds, list, versioning.VersionKey("2"))
	require.NoError(t, up(ctx))

	initialStatuses := []datatransfer.Status{
		datatransfer.Requested,
		datatransfer.InitiatorPaused,
		datatransfer.ResponderPaused,
		datatransfer.BothPaused,
		datatransfer.Ongoing,
	}
	for i := 0; i < numChannels; i++ {
		transferIDs[i] = datatransfer.TransferID(rand.Uint64())
		initiators[i] = testutil.GeneratePeers(1)[0]
		responders[i] = testutil.GeneratePeers(1)[0]
		baseCids[i] = testutil.GenerateCids(1)[0]
		totalSizes[i] = rand.Uint64()
		sents[i] = rand.Uint64()
		receiveds[i] = rand.Uint64()
		messages[i] = string(testutil.RandomBytes(20))
		vouchers[i] = testutil.NewTestTypedVoucher()
		voucherResults[i] = testutil.NewTestTypedVoucher()
		sentIndex[i] = rand.Int63()
		receivedIndex[i] = rand.Int63()
		queuedIndex[i] = rand.Int63()
		channel := migrations.ChannelStateV2{
			TransferID: transferIDs[i],
			Initiator:  initiators[i],
			Responder:  responders[i],
			BaseCid:    baseCids[i],
			Selector: internal.CborGenCompatibleNode{
				Node: allSelector,
			},
			Sender:    initiators[i],
			Recipient: responders[i],
			TotalSize: totalSizes[i],
			Status:    initialStatuses[i],
			Sent:      sents[i],
			Received:  receiveds[i],
			Message:   messages[i],
			Vouchers: []internal.EncodedVoucher{
				{
					Type: vouchers[i].Type,
					Voucher: internal.CborGenCompatibleNode{
						Node: vouchers[i].Voucher,
					},
				},
			},
			VoucherResults: []internal.EncodedVoucherResult{
				{
					Type: voucherResults[i].Type,
					VoucherResult: internal.CborGenCompatibleNode{
						Node: voucherResults[i].Voucher,
					},
				},
			},
			SentBlocksTotal:     sentIndex[i],
			ReceivedBlocksTotal: receivedIndex[i],
			QueuedBlocksTotal:   queuedIndex[i],
			SelfPeer:            selfPeer,
		}
		buf := new(bytes.Buffer)
		err = channel.MarshalCBOR(buf)
		require.NoError(t, err)
		err = vds.Put(ctx, datastore.NewKey(datatransfer.ChannelID{
			Initiator: initiators[i],
			Responder: responders[i],
			ID:        transferIDs[i],
		}.String()), buf.Bytes())
		require.NoError(t, err)
	}

	channelList, err := channels.New(ds, notifier, &fakeEnv{}, selfPeer)
	require.NoError(t, err)
	err = channelList.Start(ctx)
	require.NoError(t, err)

	expectedStatuses := []datatransfer.Status{
		datatransfer.Requested,
		datatransfer.Ongoing,
		datatransfer.Ongoing,
		datatransfer.Ongoing,
		datatransfer.Ongoing,
	}

	expectedInitiatorPaused := []bool{false, true, false, true, false}
	expectedResponderPaused := []bool{false, false, true, true, false}
	for i := 0; i < numChannels; i++ {

		channel, err := channelList.GetByID(ctx, datatransfer.ChannelID{
			Initiator: initiators[i],
			Responder: responders[i],
			ID:        transferIDs[i],
		})
		require.NoError(t, err)
		require.Equal(t, selfPeer, channel.SelfPeer())
		require.Equal(t, transferIDs[i], channel.TransferID())
		require.Equal(t, baseCids[i], channel.BaseCID())
		require.Equal(t, allSelector, channel.Selector())
		require.Equal(t, initiators[i], channel.Sender())
		require.Equal(t, responders[i], channel.Recipient())
		require.Equal(t, totalSizes[i], channel.TotalSize())
		require.Equal(t, sents[i], channel.Sent())
		require.Equal(t, receiveds[i], channel.Received())
		require.Equal(t, messages[i], channel.Message())
		require.Equal(t, vouchers[i], channel.LastVoucher())
		require.Equal(t, voucherResults[i], channel.LastVoucherResult())
		require.Equal(t, expectedStatuses[i], channel.Status())
		require.Equal(t, expectedInitiatorPaused[i], channel.InitiatorPaused())
		require.Equal(t, expectedResponderPaused[i], channel.ResponderPaused())
		require.Equal(t, basicnode.NewInt(sentIndex[i]), channel.SentIndex())
		require.Equal(t, basicnode.NewInt(receivedIndex[i]), channel.ReceivedIndex())
		require.Equal(t, basicnode.NewInt(queuedIndex[i]), channel.QueuedIndex())

	}
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
