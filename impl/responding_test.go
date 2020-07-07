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
		expectedEvents       []datatransfer.EventCode
		configureValidator   func(sv *stubbedValidator)
		configureRevalidator func(sv *stubbedRevalidator)
		verify               func(t *testing.T, h *receiverHarness)
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
				require.Equal(t, openChannel.ChannelID, channelID(h.id, h.peers))
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
				require.True(t, response.IsNew())
				require.True(t, response.IsVoucherResult())
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
				require.True(t, response.IsNew())
				require.True(t, response.IsVoucherResult())
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
				require.Equal(t, openChannel.ChannelID, channelID(h.id, h.peers))
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
				require.True(t, response.IsNew())
				require.True(t, response.IsVoucherResult())
				require.Len(t, h.transport.PausedChannels, 1)
				require.Equal(t, channelID(h.id, h.peers), h.transport.PausedChannels[0])
			},
		},
		"new pull request validates": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.Accept},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPull()
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
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
				require.True(t, response.IsNew())
				require.True(t, response.IsVoucherResult())
			},
		},
		"new pull request errors": {
			configureValidator: func(sv *stubbedValidator) {
				sv.expectErrorPull()
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.Error(t, err)
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsVoucherResult())
			},
		},
		"new pull request pauses": {
			configureValidator: func(sv *stubbedValidator) {
				sv.expectPausePull()
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.EqualError(t, err, transport.ErrPause.Error())

				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsVoucherResult())
				require.True(t, response.EmptyVoucherResult())
			},
		},
		"send vouchers from responder fails, push request": {
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				newVoucher := testutil.NewFakeDTType()
				err := h.dt.SendVoucher(h.ctx, channelID(h.id, h.peers), newVoucher)
				require.EqualError(t, err, "cannot send voucher for request we did not initiate")
			},
		},
		"send vouchers from responder fails, pull request": {
			verify: func(t *testing.T, h *receiverHarness) {
				_, _ = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				newVoucher := testutil.NewFakeDTType()
				err := h.dt.SendVoucher(h.ctx, channelID(h.id, h.peers), newVoucher)
				require.EqualError(t, err, "cannot send voucher for request we did not initiate")
			},
		},
		"receive voucher": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.NewVoucherResult, datatransfer.Accept, datatransfer.NewVoucher, datatransfer.ResumeResponder},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.EqualError(t, err, transport.ErrResume.Error())
			},
		},
		"receive pause, unpause": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.PauseInitiator,
				datatransfer.ResumeInitiator},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pauseUpdate)
				require.NoError(t, err)
				_, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.resumeUpdate)
				require.NoError(t, err)
			},
		},
		"receive pause, set pause local, receive unpause": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.PauseInitiator,
				datatransfer.PauseResponder,
				datatransfer.ResumeInitiator},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pauseUpdate)
				require.NoError(t, err)
				h.dt.PauseDataTransferChannel(h.ctx, channelID(h.id, h.peers))
				_, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.resumeUpdate)
				require.EqualError(t, err, transport.ErrPause.Error())
			},
		},
		"receive cancel": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.NewVoucherResult, datatransfer.Accept, datatransfer.Cancel, datatransfer.CleanupComplete},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.cancelUpdate)
				require.NoError(t, err)
				require.Len(t, h.transport.CleanedUpChannels, 1)
				require.Equal(t, channelID(h.id, h.peers), h.transport.CleanedUpChannels[0])
			},
		},
		"validate and revalidate successfully, push": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.Progress,
				datatransfer.NewVoucherResult,
				datatransfer.PauseResponder,
				datatransfer.NewVoucher,
				datatransfer.NewVoucherResult,
				datatransfer.ResumeResponder,
			},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			configureRevalidator: func(srv *stubbedRevalidator) {
				srv.expectPauseRevalidatePush()
				srv.stubResult(testutil.NewFakeDTType())
				srv.expectSuccessRevalidation()
				srv.stubRevalidationResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				err := h.transport.EventHandler.OnDataReceived(
					channelID(h.id, h.peers),
					cidlink.Link{Cid: testutil.GenerateCids(1)[0]},
					12345)
				require.EqualError(t, err, transport.ErrPause.Error())
				require.Len(t, h.network.SentMessages, 1)
				response, ok := h.network.SentMessages[0].Message.(message.DataTransferResponse)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.True(t, response.IsVoucherResult())
				require.False(t, response.EmptyVoucherResult())
				response, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.EqualError(t, err, transport.ErrResume.Error())
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsVoucherResult())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"validate and revalidate with err": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.Progress,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			configureRevalidator: func(srv *stubbedRevalidator) {
				srv.expectErrorRevalidatePush()
				srv.stubResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				err := h.transport.EventHandler.OnDataReceived(
					channelID(h.id, h.peers),
					cidlink.Link{Cid: testutil.GenerateCids(1)[0]},
					12345)
				require.Error(t, err)
				require.Len(t, h.network.SentMessages, 1)
				response, ok := h.network.SentMessages[0].Message.(message.DataTransferResponse)
				require.True(t, ok)
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsVoucherResult())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"validate and revalidate with err with second voucher": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.Progress,
				datatransfer.NewVoucherResult,
				datatransfer.PauseResponder,
				datatransfer.NewVoucher,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPush()
				sv.stubResult(testutil.NewFakeDTType())
			},
			configureRevalidator: func(srv *stubbedRevalidator) {
				srv.expectPauseRevalidatePush()
				srv.stubResult(testutil.NewFakeDTType())
				srv.expectErrorRevalidation()
				srv.stubRevalidationResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				err := h.transport.EventHandler.OnDataReceived(
					channelID(h.id, h.peers),
					cidlink.Link{Cid: testutil.GenerateCids(1)[0]},
					12345)
				require.EqualError(t, err, transport.ErrPause.Error())
				require.Len(t, h.network.SentMessages, 1)
				response, ok := h.network.SentMessages[0].Message.(message.DataTransferResponse)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.True(t, response.IsVoucherResult())
				require.False(t, response.EmptyVoucherResult())
				response, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.Error(t, err)
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsVoucherResult())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"validate and revalidate successfully, pull": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.Progress,
				datatransfer.NewVoucherResult,
				datatransfer.PauseResponder,
				datatransfer.NewVoucher,
				datatransfer.NewVoucherResult,
				datatransfer.ResumeResponder,
			},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPull()
				sv.stubResult(testutil.NewFakeDTType())
			},
			configureRevalidator: func(srv *stubbedRevalidator) {
				srv.expectPauseRevalidatePull()
				srv.stubResult(testutil.NewFakeDTType())
				srv.expectSuccessRevalidation()
				srv.stubRevalidationResult(testutil.NewFakeDTType())
			},
			verify: func(t *testing.T, h *receiverHarness) {
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				msg, err := h.transport.EventHandler.OnDataSent(
					channelID(h.id, h.peers),
					cidlink.Link{Cid: testutil.GenerateCids(1)[0]},
					12345)
				require.EqualError(t, err, transport.ErrPause.Error())
				response, ok := msg.(message.DataTransferResponse)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.True(t, response.IsVoucherResult())
				require.False(t, response.EmptyVoucherResult())
				response, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.EqualError(t, err, transport.ErrResume.Error())
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsVoucherResult())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"validated, finalize, and complete successfully": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.NewVoucherResult,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.BeginFinalizing,
				datatransfer.NewVoucher,
				datatransfer.ResumeResponder,
				datatransfer.CleanupComplete,
			},
			configureValidator: func(sv *stubbedValidator) {
				sv.expectSuccessPull()
				sv.stubResult(testutil.NewFakeDTType())
			},
			configureRevalidator: func(srv *stubbedRevalidator) {
				srv.expectPauseComplete()
				srv.stubResult(testutil.NewFakeDTType())
				srv.expectSuccessRevalidation()
			},
			verify: func(t *testing.T, h *receiverHarness) {
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				err = h.transport.EventHandler.OnChannelCompleted(channelID(h.id, h.peers), true)
				require.Len(t, h.network.SentMessages, 1)
				response, ok := h.network.SentMessages[0].Message.(message.DataTransferResponse)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.True(t, response.IsVoucherResult())
				require.False(t, response.EmptyVoucherResult())
				response, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.EqualError(t, err, transport.ErrResume.Error())
				require.Equal(t, response.TransferID(), h.id)
				require.True(t, response.IsVoucherResult())
				require.False(t, response.IsPaused())
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
			h.pauseUpdate = message.UpdateRequest(h.id, true)
			require.NoError(t, err)
			h.resumeUpdate = message.UpdateRequest(h.id, false)
			require.NoError(t, err)
			updateVoucher := testutil.NewFakeDTType()
			h.voucherUpdate, err = message.VoucherRequest(h.id, updateVoucher.Type(), updateVoucher)
			h.cancelUpdate = message.CancelRequest(h.id)
			require.NoError(t, err)
			h.sv = newSV()
			if verify.configureValidator != nil {
				verify.configureValidator(h.sv)
			}
			err = h.dt.RegisterVoucherType(h.voucher, h.sv)
			h.srv = newSRV()
			if verify.configureRevalidator != nil {
				verify.configureRevalidator(h.srv)
			}
			err = h.dt.RegisterRevalidator(updateVoucher, h.srv)
			require.NoError(t, err)
			verify.verify(t, h)
			h.sv.verifyExpectations(t)
			h.srv.verifyExpectations(t)
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
	srv           *stubbedRevalidator
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

type stubbedRevalidator struct {
	result               datatransfer.VoucherResult
	revalidationResult   datatransfer.VoucherResult
	didRevalidate        bool
	didRevalidatePush    bool
	didRevalidatePull    bool
	didComplete          bool
	expectRevalidate     bool
	expectRevalidatePush bool
	expectRevalidatePull bool
	expectComplete       bool
	revalidationError    error
	pushRevalidateError  error
	pullRevalidateError  error
	completeError        error
}

func newSRV() *stubbedRevalidator {
	return &stubbedRevalidator{}
}

func (srv *stubbedRevalidator) OnPullDataSent(chid datatransfer.ChannelID, additionalBytesSent uint64) (datatransfer.VoucherResult, error) {
	srv.didRevalidatePull = true
	return srv.result, srv.pullRevalidateError
}

func (srv *stubbedRevalidator) OnPushDataReceived(chid datatransfer.ChannelID, additionalBytesReceived uint64) (datatransfer.VoucherResult, error) {
	srv.didRevalidatePush = true
	return srv.result, srv.pushRevalidateError
}

func (srv *stubbedRevalidator) OnComplete(chid datatransfer.ChannelID) (datatransfer.VoucherResult, error) {
	srv.didComplete = true
	return srv.result, srv.completeError
}

func (srv *stubbedRevalidator) Revalidate(chid datatransfer.ChannelID, voucher datatransfer.Voucher) (datatransfer.VoucherResult, error) {
	srv.didRevalidate = true
	return srv.revalidationResult, srv.revalidationError
}

func (srv *stubbedRevalidator) stubResult(voucherResult datatransfer.VoucherResult) {
	srv.result = voucherResult
}

func (srv *stubbedRevalidator) stubRevalidationResult(voucherResult datatransfer.VoucherResult) {
	srv.revalidationResult = voucherResult
}

func (srv *stubbedRevalidator) stubErrorRevalidatePush() {
	srv.pushRevalidateError = errors.New("something went wrong")
}

func (srv *stubbedRevalidator) stubSuccessRevalidatePush() {
	srv.pushRevalidateError = nil
}

func (srv *stubbedRevalidator) stubPauseRevalidatePush() {
	srv.pushRevalidateError = datatransfer.ErrPause
}

func (srv *stubbedRevalidator) expectSuccessRevalidatePush() {
	srv.expectRevalidatePush = true
	srv.stubSuccessRevalidatePush()
}

func (srv *stubbedRevalidator) expectErrorRevalidatePush() {
	srv.expectRevalidatePush = true
	srv.stubErrorRevalidatePush()
}

func (srv *stubbedRevalidator) expectPauseRevalidatePush() {
	srv.expectRevalidatePush = true
	srv.stubPauseRevalidatePush()
}

func (srv *stubbedRevalidator) stubErrorRevalidatePull() {
	srv.pullRevalidateError = errors.New("something went wrong")
}

func (srv *stubbedRevalidator) stubSuccessRevalidatePull() {
	srv.pullRevalidateError = nil
}

func (srv *stubbedRevalidator) stubPauseRevalidatePull() {
	srv.pullRevalidateError = datatransfer.ErrPause
}

func (srv *stubbedRevalidator) expectSuccessRevalidatePull() {
	srv.expectRevalidatePull = true
	srv.stubSuccessRevalidatePull()
}

func (srv *stubbedRevalidator) expectErrorRevalidatePull() {
	srv.expectRevalidatePull = true
	srv.stubErrorRevalidatePull()
}

func (srv *stubbedRevalidator) expectPauseRevalidatePull() {
	srv.expectRevalidatePull = true
	srv.stubPauseRevalidatePull()
}

func (srv *stubbedRevalidator) stubErrorComplete() {
	srv.completeError = errors.New("something went wrong")
}

func (srv *stubbedRevalidator) stubSuccessComplete() {
	srv.completeError = nil
}

func (srv *stubbedRevalidator) stubPauseComplete() {
	srv.completeError = datatransfer.ErrPause
}

func (srv *stubbedRevalidator) expectSuccessComplete() {
	srv.expectComplete = true
	srv.stubSuccessComplete()
}

func (srv *stubbedRevalidator) expectErrorComplete() {
	srv.expectComplete = true
	srv.stubErrorComplete()
}

func (srv *stubbedRevalidator) expectPauseComplete() {
	srv.expectComplete = true
	srv.stubPauseComplete()
}

func (srv *stubbedRevalidator) stubErrorRevalidation() {
	srv.revalidationError = errors.New("something went wrong")
}

func (srv *stubbedRevalidator) stubSuccessRevalidation() {
	srv.revalidationError = nil
}

func (srv *stubbedRevalidator) stubPauseRevalidation() {
	srv.revalidationError = datatransfer.ErrPause
}

func (srv *stubbedRevalidator) expectSuccessRevalidation() {
	srv.expectRevalidate = true
	srv.stubSuccessRevalidation()
}

func (srv *stubbedRevalidator) expectErrorRevalidation() {
	srv.expectRevalidate = true
	srv.stubErrorRevalidation()
}

func (srv *stubbedRevalidator) expectPauseRevalidation() {
	srv.expectRevalidate = true
	srv.stubPauseRevalidation()
}

func (srv *stubbedRevalidator) verifyExpectations(t *testing.T) {
	if srv.expectRevalidate {
		require.True(t, srv.didRevalidate)
	}
	if srv.expectRevalidatePush {
		require.True(t, srv.didRevalidatePush)
	}
	if srv.expectRevalidatePull {
		require.True(t, srv.didRevalidatePull)
	}
	if srv.expectComplete {
		require.True(t, srv.didComplete)
	}
}

func channelID(id datatransfer.TransferID, peers []peer.ID) datatransfer.ChannelID {
	return datatransfer.ChannelID{ID: id, Initiator: peers[1], Responder: peers[0]}
}
