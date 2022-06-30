package impl

import (
	"context"
	"errors"
	"fmt"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"golang.org/x/xerrors"
)

// OnChannelOpened is called when we send a request for data to the other
// peer on the given channel ID
func (m *manager) OnTransportEvent(chid datatransfer.ChannelID, evt datatransfer.TransportEvent) {
	ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
	err := m.processTransferEvent(ctx, chid, evt)
	if err != nil {
		log.Infof("error on channel: %s, closing channel", err)
		err := m.closeChannelWithError(ctx, chid, err)
		if err != nil {
			log.Errorf("error closing channel: %s", err)
		}
	}
}

func (m *manager) processTransferEvent(ctx context.Context, chid datatransfer.ChannelID, transportEvent datatransfer.TransportEvent) error {
	switch evt := transportEvent.(type) {
	case datatransfer.TransportOpenedChannel:
		return m.channels.ChannelOpened(chid)
	case datatransfer.TransportInitiatedTransfer:
		return m.channels.TransferInitiated(chid)
	case datatransfer.TransportReceivedData:
		return m.channels.DataReceived(chid, evt.Size, evt.Index)
	case datatransfer.TransportSentData:
		return m.channels.DataSent(chid, evt.Size, evt.Index)
	case datatransfer.TransportQueuedData:
		return m.channels.DataQueued(chid, evt.Size, evt.Index)
	case datatransfer.TransportReachedDataLimit:
		if err := m.channels.DataLimitExceeded(chid); err != nil {
			return err
		}
		msg := message.UpdateResponse(chid.ID, true)
		return m.transport.SendMessage(ctx, chid, msg)
	/*case datatransfer.TransportReceivedVoucherRequest:
		voucher, err := evt.Request.TypedVoucher()
		if err != nil {
			return err
		}
		return m.channels.NewVoucher(chid, voucher)
	case datatransfer.TransportReceivedUpdateRequest:
		if evt.Request.IsPaused() {
			return m.pauseOther(chid)
		}
		return m.resumeOther(chid)
	case datatransfer.TransportReceivedCancelRequest:
		log.Infof("channel %s: received cancel request, cleaning up channel", chid)
		return m.channels.Cancel(chid)
	case datatransfer.TransportReceivedResponse:
		return m.receiveResponse(chid, evt.Response)*/
	case datatransfer.TransportTransferCancelled:
		log.Warnf("channel %+v was cancelled: %s", chid, evt.ErrorMessage)
		return m.channels.RequestCancelled(chid, errors.New(evt.ErrorMessage))

	case datatransfer.TransportErrorSendingData:
		log.Debugf("channel %+v had transport send error: %s", chid, evt.ErrorMessage)
		return m.channels.SendDataError(chid, errors.New(evt.ErrorMessage))
	case datatransfer.TransportErrorReceivingData:
		log.Debugf("channel %+v had transport receive error: %s", chid, evt.ErrorMessage)
		return m.channels.ReceiveDataError(chid, errors.New(evt.ErrorMessage))
	case datatransfer.TransportCompletedTransfer:
		return m.channelCompleted(chid, evt.Success, evt.ErrorMessage)
	case datatransfer.TransportReceivedRestartExistingChannelRequest:
		return m.restartExistingChannelRequestReceived(chid)
	case datatransfer.TransportErrorSendingMessage:
		return m.channels.SendMessageError(chid, errors.New(evt.ErrorMessage))
	case datatransfer.TransportPaused:
		return m.pause(chid)
	case datatransfer.TransportResumed:
		return m.resume(chid)
	}
	return nil
}

// OnResponseReceived is called when a Response message is received from the responder
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

// OnContextAugment provides an oppurtunity for transports to have data transfer add data to their context (i.e.
// to tie into tracing, etc)
func (m *manager) OnContextAugment(chid datatransfer.ChannelID) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)
		return ctx
	}
}

// channelCompleted is called
// - by the requester when all data for a transfer has been received
// - by the responder when all data for a transfer has been sent
func (m *manager) channelCompleted(chid datatransfer.ChannelID, success bool, errorMessage string) error {

	// read the channel state
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return err
	}

	// If the transferred errored on completion
	if !success {
		// send an error, but only if we haven't already errored/finished transfer already for some reason
		if !chst.Status().TransferComplete() {
			err := fmt.Errorf("data transfer channel %s failed to transfer data: %s", chid, errorMessage)
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
	msg := message.CompleteResponse(chst.TransferID(), true, chst.RequiresFinalization(), nil)
	log.Infow("sending completion message to initiator", "chid", chid)
	ctx, _ := m.spansIndex.SpanForChannel(context.Background(), chid)
	if err := m.transport.SendMessage(ctx, chid, msg); err != nil {
		err := xerrors.Errorf("channel %s: failed to send completion message to initiator: %w", chid, err)
		log.Warnw("failed to send completion message to initiator", "chid", chid, "err", err)
		return m.channels.SendMessageError(chid, err)
	}
	log.Infow("successfully sent completion message to initiator", "chid", chid)

	// set the channel state based on whether its paused final settlement
	if chst.RequiresFinalization() {
		return m.channels.BeginFinalizing(chid)
	}
	return m.channels.Complete(chid)
}

func (m *manager) restartExistingChannelRequestReceived(chid datatransfer.ChannelID) error {
	ctx, _ := m.spansIndex.SpanForChannel(context.TODO(), chid)
	// validate channel exists -> in non-terminal state and that the sender matches
	channel, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil || channel == nil {
		// nothing to do here, we wont handle the request
		return err
	}

	// channel should NOT be terminated
	if channels.IsChannelTerminated(channel.Status()) {
		return fmt.Errorf("cannot restart channel %s: channel already terminated", chid)
	}

	if err := m.openRestartChannel(ctx, channel); err != nil {
		return fmt.Errorf("failed to open restart channel %s: %s", chid, err)
	}

	return nil
}
