package impl_test

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dss "github.com/ipfs/go-datastore/sync"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	selectorparse "github.com/ipld/go-ipld-prime/traversal/selector/parse"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	. "github.com/filecoin-project/go-data-transfer/v2/impl"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
)

func TestDataTransferInitiating(t *testing.T) {
	// create network
	ctx := context.Background()
	testCases := map[string]struct {
		expectedEvents []datatransfer.EventCode
		options        []DataTransferOption
		verify         func(t *testing.T, h *harness)
	}{
		"OpenPushDataTransfer": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				require.Equal(t, channelID.Initiator, h.peers[0])
				require.Len(t, h.transport.OpenedChannels, 1)
				openChannel := h.transport.OpenedChannels[0]
				require.Equal(t, openChannel.Channel.ChannelID(), channelID)
				require.Equal(t, openChannel.Channel.Sender(), h.peers[0])
				require.Equal(t, openChannel.Channel.BaseCID(), h.baseCid)
				require.Equal(t, openChannel.Channel.Selector(), h.stor)
				receivedRequest, ok := openChannel.Message.(datatransfer.Request)
				require.True(t, ok)
				require.Equal(t, receivedRequest.TransferID(), channelID.ID)
				require.Equal(t, receivedRequest.BaseCid(), h.baseCid)
				require.False(t, receivedRequest.IsCancel())
				require.False(t, receivedRequest.IsPull())
				receivedSelector, err := receivedRequest.Selector()
				require.NoError(t, err)
				require.Equal(t, receivedSelector, h.stor)
				testutil.AssertTestVoucher(t, receivedRequest, h.voucher)
			},
		},
		"OpenPullDataTransfer": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPullDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				require.Equal(t, channelID.Initiator, h.peers[0])
				require.Len(t, h.transport.OpenedChannels, 1)
				openChannel := h.transport.OpenedChannels[0]
				require.Equal(t, openChannel.Channel.ChannelID(), channelID)
				require.Equal(t, openChannel.Channel.Sender(), h.peers[1])
				require.Equal(t, openChannel.Channel.BaseCID(), h.baseCid)
				require.Equal(t, openChannel.Channel.Selector(), h.stor)
				require.True(t, openChannel.Message.IsRequest())
				receivedRequest, ok := openChannel.Message.(datatransfer.Request)
				require.True(t, ok)
				require.Equal(t, receivedRequest.TransferID(), channelID.ID)
				require.Equal(t, receivedRequest.BaseCid(), h.baseCid)
				require.False(t, receivedRequest.IsCancel())
				require.True(t, receivedRequest.IsPull())
				receivedSelector, err := receivedRequest.Selector()
				require.NoError(t, err)
				require.Equal(t, receivedSelector, h.stor)
				testutil.AssertTestVoucher(t, receivedRequest, h.voucher)
			},
		},
		"SendVoucher with no channel open": {
			verify: func(t *testing.T, h *harness) {
				err := h.dt.SendVoucher(h.ctx, datatransfer.ChannelID{Initiator: h.peers[1], Responder: h.peers[0], ID: 999999}, h.voucher)
				require.EqualError(t, err, datatransfer.ErrChannelNotFound.Error())
			},
		},
		"SendVoucher with channel open, push succeeds": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.NewVoucher},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				voucher := testutil.NewTestTypedVoucher()
				err = h.dt.SendVoucher(ctx, channelID, voucher)
				require.NoError(t, err)
				require.Len(t, h.transport.OpenedChannels, 1)
				require.Len(t, h.transport.MessagesSent, 1)
				received := h.transport.MessagesSent[0].Message
				require.True(t, received.IsRequest())
				receivedRequest, ok := received.(datatransfer.Request)
				require.True(t, ok)
				require.True(t, receivedRequest.IsVoucher())
				require.False(t, receivedRequest.IsCancel())
				testutil.AssertTestVoucher(t, receivedRequest, voucher)
			},
		},
		"SendVoucher with channel open, pull succeeds": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.NewVoucher},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPullDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				voucher := testutil.NewTestTypedVoucher()
				err = h.dt.SendVoucher(ctx, channelID, voucher)
				require.NoError(t, err)
				require.Len(t, h.transport.OpenedChannels, 1)
				require.Len(t, h.transport.MessagesSent, 1)
				received := h.transport.MessagesSent[0].Message
				require.True(t, received.IsRequest())
				receivedRequest, ok := received.(datatransfer.Request)
				require.True(t, ok)
				require.False(t, receivedRequest.IsCancel())
				require.True(t, receivedRequest.IsVoucher())
				testutil.AssertTestVoucher(t, receivedRequest, voucher)
			},
		},
		"reregister voucher type again errors": {
			verify: func(t *testing.T, h *harness) {
				sv := testutil.NewStubbedValidator()
				err := h.dt.RegisterVoucherType(h.voucher.Type, sv)
				require.NoError(t, err)
				err = h.dt.RegisterVoucherType(testutil.TestVoucherType, sv)
				require.EqualError(t, err, "error registering voucher type: identifier already registered: TestVoucher")
			},
		},
		"success response": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.Accept, datatransfer.ResumeResponder},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				response, err := message.NewResponse(channelID.ID, true, false, nil)
				require.NoError(t, err)
				err = h.transport.EventHandler.OnResponseReceived(channelID, response)
				require.NoError(t, err)
			},
		},
		"success response, w/ voucher result": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.NewVoucherResult, datatransfer.Accept, datatransfer.ResumeResponder},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				response, err := message.NewResponse(channelID.ID, true, false, &h.voucherResult)
				require.NoError(t, err)
				err = h.transport.EventHandler.OnResponseReceived(channelID, response)
				require.NoError(t, err)
			},
		},
		"push request, pause behavior": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.Accept, datatransfer.ResumeResponder, datatransfer.PauseInitiator, datatransfer.ResumeInitiator},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				response, err := message.NewResponse(channelID.ID, true, false, nil)
				require.NoError(t, err)
				err = h.transport.EventHandler.OnResponseReceived(channelID, response)
				require.NoError(t, err)
				err = h.dt.PauseDataTransferChannel(h.ctx, channelID)
				require.NoError(t, err)
				require.Len(t, h.transport.PausedChannels, 1)
				require.Equal(t, h.transport.PausedChannels[0], channelID)
				require.Len(t, h.transport.OpenedChannels, 1)
				require.Len(t, h.transport.MessagesSent, 1)
				pauseMessage := h.transport.MessagesSent[0].Message
				require.True(t, pauseMessage.IsUpdate())
				require.True(t, pauseMessage.IsPaused())
				require.True(t, pauseMessage.IsRequest())
				require.False(t, pauseMessage.IsCancel())
				require.Equal(t, pauseMessage.TransferID(), channelID.ID)
				err = h.dt.ResumeDataTransferChannel(h.ctx, channelID)
				require.NoError(t, err)
				require.Len(t, h.transport.ResumedChannels, 1)
				resumedChannel := h.transport.ResumedChannels[0]
				require.Equal(t, resumedChannel, channelID)
				require.Len(t, h.transport.MessagesSent, 2)
				resumeMessage := h.transport.MessagesSent[1].Message
				require.True(t, resumeMessage.IsUpdate())
				require.False(t, resumeMessage.IsPaused())
				require.True(t, resumeMessage.IsRequest())
				require.False(t, resumeMessage.IsCancel())
				require.Equal(t, resumeMessage.TransferID(), channelID.ID)
			},
		},
		"close push request": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.Cancel, datatransfer.CleanupComplete},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				err = h.dt.CloseDataTransferChannel(h.ctx, channelID)
				require.NoError(t, err)
				require.Len(t, h.transport.ClosedChannels, 1)
				require.Equal(t, h.transport.ClosedChannels[0], channelID)

				require.Eventually(t, func() bool {
					return len(h.transport.MessagesSent) == 1
				}, 5*time.Second, 200*time.Millisecond)
				cancelMessage := h.transport.MessagesSent[0].Message
				require.False(t, cancelMessage.IsUpdate())
				require.False(t, cancelMessage.IsPaused())
				require.True(t, cancelMessage.IsRequest())
				require.True(t, cancelMessage.IsCancel())
				require.Equal(t, cancelMessage.TransferID(), channelID.ID)
			},
		},
		"pull request, pause behavior": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.Accept, datatransfer.ResumeResponder, datatransfer.PauseInitiator, datatransfer.ResumeInitiator},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPullDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				response, err := message.NewResponse(channelID.ID, true, false, nil)
				require.NoError(t, err)
				err = h.transport.EventHandler.OnResponseReceived(channelID, response)
				require.NoError(t, err)
				err = h.dt.PauseDataTransferChannel(h.ctx, channelID)
				require.NoError(t, err)
				require.Len(t, h.transport.PausedChannels, 1)
				require.Len(t, h.transport.OpenedChannels, 1)
				require.Len(t, h.transport.MessagesSent, 1)
				pauseMessage := h.transport.MessagesSent[0].Message
				require.True(t, pauseMessage.IsUpdate())
				require.True(t, pauseMessage.IsPaused())
				require.True(t, pauseMessage.IsRequest())
				require.False(t, pauseMessage.IsCancel())
				require.Equal(t, pauseMessage.TransferID(), channelID.ID)
				err = h.dt.ResumeDataTransferChannel(h.ctx, channelID)
				require.NoError(t, err)
				require.Len(t, h.transport.ResumedChannels, 1)
				resumedChannel := h.transport.ResumedChannels[0]
				require.Equal(t, resumedChannel, channelID)
				require.Len(t, h.transport.MessagesSent, 2)
				resumeMessage := h.transport.MessagesSent[1].Message
				require.True(t, resumeMessage.IsUpdate())
				require.False(t, resumeMessage.IsPaused())
				require.True(t, resumeMessage.IsRequest())
				require.False(t, resumeMessage.IsCancel())
				require.Equal(t, resumeMessage.TransferID(), channelID.ID)
			},
		},
		"close pull request": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.Cancel, datatransfer.CleanupComplete},
			verify: func(t *testing.T, h *harness) {
				channelID, err := h.dt.OpenPullDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				err = h.dt.CloseDataTransferChannel(h.ctx, channelID)
				require.NoError(t, err)
				require.Len(t, h.transport.ClosedChannels, 1)
				require.Equal(t, h.transport.ClosedChannels[0], channelID)

				require.Eventually(t, func() bool {
					return len(h.transport.MessagesSent) == 1
				}, 5*time.Second, 200*time.Millisecond)

				cancelMessage := h.transport.MessagesSent[0].Message
				require.False(t, cancelMessage.IsUpdate())
				require.False(t, cancelMessage.IsPaused())
				require.True(t, cancelMessage.IsRequest())
				require.True(t, cancelMessage.IsCancel())
				require.Equal(t, cancelMessage.TransferID(), channelID.ID)
			},
		},
		"customizing push transfer": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open},
			verify: func(t *testing.T, h *harness) {
				err := h.dt.RegisterTransportConfigurer(h.voucher.Type, func(channelID datatransfer.ChannelID, voucher datatransfer.TypedVoucher, transport datatransfer.Transport) {
					ft, ok := transport.(*testutil.FakeTransport)
					if !ok {
						return
					}
					ft.RecordCustomizedTransfer(channelID, voucher)
				})
				require.NoError(t, err)
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				require.Len(t, h.transport.CustomizedTransfers, 1)
				customizedTransfer := h.transport.CustomizedTransfers[0]
				require.Equal(t, channelID, customizedTransfer.ChannelID)
				require.Equal(t, h.voucher, customizedTransfer.Voucher)
			},
		},
		"customizing pull transfer": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open},
			verify: func(t *testing.T, h *harness) {
				err := h.dt.RegisterTransportConfigurer(h.voucher.Type, func(channelID datatransfer.ChannelID, voucher datatransfer.TypedVoucher, transport datatransfer.Transport) {
					ft, ok := transport.(*testutil.FakeTransport)
					if !ok {
						return
					}
					ft.RecordCustomizedTransfer(channelID, voucher)
				})
				require.NoError(t, err)
				channelID, err := h.dt.OpenPullDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				require.Len(t, h.transport.CustomizedTransfers, 1)
				customizedTransfer := h.transport.CustomizedTransfers[0]
				require.Equal(t, channelID, customizedTransfer.ChannelID)
				require.Equal(t, h.voucher, customizedTransfer.Voucher)
			},
		},
	}
	for testCase, verify := range testCases {

		// test for new protocol -> new protocol
		t.Run(testCase, func(t *testing.T) {
			h := &harness{}
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			h.ctx = ctx
			h.peers = testutil.GeneratePeers(2)
			h.transport = testutil.NewFakeTransport()
			h.ds = dss.MutexWrap(datastore.NewMapDatastore())
			dt, err := NewDataTransfer(h.ds, h.peers[0], h.transport, verify.options...)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt)
			h.dt = dt
			ev := eventVerifier{
				expectedEvents: verify.expectedEvents,
				events:         make(chan datatransfer.EventCode, len(verify.expectedEvents)),
			}
			ev.setup(t, dt)
			h.stor = 			h.stor = selectorparse.CommonSelector_ExploreAllRecursively
			h.voucher = testutil.NewTestTypedVoucher()
			h.voucherResult = testutil.NewTestTypedVoucher()
			require.NoError(t, err)
			h.baseCid = testutil.GenerateCids(1)[0]
			verify.verify(t, h)
			ev.verify(ctx, t)
		})
	}
}

func TestDataTransferRestartInitiating(t *testing.T) {
	// create network
	ctx := context.Background()
	testCases := map[string]struct {
		expectedEvents []datatransfer.EventCode
		verify         func(t *testing.T, h *harness)
	}{
		"RestartDataTransferChannel: Manager Peer Create Pull Restart works": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open, datatransfer.DataReceivedProgress, datatransfer.DataReceived, datatransfer.DataReceivedProgress, datatransfer.DataReceived},
			verify: func(t *testing.T, h *harness) {
				// open a pull channel
				channelID, err := h.dt.OpenPullDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				require.Len(t, h.transport.OpenedChannels, 1)

				// some cids should already be received
				testCids := testutil.GenerateCids(2)
				ev, ok := h.dt.(datatransfer.EventsHandler)
				require.True(t, ok)
				require.NoError(t, ev.OnDataReceived(channelID, cidlink.Link{Cid: testCids[0]}, 12345, 1, true))
				require.NoError(t, ev.OnDataReceived(channelID, cidlink.Link{Cid: testCids[1]}, 12345, 2, true))

				// restart that pull channel
				err = h.dt.RestartDataTransferChannel(ctx, channelID)
				require.NoError(t, err)
				require.Len(t, h.transport.RestartedChannels, 1)

				restartedChannel := h.transport.RestartedChannels[0]
				require.Equal(t, restartedChannel.Channel.ChannelID(), channelID)
				require.Equal(t, restartedChannel.Channel.Sender(), h.peers[1])
				require.Equal(t, restartedChannel.Channel.BaseCID(), h.baseCid)
				require.Equal(t, restartedChannel.Channel.Selector(), h.stor)
				require.True(t, restartedChannel.Message.IsRequest())

				receivedRequest := restartedChannel.Message
				require.True(t, ok)
				require.Equal(t, receivedRequest.TransferID(), channelID.ID)
				require.Equal(t, receivedRequest.BaseCid(), h.baseCid)
				require.False(t, receivedRequest.IsCancel())
				require.True(t, receivedRequest.IsPull())
				// assert the second channel open is a restart request
				require.True(t, receivedRequest.IsRestart())

				// voucher should be sent correctly
				receivedSelector, err := receivedRequest.Selector()
				require.NoError(t, err)
				require.Equal(t, receivedSelector, h.stor)
				testutil.AssertTestVoucher(t, receivedRequest, h.voucher)
			},
		},
		"RestartDataTransferChannel: Manager Peer Create Push Restart works": {
			expectedEvents: []datatransfer.EventCode{datatransfer.Open},
			verify: func(t *testing.T, h *harness) {
				// open a push channel
				channelID, err := h.dt.OpenPushDataChannel(h.ctx, h.peers[1], h.voucher, h.baseCid, h.stor)
				require.NoError(t, err)
				require.NotEmpty(t, channelID)
				require.Len(t, h.transport.OpenedChannels, 1)

				// restart that push channel
				err = h.dt.RestartDataTransferChannel(ctx, channelID)
				require.NoError(t, err)
				require.Len(t, h.transport.RestartedChannels, 1)

				// assert restart request is well formed
				restartedChannel := h.transport.RestartedChannels[0]
				require.Equal(t, restartedChannel.Channel.ChannelID(), channelID)
				receivedRequest := restartedChannel.Message
				require.Equal(t, receivedRequest.TransferID(), channelID.ID)
				require.Equal(t, receivedRequest.BaseCid(), h.baseCid)
				require.False(t, receivedRequest.IsCancel())
				require.False(t, receivedRequest.IsPull())
				require.True(t, receivedRequest.IsRestart())

				// assert voucher is sent correctly
				receivedSelector, err := receivedRequest.Selector()
				require.NoError(t, err)
				require.Equal(t, receivedSelector, h.stor)
				testutil.AssertTestVoucher(t, receivedRequest, h.voucher)
			},
		},
		"RestartDataTransferChannel: Manager Peer Receive Push Restart works ": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
			},
			verify: func(t *testing.T, h *harness) {
				ctx := context.Background()

				h.voucherValidator.ExpectSuccessPush()
				h.voucherValidator.StubResult(datatransfer.ValidationResult{Accepted: true})

				// receive a push request
				chid := datatransfer.ChannelID{Initiator: h.peers[1], Responder: h.peers[0], ID: h.pushRequest.TransferID()}
				h.transport.EventHandler.OnRequestReceived(chid, h.pushRequest)
				require.Len(t, h.voucherValidator.ValidationsReceived, 1)

				// restart the push request received above and validate it
				h.voucherValidator.StubRestartResult(datatransfer.ValidationResult{Accepted: true})
				require.NoError(t, h.dt.RestartDataTransferChannel(ctx, chid))
				require.Len(t, h.voucherValidator.RevalidationsReceived, 1)
				require.Len(t, h.transport.MessagesSent, 1)

				// assert validation on restart
				vmsg := h.voucherValidator.RevalidationsReceived[0]
				require.Equal(t, channelID(h.id, h.peers), vmsg.ChannelID)

				// assert req was sent correctly
				req := h.transport.MessagesSent[0]
				require.Equal(t, chid, req.ChannelID)
				received := req.Message
				require.True(t, received.IsRequest())
				receivedRequest, ok := received.(datatransfer.Request)
				require.True(t, ok)
				require.True(t, receivedRequest.IsRestartExistingChannelRequest())
				achId, err := receivedRequest.RestartChannelId()
				require.NoError(t, err)
				require.Equal(t, chid, achId)
			},
		},
		"RestartDataTransferChannel: Manager Peer Receive Pull Restart works ": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
			},
			verify: func(t *testing.T, h *harness) {
				ctx := context.Background()
				// receive a pull request
				h.voucherValidator.ExpectSuccessPull()
				h.voucherValidator.StubResult(datatransfer.ValidationResult{Accepted: true})

				chid := datatransfer.ChannelID{Initiator: h.peers[1], Responder: h.peers[0], ID: h.pushRequest.TransferID()}
				h.transport.EventHandler.OnRequestReceived(chid, h.pullRequest)
				require.Len(t, h.voucherValidator.ValidationsReceived, 1)

				// restart the pull request received above
				h.voucherValidator.ExpectSuccessValidateRestart()
				h.voucherValidator.StubRestartResult(datatransfer.ValidationResult{Accepted: true})
				require.NoError(t, h.dt.RestartDataTransferChannel(ctx, chid))
				require.Len(t, h.voucherValidator.RevalidationsReceived, 1)
				require.Len(t, h.transport.MessagesSent, 1)

				// assert validation on restart
				vmsg := h.voucherValidator.RevalidationsReceived[0]
				require.Equal(t, channelID(h.id, h.peers), vmsg.ChannelID)

				// assert req was sent correctly
				req := h.transport.MessagesSent[0]
				require.Equal(t, chid, req.ChannelID)
				received := req.Message
				require.True(t, received.IsRequest())
				receivedRequest, ok := received.(datatransfer.Request)
				require.True(t, ok)
				require.True(t, receivedRequest.IsRestartExistingChannelRequest())
				achId, err := receivedRequest.RestartChannelId()
				require.NoError(t, err)
				require.Equal(t, chid, achId)
			},
		},
		"RestartDataTransferChannel: Manager Peer Receive Pull Restart fails if validation fails ": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
			},
			verify: func(t *testing.T, h *harness) {
				ctx := context.Background()
				// receive a pull request
				h.voucherValidator.ExpectSuccessPull()
				h.voucherValidator.StubResult(datatransfer.ValidationResult{Accepted: true})
				chid := datatransfer.ChannelID{Initiator: h.peers[1], Responder: h.peers[0], ID: h.pushRequest.TransferID()}
				h.transport.EventHandler.OnRequestReceived(chid, h.pullRequest)
				require.Len(t, h.voucherValidator.ValidationsReceived, 1)

				// restart the pull request received above
				h.voucherValidator.ExpectSuccessValidateRestart()
				h.voucherValidator.StubRestartResult(datatransfer.ValidationResult{Accepted: false})
				require.EqualError(t, h.dt.RestartDataTransferChannel(ctx, chid), datatransfer.ErrRejected.Error())
			},
		},
		"RestartDataTransferChannel: Manager Peer Receive Push Restart fails if validation fails ": {
			expectedEvents: []datatransfer.EventCode{
				datatransfer.Open,
				datatransfer.Accept,
			},
			verify: func(t *testing.T, h *harness) {
				ctx := context.Background()
				// receive a push request
				h.voucherValidator.ExpectSuccessPush()
				h.voucherValidator.StubResult(datatransfer.ValidationResult{Accepted: true})
				chid := datatransfer.ChannelID{Initiator: h.peers[1], Responder: h.peers[0], ID: h.pushRequest.TransferID()}
				h.transport.EventHandler.OnRequestReceived(chid, h.pushRequest)
				require.Len(t, h.voucherValidator.ValidationsReceived, 1)

				// restart the pull request received above
				h.voucherValidator.ExpectSuccessValidateRestart()
				h.voucherValidator.StubRestartResult(datatransfer.ValidationResult{Accepted: false})
				require.EqualError(t, h.dt.RestartDataTransferChannel(ctx, chid), datatransfer.ErrRejected.Error())
			},
		},
		"Fails if channel does not exist": {
			expectedEvents: nil,
			verify: func(t *testing.T, h *harness) {
				channelId := datatransfer.ChannelID{}
				require.Error(t, h.dt.RestartDataTransferChannel(context.Background(), channelId))
			},
		},
	}

	for testCase, verify := range testCases {
		t.Run(testCase, func(t *testing.T) {
			h := &harness{}
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			// create the harness
			h.ctx = ctx
			h.peers = testutil.GeneratePeers(2)
			h.transport = testutil.NewFakeTransport()
			h.ds = dss.MutexWrap(datastore.NewMapDatastore())
			h.voucherValidator = testutil.NewStubbedValidator()

			// setup data transfer``
			dt, err := NewDataTransfer(h.ds, h.peers[0], h.transport)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt)
			h.dt = dt

			// setup eventing
			ev := eventVerifier{
				expectedEvents: verify.expectedEvents,
				events:         make(chan datatransfer.EventCode, len(verify.expectedEvents)),
			}
			ev.setup(t, dt)

			// setup voucher processing
			h.stor = testutil.AllSelector()
			h.voucher = testutil.NewFakeDTType()
			require.NoError(t, h.dt.RegisterVoucherType(h.voucher, h.voucherValidator))
			h.voucherResult = testutil.NewFakeDTType()
			err = h.dt.RegisterVoucherResultType(h.voucherResult)
			require.NoError(t, err)
			h.baseCid = testutil.GenerateCids(1)[0]

			h.id = datatransfer.TransferID(rand.Int31())
			h.pushRequest, err = message.NewRequest(h.id, false, false, &h.voucher, h.baseCid, h.stor)
			require.NoError(t, err)
			h.pullRequest, err = message.NewRequest(h.id, false, true, &h.voucher, h.baseCid, h.stor)
			require.NoError(t, err)

			// run tests steps and verify
			verify.verify(t, h)
			ev.verify(ctx, t)
			h.voucherValidator.VerifyExpectations(t)
		})
	}
}

type harness struct {
	ctx              context.Context
	peers            []peer.ID
	transport        *testutil.FakeTransport
	ds               datastore.Batching
	dt               datatransfer.Manager
	voucherValidator *testutil.StubbedValidator
	stor             datamodel.Node
	voucher          datatransfer.TypedVoucher
	voucherResult    datatransfer.TypedVoucher
	baseCid          cid.Cid

	id          datatransfer.TransferID
	pushRequest datatransfer.Request
	pullRequest datatransfer.Request
}

type eventVerifier struct {
	expectedEvents []datatransfer.EventCode
	events         chan datatransfer.EventCode
}

func (e eventVerifier) setup(t *testing.T, dt datatransfer.Manager) {
	if len(e.expectedEvents) > 0 {
		received := 0
		dt.SubscribeToEvents(func(evt datatransfer.Event, state datatransfer.ChannelState) {
			received++
			e.events <- evt.Code
		})
	}
}

func (e eventVerifier) verify(ctx context.Context, t *testing.T) {
	if len(e.expectedEvents) > 0 {
		receivedEvents := make([]datatransfer.EventCode, 0, len(e.expectedEvents))
		for i := 0; i < len(e.expectedEvents); i++ {
			select {
			case <-ctx.Done():
				t.Fatal("did not receive expected events")
			case event := <-e.events:
				receivedEvents = append(receivedEvents, event)
			}
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case event := <-e.events:
			t.Fatalf("received extra event: %s", datatransfer.Events[event])
		case <-timer.C:
		}
		require.Equal(t, e.expectedEvents, receivedEvents)
	}
}
