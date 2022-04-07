package impl

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
)

// OnChannelOpened is called when we send a request for data to the other
// peer on the given channel ID
func (m *manager) OnChannelOpened(chid datatransfer.ChannelID) error {
	log.Infof("channel %s: opened", chid)

	// Check if the channel is being tracked
	has, err := m.channels.HasChannel(chid)
	if err != nil {
		return err
	}
	if !has {
		return datatransfer.ErrChannelNotFound
	}

	// Fire an event
	return m.channels.ChannelOpened(chid)
}

// OnDataReceived is called when the transport layer reports that it has
// received some data from the sender.
// It fires an event on the channel, updating the sum of received data and
// calls revalidators so they can pause / resume the channel or send a
// message over the transport.
func (m *manager) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) error {
	ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "dataReceived", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
		attribute.String("link", link.String()),
		attribute.Int64("index", index),
		attribute.Int64("size", int64(size)),
	))
	defer span.End()

	err := m.channels.DataReceived(chid, link.(cidlink.Link).Cid, size, index, unique)
	// if this channel is now paused, send the pause message
	if err == datatransfer.ErrPause {
		msg := message.UpdateResponse(chid.ID, true)
		ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
		if err := m.dataTransferNetwork.SendMessage(ctx, chid.Initiator, msg); err != nil {
			return err
		}
	}

	return err
}

// OnDataQueued is called when the transport layer reports that it has queued
// up some data to be sent to the requester.
// It fires an event on the channel, updating the sum of queued data and calls
// revalidators so they can pause / resume or send a message over the transport.
func (m *manager) OnDataQueued(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) (datatransfer.Message, error) {
	// The transport layer reports that some data has been queued up to be sent
	// to the requester, so fire a DataQueued event on the channels state
	// machine.

	ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "dataQueued", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
		attribute.String("link", link.String()),
		attribute.Int64("size", int64(size)),
	))
	defer span.End()

	var msg datatransfer.Message
	err := m.channels.DataQueued(chid, link.(cidlink.Link).Cid, size, index, unique)
	// if this channel is now paused, send the pause message
	if err == datatransfer.ErrPause {
		msg = message.UpdateResponse(chid.ID, true)
	}

	return msg, err
}

func (m *manager) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) error {

	ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "dataSent", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
		attribute.String("link", link.String()),
		attribute.Int64("size", int64(size)),
	))
	defer span.End()

	return m.channels.DataSent(chid, link.(cidlink.Link).Cid, size, index, unique)
}

func (m *manager) OnRequestReceived(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {
	// if request is restart, process as restart
	if request.IsRestart() {
		return m.receiveRestartRequest(chid, request)
	}

	// if request is new, process as new
	if request.IsNew() {
		return m.receiveNewRequest(chid, request)
	}

	// if request is cancel, process as cancel
	if request.IsCancel() {
		log.Infof("channel %s: received cancel request, cleaning up channel", chid)

		m.transport.CleanupChannel(chid)
		return nil, m.channels.Cancel(chid)
	}

	// if request is a new voucher, process as voucher
	if request.IsVoucher() {
		return m.processUpdateVoucher(chid, request)
	}

	// otherwise process as an update message (pause or resume)
	return m.receiveUpdateRequest(chid, request)
}

func (m *manager) OnTransferQueued(chid datatransfer.ChannelID) {
	m.channels.TransferRequestQueued(chid)
}

func (m *manager) OnResponseReceived(chid datatransfer.ChannelID, response datatransfer.Response) error {

	// if response is cancel, process as cancel
	if response.IsCancel() {
		log.Infof("channel %s: received cancel response, cancelling channel", chid)
		return m.channels.Cancel(chid)
	}

	// does this response contain a response to a validation attempt?
	if response.IsVoucherResult() {

		// is there a voucher response in this message?
		if !response.EmptyVoucherResult() {
			// if so decode and save it
			vresult, err := m.decodeVoucherResult(response)
			if err != nil {
				return err
			}
			err = m.channels.NewVoucherResult(chid, vresult)
			if err != nil {
				return err
			}
		}

		// was the validateion attempt successful?
		if !response.Accepted() {
			// if not, error and fail
			log.Infof("channel %s: received rejected response, erroring out channel", chid)
			return m.channels.Error(chid, datatransfer.ErrRejected)
		}
	}

	// was it the response to our initial validation attempt?
	if response.IsNew() {
		log.Infof("channel %s: received new response, accepting channel", chid)
		// if so, accept
		err := m.channels.Accept(chid)
		if err != nil {
			return err
		}
	}

	// was this a response to a restart attempt?
	if response.IsRestart() {
		log.Infof("channel %s: received restart response, restarting channel", chid)
		// if so, restart
		err := m.channels.Restart(chid)
		if err != nil {
			return err
		}
	}

	// was this response a final status message?
	if response.IsComplete() {
		// is the responder paused pending final settlement?
		if !response.IsPaused() {
			// if not, mark the responder done and return
			log.Infow("received complete response,responder not paused, completing channel", "chid", chid)
			return m.channels.ResponderCompletes(chid)
		}

		// if yes, mark the responder being in final settlement
		log.Infow("received complete response, responder is paused, not completing channel", "chid", chid)
		err := m.channels.ResponderBeginsFinalization(chid)
		if err != nil {
			return err
		}
	}

	// handle pause/resume for all response types
	if response.IsPaused() {
		return m.pauseOther(chid)
	}
	return m.resumeOther(chid)
}

func (m *manager) OnRequestCancelled(chid datatransfer.ChannelID, err error) error {
	log.Warnf("channel %+v was cancelled: %s", chid, err)
	return m.channels.RequestCancelled(chid, err)
}

func (m *manager) OnRequestDisconnected(chid datatransfer.ChannelID, err error) error {
	log.Warnf("channel %+v has stalled or disconnected: %s", chid, err)
	return m.channels.Disconnected(chid, err)
}

func (m *manager) OnSendDataError(chid datatransfer.ChannelID, err error) error {
	log.Debugf("channel %+v had transport send error: %s", chid, err)
	return m.channels.SendDataError(chid, err)
}

func (m *manager) OnReceiveDataError(chid datatransfer.ChannelID, err error) error {
	log.Debugf("channel %+v had transport receive error: %s", chid, err)
	return m.channels.ReceiveDataError(chid, err)
}

// OnChannelCompleted is called
// - by the requester when all data for a transfer has been received
// - by the responder when all data for a transfer has been sent
func (m *manager) OnChannelCompleted(chid datatransfer.ChannelID, completeErr error) error {

	// read the channel state
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return err
	}

	// If the transferred errored on completion
	if completeErr != nil {
		// send an error, but only if we haven't already errored for some reason
		if chst.Status() != datatransfer.Failing && chst.Status() != datatransfer.Failed {
			err := xerrors.Errorf("data transfer channel %s failed to transfer data: %w", chid, completeErr)
			log.Warnf(err.Error())
			return m.channels.Error(chid, err)
		}
		return nil
	}

	// if the channel was initiated by this node, simply record the transfer being finished
	if chid.Initiator == m.peerID {
		log.Infof("channel %s: transfer initiated by local node is complete", chid)
		return m.channels.FinishTransfer(chid)
	}

	// otherwise, process as responder
	log.Infow("received OnChannelCompleted, will send completion message to initiator", "chid", chid)

	// generate and send the final status message
	msg, err := message.NewResponse(chst.TransferID(), types.CompleteMessage, true, chst.RequiresFinalization(), datatransfer.EmptyTypeIdentifier, nil)
	if err != nil {
		return err
	}
	log.Infow("sending completion message to initiator", "chid", chid)
	ctx, _ := m.spansIndex.SpanForChannel(context.Background(), chid)
	if err := m.dataTransferNetwork.SendMessage(ctx, chid.Initiator, msg); err != nil {
		err := xerrors.Errorf("channel %s: failed to send completion message to initiator: %w", chid, err)
		log.Warnw("failed to send completion message to initiator", "chid", chid, "err", err)
		return m.OnRequestDisconnected(chid, err)
	}
	log.Infow("successfully sent completion message to initiator", "chid", chid)

	// set the channel state based on whether its paused final settlement
	if chst.RequiresFinalization() {
		return m.channels.BeginFinalizing(chid)
	}
	return m.channels.Complete(chid)
}

func (m *manager) OnContextAugment(chid datatransfer.ChannelID) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)
		return ctx
	}
}

func (m *manager) receiveRestartRequest(chid datatransfer.ChannelID, incoming datatransfer.Request) (datatransfer.Response, error) {
	log.Infof("channel %s: received restart request", chid)

	result, err := m.restartRequest(chid, incoming)
	msg, msgErr := m.validationResponseMessage(types.RestartMessage, incoming.TransferID(), result, err)
	if msgErr != nil {
		return nil, msgErr
	}
	return msg, m.requestError(result, err, false)
}

func (m *manager) receiveNewRequest(chid datatransfer.ChannelID, incoming datatransfer.Request) (datatransfer.Response, error) {
	log.Infof("channel %s: received new channel request from %s", chid, chid.Initiator)

	result, err := m.acceptRequest(chid, incoming)
	msg, msgErr := m.validationResponseMessage(types.NewMessage, incoming.TransferID(), result, err)
	if msgErr != nil {
		return nil, msgErr
	}

	return msg, m.requestError(result, err, false)
}

func (m *manager) restartRequest(chid datatransfer.ChannelID,
	incoming datatransfer.Request) (datatransfer.ValidationResult, error) {

	initiator := chid.Initiator
	if m.peerID == initiator {
		return datatransfer.ValidationResult{}, xerrors.New("initiator cannot be manager peer for a restart request")
	}

	if err := m.validateRestartRequest(context.Background(), initiator, chid, incoming); err != nil {
		return datatransfer.ValidationResult{}, xerrors.Errorf("restart request for channel %s failed validation: %w", chid, err)
	}

	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return datatransfer.ValidationResult{}, err
	}

	result, err := m.revalidate(chst)

	if err != nil || !result.Accepted {
		return result, err
	}

	if err := m.channels.Restart(chid); err != nil {
		return result, xerrors.Errorf("failed to restart channel %s: %w", chid, err)
	}

	if err := m.recordValidationEvents(chid, result, false); err != nil {
		return result, err
	}

	voucher, err := m.decodeVoucher(incoming)
	if err != nil {
		return result, err
	}
	processor, has := m.transportConfigurers.Processor(voucher.Type())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(initiator, chid.String())
	return result, nil
}

func (m *manager) acceptRequest(chid datatransfer.ChannelID, incoming datatransfer.Request) (datatransfer.ValidationResult, error) {

	stor, err := incoming.Selector()
	if err != nil {
		return datatransfer.ValidationResult{}, err
	}

	voucher, err := m.decodeVoucher(incoming)
	if err != nil {
		return datatransfer.ValidationResult{}, err
	}

	var validatorFunc func(datatransfer.ChannelID, peer.ID, datatransfer.Voucher, cid.Cid, ipld.Node) (datatransfer.ValidationResult, error)
	processor, _ := m.validatedTypes.Processor(voucher.Type())
	validator := processor.(datatransfer.RequestValidator)
	if incoming.IsPull() {
		validatorFunc = validator.ValidatePull
	} else {
		validatorFunc = validator.ValidatePush
	}

	result, err := validatorFunc(chid, chid.Initiator, voucher, incoming.BaseCid(), stor)
	if err != nil {
		return result, err
	}

	var dataSender, dataReceiver peer.ID
	if incoming.IsPull() {
		dataSender = m.peerID
		dataReceiver = chid.Initiator
	} else {
		dataSender = chid.Initiator
		dataReceiver = m.peerID
	}

	log.Infow("data-transfer request validated, will create & start tracking channel", "channelID", chid, "payloadCid", incoming.BaseCid())
	_, err = m.channels.CreateNew(m.peerID, incoming.TransferID(), incoming.BaseCid(), stor, voucher, chid.Initiator, dataSender, dataReceiver)
	if err != nil {
		log.Errorw("failed to create and start tracking channel", "channelID", chid, "err", err)
		return result, err
	}
	log.Debugw("successfully created and started tracking channel", "channelID", chid)
	if err := m.channels.Accept(chid); err != nil {
		return result, err
	}

	if err := m.recordValidationEvents(chid, result, false); err != nil {
		return result, err
	}

	processor, has := m.transportConfigurers.Processor(voucher.Type())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(chid.Initiator, chid.String())

	return result, nil
}

func (m *manager) requestError(result datatransfer.ValidationResult, resultErr error, handleResumes bool) error {
	if resultErr != nil {
		return resultErr
	}
	if !result.Accepted {
		return datatransfer.ErrRejected
	}
	if result.LeaveRequestPaused {
		return datatransfer.ErrPause
	}
	if handleResumes {
		return datatransfer.ErrResume
	}
	return nil
}
func (m *manager) recordValidationEvents(chid datatransfer.ChannelID, result datatransfer.ValidationResult, handleResumes bool) error {
	if result.LeaveRequestPaused {
		err := m.channels.PauseResponder(chid)
		if err != nil {
			return err
		}
	} else if handleResumes && result.Accepted {
		err := m.channels.ResumeResponder(chid)
		if err != nil {
			return err
		}
	}

	if result.VoucherResult != nil {
		err := m.channels.NewVoucherResult(chid, result.VoucherResult)
		if err != nil {
			return err
		}
	}

	err := m.channels.SetDataLimit(chid, result.DataLimit)
	if err != nil {
		return err
	}

	err = m.channels.SetRequiresFinalization(chid, result.RequiresFinalization)
	if err != nil {
		return err
	}

	return nil
}

func (m *manager) revalidate(chst datatransfer.ChannelState) (datatransfer.ValidationResult, error) {
	processor, _ := m.validatedTypes.Processor(chst.LastVoucher().Type())
	validator := processor.(datatransfer.RequestValidator)

	return validator.Revalidate(chst.ChannelID(), chst)
}

// revalidateFromRequest converts a voucher in an incoming message to its appropriate
// voucher struct, then runs the revalidator and returns the results.
// returns error if:
//   * reading voucher fails
//   * validation fails
func (m *manager) revalidateFromRequest(chid datatransfer.ChannelID,
	incoming datatransfer.Request) (datatransfer.ChannelState, datatransfer.ValidationResult, error) {
	vouch, err := m.decodeVoucher(incoming)
	if err != nil {
		return nil, datatransfer.ValidationResult{}, err
	}
	err = m.channels.NewVoucher(chid, vouch)
	if err != nil {
		return nil, datatransfer.ValidationResult{}, err
	}

	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return nil, datatransfer.ValidationResult{}, err
	}

	result, err := m.revalidate(chst)
	return chst, result, err
}

func (m *manager) processUpdateVoucher(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {
	chst, result, err := m.revalidateFromRequest(chid, request)
	response, msgErr := m.revalidationResponse(chst, result, err)
	if msgErr != nil {
		return nil, msgErr
	}
	if err := m.recordValidationEvents(chid, result, true); err != nil {
		return nil, err
	}

	return response, m.requestError(result, err, true)
}

func (m *manager) receiveUpdateRequest(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {

	if request.IsPaused() {
		return nil, m.pauseOther(chid)
	}

	err := m.resumeOther(chid)
	if err != nil {
		return nil, err
	}
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return nil, err
	}
	if chst.Status() == datatransfer.ResponderPaused ||
		chst.Status() == datatransfer.ResponderFinalizing {
		return nil, datatransfer.ErrPause
	}
	return nil, nil
}

func (m *manager) revalidationResponse(chst datatransfer.ChannelState, result datatransfer.ValidationResult, resultErr error) (datatransfer.Response, error) {
	messageType := types.VoucherResultMessage
	if chst.Status() == datatransfer.Finalizing {
		messageType = types.CompleteMessage
	}
	msg, err := m.validationResponseMessage(messageType, chst.TransferID(), result, resultErr)
	if err != nil {
		return nil, err
	}
	if resultErr == nil && result.LeaveRequestPaused {
		resultErr = datatransfer.ErrPause
	}
	return msg, resultErr
}
