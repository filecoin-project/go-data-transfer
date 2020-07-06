package impl_test

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dss "github.com/ipfs/go-datastore/sync"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	. "github.com/filecoin-project/go-data-transfer/impl"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/filecoin-project/go-data-transfer/testutil"
	"github.com/filecoin-project/go-data-transfer/transport"
	"github.com/filecoin-project/go-storedcounter"
)

func TestDataTransferResponding(t *testing.T) {
	// create network
	ctx := context.Background()
	testCases := map[string]struct {
		expectedEvents     []datatransfer.EventCode
		configureValidator func(sv *stubbedValidator)
		verify             func(t *testing.T, h *receiverHarness)
	}{
		"new push request validates": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.NewVoucherResult, datatransfer.Accept},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				require.Len(t, h.sv.validationsReceived, 1)
				validation := h.sv.validationsReceived[0]
				assert.False(t, validation.isPull)
				assert.Equal(t, h.peers[1], validation.other)
				assert.Equal(t, h.voucher, validation.voucher)
				assert.Equal(t, h.baseCid, validation.baseCid)
				assert.Equal(t, h.stor, validation.selector)

				require.Len(t, h.transport.OpenedChannels, 1)
				openChannel := h.transport.OpenedChannels[0]
				require.Equal(t, openChannel.ChannelID, datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]})
				require.Equal(t, openChannel.DataSender, h.peers[1])
				require.Equal(t, openChannel.Root, cidlink.Link{Cid: h.baseCid})
				require.Equal(t, openChannel.Selector, h.stor)
				require.False(t, openChannel.Message.IsRequest())
				response, ok := openChannel.Message.(message.DataTransferResponse)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"new push request errors": {
			configureValidator: func(sv *stubbedValidator) {
				sv.expectErrorPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				require.Len(t, h.network.SentMessages, 1)
				responseMessage := h.network.SentMessages[0].Message
				require.False(t, responseMessage.IsRequest())
				response, ok := responseMessage.(message.DataTransferResponse)
				require.True(t, ok)
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"new push request pauses": {
			configureValidator: func(sv *stubbedValidator) {
				sv.expectPausePush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)

				require.Len(t, h.transport.OpenedChannels, 1)
				openChannel := h.transport.OpenedChannels[0]
				require.Equal(t, openChannel.ChannelID, datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]})
				require.Equal(t, openChannel.DataSender, h.peers[1])
				require.Equal(t, openChannel.Root, cidlink.Link{Cid: h.baseCid})
				require.Equal(t, openChannel.Selector, h.stor)
				require.False(t, openChannel.Message.IsRequest())
				response, ok := openChannel.Message.(message.DataTransferResponse)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.False(t, response.EmptyVoucherResult())
				require.Len(t, h.transport.PausedChannels, 1)
				require.Equal(t, datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.transport.PausedChannels[0])
			},
		},
		"new pull request validates": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.Accept},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPull()
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.sv.validationsReceived, 1)
				validation := h.sv.validationsReceived[0]
				assert.True(t, validation.isPull)
				assert.Equal(t, h.peers[1], validation.other)
				assert.Equal(t, h.voucher, validation.voucher)
				assert.Equal(t, h.baseCid, validation.baseCid)
				assert.Equal(t, h.stor, validation.selector)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.EmptyVoucherResult())
			},
		},
		"new pull request errors": {
			configureValidator: func(sv *stubbedValidator) {
				sv.expectErrorPull()
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.pullRequest)
				require.Error(t, err)
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.EmptyVoucherResult())
			},
		},
		"new pull request pauses": {
			configureValidator: func(sv *stubbedValidator) {
				sv.expectPausePull()
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.pullRequest)
				require.EqualError(t, err, transport.ErrPause.Error())

				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.True(t, response.EmptyVoucherResult())
			},
		},
		"send vouchers from responder fails, push request": {
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				newVoucher := testutil.NewFakeDTType()
				err := h.dt.SendVoucher(h.ctx, datatransfer.ChannelID{Initiator: h.peers[1], ID: h.id}, newVoucher)
				require.EqualError(t, err, "cannot send voucher for request we did not initiate")
			},
		},
		"send vouchers from responder fails, pull request": {
			verify: func(t *testing.T, h *receiverHarness) {
				_, _ = h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.pullRequest)
				newVoucher := testutil.NewFakeDTType()
				err := h.dt.SendVoucher(h.ctx, datatransfer.ChannelID{Initiator: h.peers[1], ID: h.id}, newVoucher)
				require.EqualError(t, err, "cannot send voucher for request we did not initiate")
			},
		},
		"receive voucher": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.NewVoucherResult, datatransfer.Accept, datatransfer.NewVoucher},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.voucherUpdate)
				require.NoError(t, err)
			},
		},
		"receive pause, unpause": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.PauseSender,
				datatransfer.ResumeSender},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.pauseUpdate)
				require.NoError(t, err)
				_, err = h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.resumeUpdate)
				require.NoError(t, err)
			},
		},
		"receive pause, set pause local, receive unpause": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.PauseSender,
				datatransfer.PauseReceiver,
				datatransfer.ResumeSender},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.pauseUpdate)
				require.NoError(t, err)
				h.dt.PauseDataTransferChannel(h.ctx, datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]})
				_, err = h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.resumeUpdate)
				require.EqualError(t, err, transport.ErrPause.Error())
			},
		},
		"receive cancel": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.NewVoucherResult, datatransfer.Accept, datatransfer.Cancel},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.cancelUpdate)
				require.NoError(t, err)
				require.Len(t, h.transport.CleanedUpChannels, 1)
				require.Equal(t, datatransfer.ChannelID{ID: h.id, Initiator: h.peers[1]}, h.transport.CleanedUpChannels[0])
			},
		},
	}
	for testCase, verify := range testCases {
		t.Run(testCase, func(t *testing.T) {
			h := &receiverHarness{}
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			h.ctx = ctx
			h.peers = testutil.GeneratePeers(2)
			h.network = testutil.NewFakeNetwork(h.peers[0])
			h.transport = testutil.NewFakeTransport()
			h.ds = dss.MutexWrap(datastore.NewMapDatastore())
			h.storedCounter = storedcounter.New(h.ds, datastore.NewKey("counter"))
			dt, err := NewDataTransfer(h.ds, h.network, h.transport, h.storedCounter)
			require.NoError(t, err)
			dt.Start(ctx)
			h.dt = dt
			ev := eventVerifier{
				expectedEvents: verify.expectedEvents,
				events:         make(chan datatransfer.EventCode, len(verify.expectedEvents)),
			}
			ev.setup(t, dt)
			h.stor = testutil.AllSelector()
			h.voucher = testutil.NewFakeDTType()
			h.baseCid = testutil.GenerateCids(1)[0]
			h.id = datatransfer.TransferID(rand.Int31())
			h.pullRequest, err = message.NewRequest(h.id, true, h.voucher.Type(), h.voucher, h.baseCid, h.stor)
			require.NoError(t, err)
			h.pushRequest, err = message.NewRequest(h.id, false, h.voucher.Type(), h.voucher, h.baseCid, h.stor)
			require.NoError(t, err)
			h.pauseUpdate, err = message.UpdateRequest(h.id, true, datatransfer.EmptyTypeIdentifier, nil)
			require.NoError(t, err)
			h.resumeUpdate, err = message.UpdateRequest(h.id, false, datatransfer.EmptyTypeIdentifier, nil)
			require.NoError(t, err)
			updateVoucher := testutil.NewFakeDTType()
			h.voucherUpdate, err = message.UpdateRequest(h.id, false, updateVoucher.Type(), updateVoucher)
			h.cancelUpdate = message.CancelRequest(h.id)
			require.NoError(t, err)
			h.sv = newSV()
			if verify.configureValidator != nil {
				verify.configureValidator(h.sv)
			}
			err = h.dt.RegisterVoucherType(h.voucher, h.sv)
			require.NoError(t, err)
			verify.verify(t, h)
			h.sv.verifyExpectations(t)
			ev.verify(ctx, t)
		})
	}
}

type receiverHarness struct {
	id            datatransfer.TransferID
	pushRequest   message.DataTransferRequest
	pullRequest   message.DataTransferRequest
	voucherUpdate message.DataTransferRequest
	pauseUpdate   message.DataTransferRequest
	resumeUpdate  message.DataTransferRequest
	cancelUpdate  message.DataTransferRequest
	ctx           context.Context
	peers         []peer.ID
	network       *testutil.FakeNetwork
	transport     *testutil.FakeTransport
	sv            *stubbedValidator
	ds            datastore.Datastore
	storedCounter *storedcounter.StoredCounter
	dt            datatransfer.Manager
	stor          ipld.Node
	voucher       *testutil.FakeDTType
	baseCid       cid.Cid
}

func newSV() *stubbedValidator {
	return &stubbedValidator{nil, false, false, false, false, nil, nil, nil}
}

func (sv *stubbedValidator) ValidatePush(
	sender peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.VoucherResult, error) {
	sv.didPush = true
	sv.validationsReceived = append(sv.validationsReceived, receivedValidation{false, sender, voucher, baseCid, selector})
	return sv.result, sv.pushError
}

func (sv *stubbedValidator) ValidatePull(
	receiver peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.VoucherResult, error) {
	sv.didPull = true
	sv.validationsReceived = append(sv.validationsReceived, receivedValidation{true, receiver, voucher, baseCid, selector})
	return sv.result, sv.pullError
}

func (sv *stubbedValidator) stubResult(voucherResult datatransfer.VoucherResult) {
	sv.result = voucherResult
}
func (sv *stubbedValidator) stubErrorPush() {
	sv.pushError = errors.New("something went wrong")
}

func (sv *stubbedValidator) stubSuccessPush() {
	sv.pushError = nil
}

func (sv *stubbedValidator) stubPausePush() {
	sv.pushError = datatransfer.ErrPause
}

func (sv *stubbedValidator) expectSuccessPush() {
	sv.expectPush = true
	sv.stubSuccessPush()
}

func (sv *stubbedValidator) expectErrorPush() {
	sv.expectPush = true
	sv.stubErrorPush()
}

func (sv *stubbedValidator) expectPausePush() {
	sv.expectPush = true
	sv.stubPausePush()
}

func (sv *stubbedValidator) stubErrorPull() {
	sv.pullError = errors.New("something went wrong")
}

func (sv *stubbedValidator) stubSuccessPull() {
	sv.pullError = nil
}

func (sv *stubbedValidator) stubPausePull() {
	sv.pullError = datatransfer.ErrPause
}

func (sv *stubbedValidator) expectSuccessPull() {
	sv.expectPull = true
	sv.stubSuccessPull()
}

func (sv *stubbedValidator) expectErrorPull() {
	sv.expectPull = true
	sv.stubErrorPull()
}

func (sv *stubbedValidator) expectPausePull() {
	sv.expectPull = true
	sv.stubPausePull()
}

func (sv *stubbedValidator) verifyExpectations(t *testing.T) {
	if sv.expectPush {
		require.True(t, sv.didPush)
	}
	if sv.expectPull {
		require.True(t, sv.didPull)
	}
}

type receivedValidation struct {
	isPull   bool
	other    peer.ID
	voucher  datatransfer.Voucher
	baseCid  cid.Cid
	selector ipld.Node
}

type stubbedValidator struct {
	result              datatransfer.VoucherResult
	didPush             bool
	didPull             bool
	expectPush          bool
	expectPull          bool
	pushError           error
	pullError           error
	validationsReceived []receivedValidation
}
