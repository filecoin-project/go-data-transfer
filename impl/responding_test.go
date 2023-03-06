package impl_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dss "github.com/ipfs/go-datastore/sync"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	selectorparse "github.com/ipld/go-ipld-prime/traversal/selector/parse"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels"
	. "github.com/filecoin-project/go-data-transfer/v2/impl"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
)

func TestDataTransferResponding(t *testing.T) {
	// create network
	ctx := context.Background()
	testCases := map[string]struct {
		expectedEvents     []datatransfer.EventCode
		configureValidator func(sv *testutil.StubbedValidator)
		verify             func(t *testing.T, h *receiverHarness)
	}{
		"new push request validates": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				require.Len(t, h.sv.ValidationsReceived, 1)
				validation := h.sv.ValidationsReceived[0]
				assert.False(t, validation.IsPull)
				assert.Equal(t, h.peers[1], validation.Other)
				assert.True(t, ipld.DeepEqual(h.voucher.Voucher, validation.Voucher))
				assert.Equal(t, h.baseCid, validation.BaseCid)
				assert.Equal(t, h.stor, validation.Selector)

				require.Len(t, h.transport.OpenedChannels, 1)
				openChannel := h.transport.OpenedChannels[0]
				require.Equal(t, openChannel.ChannelID, channelID(h.id, h.peers))
				require.Equal(t, openChannel.DataSender, h.peers[1])
				require.Equal(t, openChannel.Root, cidlink.Link{Cid: h.baseCid})
				require.Equal(t, openChannel.Selector, h.stor)
				require.False(t, openChannel.Message.IsRequest())
				response, ok := openChannel.Message.(datatransfer.Response)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsValidationResult())
			},
		},
		"new push request rejects": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: false, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				require.Len(t, h.network.SentMessages, 1)
				responseMessage := h.network.SentMessages[0].Message
				require.False(t, responseMessage.IsRequest())
				response, ok := responseMessage.(datatransfer.Response)
				require.True(t, ok)
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsValidationResult())
			},
		},
		"new push request errors": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectErrorPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				require.Len(t, h.network.SentMessages, 1)
				responseMessage := h.network.SentMessages[0].Message
				require.False(t, responseMessage.IsRequest())
				response, ok := responseMessage.(datatransfer.Response)
				require.True(t, ok)
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsValidationResult())
			},
		},
		"new push request pauses": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, ForcePause: true, VoucherResult: &vr})
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
				response, ok := openChannel.Message.(datatransfer.Response)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsValidationResult())
				require.Len(t, h.transport.PausedChannels, 1)
				require.Equal(t, channelID(h.id, h.peers), h.transport.PausedChannels[0])
			},
		},
		"new pull request validates": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.sv.ValidationsReceived, 1)
				validation := h.sv.ValidationsReceived[0]
				assert.True(t, validation.IsPull)
				assert.Equal(t, h.peers[1], validation.Other)
				assert.True(t, ipld.DeepEqual(h.voucher.Voucher, validation.Voucher))
				assert.Equal(t, h.baseCid, validation.BaseCid)
				assert.Equal(t, h.stor, validation.Selector)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsValidationResult())
			},
		},
		"new pull request rejects": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				sv.StubResult(datatransfer.ValidationResult{Accepted: false})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.EqualError(t, err, datatransfer.ErrRejected.Error())
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsValidationResult())
			},
		},
		"new pull request errors": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectErrorPull()
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
				require.True(t, response.IsValidationResult())
			},
		},
		"new pull request pauses": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, ForcePause: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				response, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.EqualError(t, err, datatransfer.ErrPause.Error())

				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.True(t, response.IsNew())
				require.True(t, response.IsValidationResult())
				require.True(t, response.EmptyVoucherResult())
			},
		},
		"send voucher results from responder succeeds, push request": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				newVoucherResult := testutil.NewTestTypedVoucher()
				err := h.dt.SendVoucherResult(h.ctx, channelID(h.id, h.peers), newVoucherResult)
				require.NoError(t, err)
			},
		},
		"send voucher results from responder succeeds, pull request": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				_, _ = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				newVoucherResult := testutil.NewTestTypedVoucher()
				err := h.dt.SendVoucherResult(h.ctx, channelID(h.id, h.peers), newVoucherResult)
				require.NoError(t, err)
			},
		},
		"send vouchers from responder fails, push request": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				newVoucher := testutil.NewTestTypedVoucher()
				err := h.dt.SendVoucher(h.ctx, channelID(h.id, h.peers), newVoucher)
				require.EqualError(t, err, "cannot send voucher for request we did not initiate")
			},
		},
		"send vouchers from responder fails, pull request": {
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				_, _ = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				newVoucher := testutil.NewTestTypedVoucher()
				err := h.dt.SendVoucher(h.ctx, channelID(h.id, h.peers), newVoucher)
				require.EqualError(t, err, "cannot send voucher for request we did not initiate")
			},
		},
		"receive voucher": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.NewVoucher,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.NoError(t, err)
			},
		},
		"receive pause, unpause": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.PauseInitiator,
				datatransfer.ResumeInitiator},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
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
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.PauseInitiator,
				datatransfer.PauseResponder,
				datatransfer.ResumeInitiator},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pauseUpdate)
				require.NoError(t, err)
				err = h.dt.PauseDataTransferChannel(h.ctx, channelID(h.id, h.peers))
				require.NoError(t, err)
				_, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.resumeUpdate)
				require.EqualError(t, err, datatransfer.ErrPause.Error())
			},
		},
		"receive cancel": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.Cancel,
				datatransfer.CleanupComplete,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.cancelUpdate)
				require.NoError(t, err)
				h.transport.TransportLk.Lock()
				defer h.transport.TransportLk.Unlock()
				require.Len(t, h.transport.CleanedUpChannels, 1)
				require.Equal(t, channelID(h.id, h.peers), h.transport.CleanedUpChannels[0])
			},
		},
		"validate and revalidate successfully, push": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.SetDataLimit,
				datatransfer.TransferInitiated,
				datatransfer.DataReceivedProgress,
				datatransfer.DataReceived,
				datatransfer.DataLimitExceeded,
				datatransfer.NewVoucher,
				datatransfer.NewVoucherResult,
				datatransfer.SetDataLimit,
				datatransfer.ResumeResponder,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, DataLimit: 1000, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				h.transport.EventHandler.OnTransferInitiated(channelID(h.id, h.peers))
				err := h.transport.EventHandler.OnDataReceived(channelID(h.id, h.peers), cidlink.Link{Cid: testutil.GenerateCids(1)[0]}, 12345, 1, true)
				require.EqualError(t, err, datatransfer.ErrPause.Error())
				require.Len(t, h.network.SentMessages, 1)
				response, ok := h.network.SentMessages[0].Message.(datatransfer.Response)
				require.True(t, ok)
				require.Equal(t, response.TransferID(), h.id)
				require.True(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.False(t, response.IsValidationResult())
				response, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.NoError(t, err, nil)
				require.Nil(t, response)
				vr := testutil.NewTestTypedVoucher()
				err = h.dt.UpdateValidationStatus(ctx, channelID(h.id, h.peers), datatransfer.ValidationResult{Accepted: true, DataLimit: 50000, VoucherResult: &vr})
				require.NoError(t, err)
				require.Len(t, h.transport.ResumedChannels, 1)
				resCh := h.transport.ResumedChannels[0]
				require.Equal(t, resCh.ChannelID, channelID(h.id, h.peers))
				response, ok = resCh.Message.(datatransfer.Response)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsValidationResult())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"validate and revalidate with rejection on second voucher": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.SetDataLimit,
				datatransfer.TransferInitiated,
				datatransfer.DataReceivedProgress,
				datatransfer.DataReceived,
				datatransfer.DataLimitExceeded,
				datatransfer.NewVoucher,
				datatransfer.NewVoucherResult,
				datatransfer.Error,
				datatransfer.CleanupComplete,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, DataLimit: 1000, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				h.transport.EventHandler.OnTransferInitiated(channelID(h.id, h.peers))
				err := h.transport.EventHandler.OnDataReceived(channelID(h.id, h.peers), cidlink.Link{Cid: testutil.GenerateCids(1)[0]}, 12345, 1, true)
				require.EqualError(t, err, datatransfer.ErrPause.Error())
				require.Len(t, h.network.SentMessages, 1)
				response, ok := h.network.SentMessages[0].Message.(datatransfer.Response)
				require.True(t, ok)
				require.Equal(t, response.TransferID(), h.id)
				require.True(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.False(t, response.IsValidationResult())
				response, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.NoError(t, err, nil)
				require.Nil(t, response)
				vr := testutil.NewTestTypedVoucher()
				err = h.dt.UpdateValidationStatus(ctx, channelID(h.id, h.peers), datatransfer.ValidationResult{Accepted: false, VoucherResult: &vr})
				require.NoError(t, err)
				require.Len(t, h.transport.ClosedChannels, 1)
				require.Equal(t, h.transport.ClosedChannels[0], channelID(h.id, h.peers))
				require.Len(t, h.network.SentMessages, 2)
				sentMsg := h.network.SentMessages[1]
				response, ok = sentMsg.Message.(datatransfer.Response)
				require.True(t, ok)
				require.False(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsValidationResult())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"validate and revalidate successfully, pull": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.SetDataLimit,
				datatransfer.TransferInitiated,
				datatransfer.DataQueuedProgress,
				datatransfer.DataQueued,
				datatransfer.DataLimitExceeded,
				datatransfer.NewVoucher,
				datatransfer.NewVoucherResult,
				datatransfer.SetDataLimit,
				datatransfer.ResumeResponder,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, DataLimit: 1000, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				h.transport.EventHandler.OnTransferInitiated(channelID(h.id, h.peers))
				msg, err := h.transport.EventHandler.OnDataQueued(
					channelID(h.id, h.peers),
					cidlink.Link{Cid: testutil.GenerateCids(1)[0]},
					12345, 1, true)
				require.EqualError(t, err, datatransfer.ErrPause.Error())
				response, ok := msg.(datatransfer.Response)
				require.True(t, ok)
				require.Equal(t, response.TransferID(), h.id)
				require.True(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsPaused())
				require.False(t, response.IsValidationResult())
				response, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.NoError(t, err, nil)
				require.Nil(t, response)
				vr := testutil.NewTestTypedVoucher()
				err = h.dt.UpdateValidationStatus(ctx, channelID(h.id, h.peers), datatransfer.ValidationResult{Accepted: true, DataLimit: 50000, VoucherResult: &vr})
				require.NoError(t, err)
				require.Len(t, h.transport.ResumedChannels, 1)
				resCh := h.transport.ResumedChannels[0]
				require.Equal(t, resCh.ChannelID, channelID(h.id, h.peers))
				response, ok = resCh.Message.(datatransfer.Response)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.True(t, response.IsValidationResult())
				require.False(t, response.EmptyVoucherResult())
			},
		},
		"validated, finalize, and complete successfully": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.SetRequiresFinalization,
				datatransfer.TransferInitiated,
				datatransfer.BeginFinalizing,
				datatransfer.NewVoucher,
				datatransfer.NewVoucherResult,
				datatransfer.SetRequiresFinalization,
				datatransfer.ResumeResponder,
				datatransfer.CleanupComplete,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, RequiresFinalization: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				h.transport.EventHandler.OnTransferInitiated(channelID(h.id, h.peers))
				err = h.transport.EventHandler.OnChannelCompleted(channelID(h.id, h.peers), nil)
				require.NoError(t, err)
				require.Len(t, h.network.SentMessages, 1)
				response, ok := h.network.SentMessages[0].Message.(datatransfer.Response)
				require.True(t, ok)
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.True(t, response.IsComplete())
				require.True(t, response.IsPaused())
				require.True(t, response.IsValidationResult())
				require.True(t, response.EmptyVoucherResult())
				response, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.voucherUpdate)
				require.NoError(t, err, nil)
				require.Nil(t, response)
				vr := testutil.NewTestTypedVoucher()
				err = h.dt.UpdateValidationStatus(ctx, channelID(h.id, h.peers), datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
				require.NoError(t, err)
				require.Len(t, h.network.SentMessages, 2)
				sentMsg := h.network.SentMessages[1]
				response, ok = sentMsg.Message.(datatransfer.Response)
				require.True(t, ok)
				require.Equal(t, response.TransferID(), h.id)
				require.True(t, response.IsValidationResult())
				require.False(t, response.IsPaused())
			},
		},
		"validated, incomplete response": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.Error,
				datatransfer.CleanupComplete,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				err = h.transport.EventHandler.OnChannelCompleted(channelID(h.id, h.peers), fmt.Errorf("err"))
				require.NoError(t, err)
			},
		},
		"new push request, customized transport": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				err := h.dt.RegisterTransportConfigurer(h.voucher.Type, func(channelID datatransfer.ChannelID, voucher datatransfer.TypedVoucher) []datatransfer.TransportOption {
					return []datatransfer.TransportOption{testutil.RecordCustomizedTransfer()}
				})
				require.NoError(t, err)
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				require.Len(t, h.transport.CustomizedTransfers, 1)
				customizedTransfer := h.transport.CustomizedTransfers[0]
				require.Equal(t, channelID(h.id, h.peers), customizedTransfer)
			},
		},
		"new pull request, customized transport": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				err := h.dt.RegisterTransportConfigurer(h.voucher.Type, func(channelID datatransfer.ChannelID, voucher datatransfer.TypedVoucher) []datatransfer.TransportOption {
					return []datatransfer.TransportOption{testutil.RecordCustomizedTransfer()}
				})
				require.NoError(t, err)
				_, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.transport.CustomizedTransfers, 1)
				customizedTransfer := h.transport.CustomizedTransfers[0]
				require.Equal(t, channelID(h.id, h.peers), customizedTransfer)
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
			dt, err := NewDataTransfer(h.ds, h.network, h.transport)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt)
			h.dt = dt
			ev := eventVerifier{
				expectedEvents: verify.expectedEvents,
				events:         make(chan datatransfer.EventCode, len(verify.expectedEvents)),
			}
			ev.setup(t, dt)
			h.stor = selectorparse.CommonSelector_ExploreAllRecursively
			h.voucher = testutil.NewTestTypedVoucher()
			h.baseCid = testutil.GenerateCids(1)[0]
			h.id = datatransfer.TransferID(rand.Int31())
			h.pullRequest, err = message.NewRequest(h.id, false, true, &h.voucher, h.baseCid, h.stor)
			require.NoError(t, err)
			h.pushRequest, err = message.NewRequest(h.id, false, false, &h.voucher, h.baseCid, h.stor)
			require.NoError(t, err)
			h.pauseUpdate = message.UpdateRequest(h.id, true)
			require.NoError(t, err)
			h.resumeUpdate = message.UpdateRequest(h.id, false)
			require.NoError(t, err)
			updateVoucher := testutil.NewTestTypedVoucher()
			h.voucherUpdate, err = message.VoucherRequest(h.id, &updateVoucher)
			h.cancelUpdate = message.CancelRequest(h.id)
			require.NoError(t, err)
			h.sv = testutil.NewStubbedValidator()
			if verify.configureValidator != nil {
				verify.configureValidator(h.sv)
			}
			require.NoError(t, h.dt.RegisterVoucherType(h.voucher.Type, h.sv))
			require.NoError(t, err)
			verify.verify(t, h)
			h.sv.VerifyExpectations(t)
			ev.verify(ctx, t)
		})
	}
}

func TestDataTransferRestartResponding(t *testing.T) {
	// create network
	ctx := context.Background()
	testCases := map[string]struct {
		expectedEvents     []datatransfer.EventCode
		configureValidator func(sv *testutil.StubbedValidator)
		verify             func(t *testing.T, h *receiverHarness)
	}{
		"receiving a pull restart response": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.Restart, datatransfer.ResumeResponder},
			verify: func(t *testing.T, h *receiverHarness) {
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)

				response, err := message.RestartResponse(channelID.ID, true, false, nil)
				require.NoError(t, err)
				err = h.transport.EventHandler.OnResponseReceived(channelID, response)
				require.NoError(t, err)
			},
		},
		"receiving a push restart request validates and opens a channel for pull": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.TransferInitiated,
				datatransfer.DataReceivedProgress,
				datatransfer.DataReceived,
				datatransfer.DataReceivedProgress,
				datatransfer.DataReceived,
				datatransfer.Restart,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
				sv.ExpectSuccessValidateRestart()
				vr = testutil.NewTestTypedVoucher()
				sv.StubRestartResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// receive an incoming push
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				require.Len(t, h.sv.ValidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 1)
				require.Len(t, h.network.SentMessages, 0)

				// some cids are received
				chid := datatransfer.ChannelID{Initiator: h.peers[1], Responder: h.peers[0], ID: h.pushRequest.TransferID()}
				testCids := testutil.GenerateCids(2)
				ev, ok := h.dt.(datatransfer.EventsHandler)
				require.True(t, ok)
				ev.OnTransferInitiated(chid)
				require.NoError(t, ev.OnDataReceived(chid, cidlink.Link{Cid: testCids[0]}, 12345, 1, true))
				require.NoError(t, ev.OnDataReceived(chid, cidlink.Link{Cid: testCids[1]}, 12345, 2, true))

				// receive restart push request
				req, err := message.NewRequest(h.pushRequest.TransferID(), true, false, &h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], req)
				require.Len(t, h.sv.RevalidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 2)
				require.Len(t, h.network.SentMessages, 0)

				// validate channel that is opened second time
				openChannel := h.transport.OpenedChannels[1]
				require.Equal(t, openChannel.ChannelID, channelID(h.id, h.peers))
				require.Equal(t, openChannel.DataSender, h.peers[1])
				require.Equal(t, openChannel.Root, cidlink.Link{Cid: h.baseCid})
				require.Equal(t, openChannel.Selector, h.stor)
				// assert do not send cids are sent
				require.False(t, openChannel.Message.IsRequest())
				response, ok := openChannel.Message.(datatransfer.Response)
				require.True(t, ok)
				require.True(t, response.IsRestart())
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.False(t, response.IsNew())
				require.True(t, response.IsValidationResult())

				vmsg := h.sv.RevalidationsReceived[0]
				require.Equal(t, channelID(h.id, h.peers), vmsg.ChannelID)
			},
		},
		"receiving a pull restart request validates and sends a success response": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.Restart,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
				sv.ExpectSuccessValidateRestart()
				vr = testutil.NewTestTypedVoucher()
				sv.StubRestartResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// receive an incoming pull
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.sv.ValidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 0)

				// receive restart pull request
				restartReq, err := message.NewRequest(h.id, true, true, &h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				response, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), restartReq)
				require.NoError(t, err)
				require.Len(t, h.sv.RevalidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 0)

				// validate response
				require.True(t, response.IsRestart())
				require.True(t, response.Accepted())
				require.Equal(t, response.TransferID(), h.id)
				require.False(t, response.IsUpdate())
				require.False(t, response.IsCancel())
				require.False(t, response.IsPaused())
				require.False(t, response.IsNew())
				require.True(t, response.IsValidationResult())

				vmsg := h.sv.RevalidationsReceived[0]
				require.Equal(t, channelID(h.id, h.peers), vmsg.ChannelID)
			},
		},
		"restart request fails if channel does not exist": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// receive an incoming pull
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.sv.ValidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 0)

				// receive restart pull request
				restartReq, err := message.NewRequest(h.id, true, true, &h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				p := testutil.GeneratePeers(1)[0]
				chid := datatransfer.ChannelID{ID: h.pullRequest.TransferID(), Initiator: p, Responder: h.peers[0]}
				_, err = h.transport.EventHandler.OnRequestReceived(chid, restartReq)
				require.True(t, errors.As(err, new(*channels.ErrNotFound)))
			},
		},
		"restart request fails if voucher validation fails": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
				datatransfer.Error,
				datatransfer.CleanupComplete,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
				sv.ExpectSuccessValidateRestart()
				sv.StubRestartResult(datatransfer.ValidationResult{Accepted: false})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// receive an incoming pull
				_, err := h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.sv.ValidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 0)

				// receive restart pull request
				h.sv.ExpectErrorPull()
				restartReq, err := message.NewRequest(h.id, true, true, &h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				_, err = h.transport.EventHandler.OnRequestReceived(channelID(h.id, h.peers), restartReq)
				require.EqualError(t, err, datatransfer.ErrRejected.Error())
			},
		},
		"restart request fails if base cid does not match": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// receive an incoming pull
				chid := channelID(h.id, h.peers)
				_, err := h.transport.EventHandler.OnRequestReceived(chid, h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.sv.ValidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 0)

				// receive restart pull request
				randCid := testutil.GenerateCids(1)[0]
				restartReq, err := message.NewRequest(h.id, true, true, &h.voucher, randCid, h.stor)
				require.NoError(t, err)
				_, err = h.transport.EventHandler.OnRequestReceived(chid, restartReq)
				require.EqualError(t, err, fmt.Sprintf("restart request for channel %s failed validation: base cid does not match", chid))
			},
		},
		"restart request fails if voucher type is not decodable": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// receive an incoming pull
				chid := channelID(h.id, h.peers)
				_, err := h.transport.EventHandler.OnRequestReceived(chid, h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.sv.ValidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 0)

				// receive restart pull request

				restartReq, err := message.NewRequest(h.id, true, true, &datatransfer.TypedVoucher{Voucher: h.voucher.Voucher, Type: "rand"}, h.baseCid, h.stor)
				require.NoError(t, err)
				_, err = h.transport.EventHandler.OnRequestReceived(chid, restartReq)
				require.EqualError(t, err, fmt.Sprintf("restart request for channel %s failed validation: channel and request voucher types do not match", chid))
			},
		},
		"restart request fails if voucher does not match": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
				datatransfer.NewVoucherResult,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPull()
				vr := testutil.NewTestTypedVoucher()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true, VoucherResult: &vr})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// receive an incoming pull
				chid := channelID(h.id, h.peers)
				_, err := h.transport.EventHandler.OnRequestReceived(chid, h.pullRequest)
				require.NoError(t, err)
				require.Len(t, h.sv.ValidationsReceived, 1)
				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 0)

				// receive restart pull request
				v := testutil.NewTestTypedVoucherWith("rand")
				restartReq, err := message.NewRequest(h.id, true, true, &v, h.baseCid, h.stor)
				require.NoError(t, err)
				_, err = h.transport.EventHandler.OnRequestReceived(chid, restartReq)
				require.EqualError(t, err, fmt.Sprintf("restart request for channel %s failed validation: channel and request vouchers do not match", chid))
			},
		},
		"ReceiveRestartExistingChannelRequest: Reopen Pull Channel": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.TransferInitiated,
				datatransfer.DataReceivedProgress,
				datatransfer.DataReceived,
				datatransfer.DataReceivedProgress,
				datatransfer.DataReceived,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// create an outgoing pull channel first
				channelID, err := h.dt.OpenPullDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)

				// some cids should already be received
				testCids := testutil.GenerateCids(2)
				ev, ok := h.dt.(datatransfer.EventsHandler)
				require.True(t, ok)
				ev.OnTransferInitiated(channelID)
				require.NoError(t, ev.OnDataReceived(channelID, cidlink.Link{Cid: testCids[0]}, 12345, 1, true))
				require.NoError(t, ev.OnDataReceived(channelID, cidlink.Link{Cid: testCids[1]}, 12345, 2, true))

				// send a request to restart the same pull channel
				restartReq := message.RestartExistingChannelRequest(channelID)
				h.network.Delegate.ReceiveRestartExistingChannelRequest(ctx, h.peers[1], restartReq)

				require.Len(t, h.transport.OpenedChannels, 2)
				require.Len(t, h.network.SentMessages, 0)

				// assert correct channel was created in response to this
				require.Len(t, h.transport.OpenedChannels, 2)
				openChannel := h.transport.OpenedChannels[1]
				require.Equal(t, openChannel.ChannelID, channelID)
				require.Equal(t, openChannel.DataSender, h.peers[1])
				require.Equal(t, openChannel.Root, cidlink.Link{Cid: h.baseCid})
				require.Equal(t, openChannel.Selector, h.stor)
				require.True(t, openChannel.Message.IsRequest())
				require.EqualValues(t, len(testCids), openChannel.Channel.ReceivedCidsTotal())

				// assert a restart request is in the channel
				request, ok := openChannel.Message.(datatransfer.Request)
				require.True(t, ok)
				require.True(t, request.IsRestart())
				require.Equal(t, request.TransferID(), channelID.ID)
				require.Equal(t, request.BaseCid(), h.baseCid)
				require.False(t, request.IsCancel())
				require.True(t, request.IsPull())
				require.False(t, request.IsNew())

				// voucher should be sent correctly
				receivedSelector, err := request.Selector()
				require.NoError(t, err)
				require.Equal(t, receivedSelector, h.stor)
				testutil.AssertTestVoucher(t, request, h.voucher)
			},
		},
		"ReceiveRestartExistingChannelRequest: Resend Push Request": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// create an outgoing push request first
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)

				// send a request to restart the same push request
				restartReq := message.RestartExistingChannelRequest(channelID)
				h.network.Delegate.ReceiveRestartExistingChannelRequest(ctx, h.peers[1], restartReq)

				// assert correct message was sent in response to this
				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 2)

				// assert restart request is well formed
				messageReceived := h.network.SentMessages[1]
				require.Equal(t, messageReceived.PeerID, h.peers[1])
				received := messageReceived.Message
				require.True(t, received.IsRequest())
				receivedRequest, ok := received.(datatransfer.Request)
				require.True(t, ok)
				require.Equal(t, receivedRequest.TransferID(), channelID.ID)
				require.Equal(t, receivedRequest.BaseCid(), h.baseCid)
				require.False(t, receivedRequest.IsCancel())
				require.False(t, receivedRequest.IsPull())
				require.True(t, receivedRequest.IsRestart())
				require.False(t, receivedRequest.IsNew())

				// assert voucher is sent correctly
				receivedSelector, err := receivedRequest.Selector()
				require.NoError(t, err)
				require.Equal(t, receivedSelector, h.stor)
				testutil.AssertTestVoucher(t, receivedRequest, h.voucher)
			},
		},
		"ReceiveRestartExistingChannelRequest: errors if peer is not the initiator": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
			},
			configureValidator: func(sv *testutil.StubbedValidator) {
				sv.ExpectSuccessPush()
				sv.StubResult(datatransfer.ValidationResult{Accepted: true})
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// create an incoming push first
				h.network.Delegate.ReceiveRequest(h.ctx, h.peers[1], h.pushRequest)
				require.Len(t, h.sv.ValidationsReceived, 1)

				// restart req does not anything as we are not the initiator
				chid := datatransfer.ChannelID{Initiator: h.peers[1], Responder: h.peers[0], ID: h.pushRequest.TransferID()}
				restartReq := message.RestartExistingChannelRequest(chid)
				h.network.Delegate.ReceiveRestartExistingChannelRequest(ctx, h.peers[1], restartReq)

				require.Len(t, h.transport.OpenedChannels, 1)
				require.Len(t, h.network.SentMessages, 0)
			},
		},
		"ReceiveRestartExistingChannelRequest: errors if sending peer is not the counter-party on the channel": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open},
			configureValidator: func(sv *testutil.StubbedValidator) {
			},
			verify: func(t *testing.T, h *receiverHarness) {
				// create an outgoing push request first
				p := testutil.GeneratePeers(1)[0]
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, p, h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)

				// sending peer is not the counter-party on the channel
				restartReq := message.RestartExistingChannelRequest(channelID)
				h.network.Delegate.ReceiveRestartExistingChannelRequest(ctx, h.peers[1], restartReq)

				require.Len(t, h.transport.OpenedChannels, 0)
				require.Len(t, h.network.SentMessages, 1)
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
			dt, err := NewDataTransfer(h.ds, h.network, h.transport)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt)
			h.dt = dt
			ev := eventVerifier{
				expectedEvents: verify.expectedEvents,
				events:         make(chan datatransfer.EventCode, len(verify.expectedEvents)),
			}
			ev.setup(t, dt)
			h.stor = selectorparse.CommonSelector_ExploreAllRecursively
			h.voucher = testutil.NewTestTypedVoucher()
			h.baseCid = testutil.GenerateCids(1)[0]
			h.id = datatransfer.TransferID(rand.Int31())
			h.pullRequest, err = message.NewRequest(h.id, false, true, &h.voucher, h.baseCid, h.stor)
			require.NoError(t, err)
			h.pushRequest, err = message.NewRequest(h.id, false, false, &h.voucher, h.baseCid, h.stor)
			require.NoError(t, err)

			h.sv = testutil.NewStubbedValidator()
			if verify.configureValidator != nil {
				verify.configureValidator(h.sv)
			}
			require.NoError(t, h.dt.RegisterVoucherType(h.voucher.Type, h.sv))

			verify.verify(t, h)
			h.sv.VerifyExpectations(t)
			ev.verify(ctx, t)
		})
	}
}

type receiverHarness struct {
	id            datatransfer.TransferID
	pushRequest   datatransfer.Request
	pullRequest   datatransfer.Request
	voucherUpdate datatransfer.Request
	pauseUpdate   datatransfer.Request
	resumeUpdate  datatransfer.Request
	cancelUpdate  datatransfer.Request
	ctx           context.Context
	peers         []peer.ID
	network       *testutil.FakeNetwork
	transport     *testutil.FakeTransport
	sv            *testutil.StubbedValidator
	ds            datastore.Batching
	dt            datatransfer.Manager
	stor          datamodel.Node
	voucher       datatransfer.TypedVoucher
	baseCid       cid.Cid
}

func channelID(id datatransfer.TransferID, peers []peer.ID) datatransfer.ChannelID {
	return datatransfer.ChannelID{ID: id, Initiator: peers[1], Responder: peers[0]}
}
