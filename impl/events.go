package impl

import (
	"context"

	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
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
// It fires an event on the channel, updating the sum of received data and reports
// back a pause to the transport if the data limit is exceeded
func (m *manager) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) error {
	ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
	_, span := otel.Tracer("data-transfer").Start(ctx, "dataReceived", trace.WithAttributes(
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
// It fires an event on the channel, updating the sum of queued data and reports
// back a pause to the transport if the data limit is exceeded
func (m *manager) OnDataQueued(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) (datatransfer.Message, error) {
	// The transport layer reports that some data has been queued up to be sent
	// to the requester, so fire a DataQueued event on the channels state
	// machine.

	ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
	_, span := otel.Tracer("data-transfer").Start(ctx, "dataQueued", trace.WithAttributes(
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

// OnDataSent is called when the transport layer reports that it has finished
// sending data to the requester.
func (m *manager) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64, unique bool) error {

	ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
	_, span := otel.Tracer("data-transfer").Start(ctx, "dataSent", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
		attribute.String("link", link.String()),
		attribute.Int64("size", int64(size)),
	))
	defer span.End()

	return m.channels.DataSent(chid, link.(cidlink.Link).Cid, size, index, unique)
}

// OnRequestReceived is called when a Request message is received from the initiator
// on the responder
func (m *manager) OnRequestReceived(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {
	// if request is restart request, process as restart
	if request.IsRestart() {
		return m.receiveRestartRequest(chid, request)
	}

	// if request is new request, process as new
	if request.IsNew() {
		return m.receiveNewRequest(chid, request)
	}

	// if request is cancel request, process as cancel
	if request.IsCancel() {
		log.Infof("channel %s: received cancel request, cleaning up channel", chid)

		m.transport.CleanupChannel(chid)
		return nil, m.channels.Cancel(chid)
	}

	// if request contains a new voucher, process updated voucher
	if request.IsVoucher() {
		return m.processUpdateVoucher(chid, request)
	}

	// otherwise process as an "update" message (i.e. a pause or resume)
	return m.receiveUpdateRequest(chid, request)
}

// OnTransferInitiated is called when the transport layer initiates transfer
func (m *manager) OnTransferInitiated(chid datatransfer.ChannelID) {
	m.channels.TransferInitiated(chid)
}

// OnRequestReceived is called when a Response message is received from the responder
// on the initiator
func (m *manager) OnResponseReceived(chid datatransfer.ChannelID, response datatransfer.Response) error {

	// if response is cancel, process as cancel
	if response.IsCancel() {
		log.Infof("channel %s: received cancel response, cancelling channel", chid)
		return m.channels.Cancel(chid)
	}

	// does this response contain a response to a validation attempt?
	if response.IsValidationResult() {

		// is there a voucher response in this message?
		if !response.EmptyVoucherResult() {
			// if so decode and save it
			vresult, err := response.VoucherResult()
			if err != nil {
				return err
			}
			err = m.channels.NewVoucherResult(chid, datatransfer.TypedVoucher{Voucher: vresult, Type: response.VoucherResultType()})
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

	// was this the first response to our initial request
	if response.IsNew() {
		log.Infof("channel %s: received new response, accepting channel", chid)
		// if so, record an accept event (not accepted has already been handled)
		err := m.channels.Accept(chid)
		if err != nil {
			return err
		}
	}

	// was this a response to a restart attempt?
	if response.IsRestart() {
		log.Infof("channel %s: received restart response, restarting channel", chid)
		// if so, record restart
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
		return m.channels.ResponderBeginsFinalization(chid)
	}

	// handle pause/resume for all response types
	if response.IsPaused() {
		return m.pauseOther(chid)
	}
	err := m.resumeOther(chid)
	if err != nil {
		return err
	}
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return err
	}
	if chst.SelfPaused() {
		return datatransfer.ErrPause
	}
	return nil
}

// OnRequestCancelled is called when a transport reports a channel is cancelled
func (m *manager) OnRequestCancelled(chid datatransfer.ChannelID, err error) error {
	log.Warnf("channel %+v was cancelled: %s", chid, err)
	return m.channels.RequestCancelled(chid, err)
}

// OnRequestCancelled is called when a transport reports a channel disconnected
func (m *manager) OnRequestDisconnected(chid datatransfer.ChannelID, err error) error {
	log.Warnf("channel %+v has stalled or disconnected: %s", chid, err)
	return m.channels.Disconnected(chid, err)
}

// OnSendDataError is called when a transport has a network error sending data
func (m *manager) OnSendDataError(chid datatransfer.ChannelID, err error) error {
	log.Debugf("channel %+v had transport send error: %s", chid, err)
	return m.channels.SendDataError(chid, err)
}

// OnReceiveDataError is called when a transport has a network error receiving data
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
	msg, err := message.CompleteResponse(chst.TransferID(), true, chst.RequiresFinalization(), nil)
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

// OnContextAugment provides an oppurtunity for transports to have data transfer add data to their context (i.e.
// to tie into tracing, etc)
func (m *manager) OnContextAugment(chid datatransfer.ChannelID) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)
		return ctx
	}
}
