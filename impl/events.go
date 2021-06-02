package impl

import (
	"context"
	"errors"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/encoding"
	"github.com/filecoin-project/go-data-transfer/registry"
)

func (m *manager) OnChannelOpened(chid datatransfer.ChannelID) error {
	log.Infof("channel %s: opened", chid)

	has, err := m.channels.HasChannel(chid)
	if err != nil {
		return err
	}
	if !has {
		return datatransfer.ErrChannelNotFound
	}
	return nil
}

// OnDataReceived is called when the transport layer reports that it has
// received some data from the sender.
// It fires an event on the channel, updating the sum of received data and
// calls revalidators so they can pause / resume the channel or send a
// message over the transport.
func (m *manager) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64) error {
	isNew, err := m.channels.DataReceived(chid, link.(cidlink.Link).Cid, size)
	if err != nil {
		return err
	}

	// If this block has already been received on the channel, take no further
	// action (this can happen when the data-transfer channel is restarted)
	if !isNew {
		return nil
	}

	// If this node initiated the data transfer, there's nothing more to do
	if chid.Initiator == m.peerID {
		return nil
	}

	// Check each revalidator to see if they want to pause / resume, or send
	// a message over the transport
	var result datatransfer.VoucherResult
	var handled bool
	_ = m.revalidators.Each(func(_ datatransfer.TypeIdentifier, _ encoding.Decoder, processor registry.Processor) error {
		revalidator := processor.(datatransfer.Revalidator)
		handled, result, err = revalidator.OnPushDataReceived(chid, size)
		if handled {
			return errors.New("stop processing")
		}
		return nil
	})
	if err != nil || result != nil {
		msg, err := m.processRevalidationResult(chid, result, err)
		if msg != nil {
			if err := m.dataTransferNetwork.SendMessage(context.TODO(), chid.Initiator, msg); err != nil {
				return err
			}
		}
		return err
	}

	return nil
}

// OnDataQueued is called when the transport layer reports that it has queued
// up some data to be sent to the requester.
// It fires an event on the channel, updating the sum of queued data and calls
// revalidators so they can pause / resume or send a message over the transport.
func (m *manager) OnDataQueued(chid datatransfer.ChannelID, link ipld.Link, size uint64) (datatransfer.Message, error) {
	// The transport layer reports that some data has been queued up to be sent
	// to the requester, so fire a DataQueued event on the channels state
	// machine.
	isNew, err := m.channels.DataQueued(chid, link.(cidlink.Link).Cid, size)
	if err != nil {
		return nil, err
	}

	// If this block has already been queued on the channel, take no further
	// action (this can happen when the data-transfer channel is restarted)
	if !isNew {
		return nil, nil
	}

	// If this node initiated the data transfer, there's nothing more to do
	if chid.Initiator == m.peerID {
		return nil, nil
	}

	// Check each revalidator to see if they want to pause / resume, or send
	// a message over the transport.
	// For example if the data-sender is waiting for the receiver to pay for
	// data they may pause the data-transfer.
	var result datatransfer.VoucherResult
	var handled bool
	_ = m.revalidators.Each(func(_ datatransfer.TypeIdentifier, _ encoding.Decoder, processor registry.Processor) error {
		revalidator := processor.(datatransfer.Revalidator)
		handled, result, err = revalidator.OnPullDataSent(chid, size)
		if handled {
			return errors.New("stop processing")
		}
		return nil
	})
	if err != nil || result != nil {
		return m.processRevalidationResult(chid, result, err)
	}

	return nil, nil
}

func (m *manager) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64) error {
	_, err := m.channels.DataSent(chid, link.(cidlink.Link).Cid, size)
	return err
}

func (m *manager) OnRequestReceived(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {
	if request.IsRestart() {
		return m.receiveRestartRequest(chid, request)
	}

	if request.IsNew() {
		return m.receiveNewRequest(chid.Initiator, request)
	}
	if request.IsCancel() {
		log.Infof("channel %s: received cancel request, cleaning up channel", chid)

		m.transport.CleanupChannel(chid)
		return nil, m.channels.Cancel(chid)
	}
	if request.IsVoucher() {
		return m.processUpdateVoucher(chid, request)
	}
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

func (m *manager) OnResponseReceived(chid datatransfer.ChannelID, response datatransfer.Response) error {
	log.Infof("channel %s: received response %+v from provider", chid, response)

	if response.IsCancel() {
		log.Infof("channel %s: received cancel response, cancelling channel", chid)
		return m.channels.Cancel(chid)
	}

	if response.IsVoucherResult() {
		log.Infof("channel %s: received response %+v from provider is a voucher result", chid, response)
		if !response.EmptyVoucherResult() {
			log.Debugf("channel %s: processing non-empty voucher result", chid)
			vresult, err := m.decodeVoucherResult(response)
			if err != nil {
				log.Errorf("channel %s:, failed to decode voucher result, err=%s", chid, err)
				return err
			}
			log.Infof("channel %s: received voucher response %+v", chid, vresult)
			err = m.channels.NewVoucherResult(chid, vresult)
			if err != nil {
				log.Errorf("channel %s: failed NewVoucherResult, err=%s ", chid, err)
				return err
			}
		}

		if !response.Accepted() {
			log.Infof("channel %s: received rejected response, erroring out channel", chid)
			return m.channels.Error(chid, datatransfer.ErrRejected)
		}

		if response.IsNew() {
			log.Infof("channel %s: received new response, accepting channel", chid)
			err := m.channels.Accept(chid)
			if err != nil {
				log.Errorf("channel %s: failed to accept new response, err=%s", chid, err)
				return err
			}
		}

		if response.IsRestart() {
			log.Infof("channel %s: received restart response, restarting channel", chid)
			err := m.channels.Restart(chid)
			if err != nil {
				return err
			}
		}
	}

	if response.IsComplete() && response.Accepted() {
		if !response.IsPaused() {
			log.Infof("channel %s: received complete response, completing channel", chid)
			return m.channels.ResponderCompletes(chid)
		}

		log.Infof("channel %s: received complete response but responder is paused", chid)

		err := m.channels.ResponderBeginsFinalization(chid)
		if err != nil {
			return nil
		}
	}

	if response.IsPaused() {
		return m.pauseOther(chid)
	}
	return m.resumeOther(chid)
}

func (m *manager) OnRequestTimedOut(chid datatransfer.ChannelID, err error) error {
	log.Warnf("channel %+v has timed out: %s", chid, err)
	return m.channels.RequestTimedOut(chid, err)
}

func (m *manager) OnRequestDisconnected(chid datatransfer.ChannelID, err error) error {
	log.Warnf("channel %+v has stalled or disconnected: %s", chid, err)
	return m.channels.Disconnected(chid, err)
}

func (m *manager) OnSendDataError(chid datatransfer.ChannelID, err error) error {
	log.Warnf("channel %+v had transport send error: %s", chid, err)
	return m.channels.SendDataError(chid, err)
}

func (m *manager) OnReceiveDataError(chid datatransfer.ChannelID, err error) error {
	log.Warnf("channel %+v had transport receive error: %s", chid, err)
	return m.channels.ReceiveDataError(chid, err)
}

// OnChannelCompleted is called
// - by the requester when all data for a transfer has been received
// - by the responder when all data for a transfer has been sent
func (m *manager) OnChannelCompleted(chid datatransfer.ChannelID, completeErr error) error {
	// If the channel completed successfully
	if completeErr == nil {
		// If the channel was initiated by the other peer
		if chid.Initiator != m.peerID {
			msg, err := m.completeMessage(chid)
			if err != nil {
				return nil
			}
			if msg != nil {
				// Send the other peer a message that the transfer has completed
				log.Infof("channel %s: sending completion message to initiator", chid)
				if err := m.dataTransferNetwork.SendMessage(context.Background(), chid.Initiator, msg); err != nil {
					err := xerrors.Errorf("channel %s: failed to send completion message to initiator: %w", chid, err)
					log.Warn(err)
					return m.OnRequestDisconnected(chid, err)
				}
			}
			if msg.Accepted() {
				if msg.IsPaused() {
					return m.channels.BeginFinalizing(chid)
				}
				return m.channels.Complete(chid)
			}
			return m.channels.Error(chid, err)
		}

		// The channel was initiated by this node, so move to the finished state
		log.Infof("channel %s: transfer initiated by local node is complete", chid)
		return m.channels.FinishTransfer(chid)
	}

	// There was an error so fire an Error event
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return err
	}
	// send an error, but only if we haven't already errored for some reason
	if chst.Status() != datatransfer.Failing && chst.Status() != datatransfer.Failed {
		err := xerrors.Errorf("data transfer channel %s failed to transfer data: %w", chid, completeErr)
		log.Warnf(err.Error())
		return m.channels.Error(chid, err)
	}
	return nil
}

func (m *manager) receiveRestartRequest(chid datatransfer.ChannelID, incoming datatransfer.Request) (datatransfer.Response, error) {
	log.Infof("channel %s: received restart request", chid)

	result, err := m.restartRequest(chid, incoming)
	msg, msgErr := m.response(true, false, err, incoming.TransferID(), result)
	if msgErr != nil {
		return nil, msgErr
	}
	return msg, err
}

func (m *manager) receiveNewRequest(
	initiator peer.ID,
	incoming datatransfer.Request) (datatransfer.Response, error) {
	log.Infof("received new channel request from %s", initiator)

	result, err := m.acceptRequest(initiator, incoming)
	msg, msgErr := m.response(false, true, err, incoming.TransferID(), result)
	if msgErr != nil {
		return nil, msgErr
	}
	return msg, err
}

func (m *manager) restartRequest(chid datatransfer.ChannelID,
	incoming datatransfer.Request) (datatransfer.VoucherResult, error) {

	initiator := chid.Initiator
	if m.peerID == initiator {
		return nil, xerrors.New("initiator cannot be manager peer for a restart request")
	}

	if err := m.validateRestartRequest(context.Background(), initiator, chid, incoming); err != nil {
		return nil, xerrors.Errorf("restart request for channel %s failed validation: %w", chid, err)
	}

	stor, err := incoming.Selector()
	if err != nil {
		return nil, err
	}

	voucher, result, err := m.validateVoucher(true, initiator, incoming, incoming.IsPull(), incoming.BaseCid(), stor)
	if err != nil && err != datatransfer.ErrPause {
		return result, xerrors.Errorf("failed to validate voucher: %w", err)
	}
	voucherErr := err

	if result != nil {
		err := m.channels.NewVoucherResult(chid, result)
		if err != nil {
			return result, err
		}
	}
	if err := m.channels.Restart(chid); err != nil {
		return result, xerrors.Errorf("failed to restart channel %s: %w", chid, err)
	}
	processor, has := m.transportConfigurers.Processor(voucher.Type())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(initiator, chid.String())
	if voucherErr == datatransfer.ErrPause {
		err := m.channels.PauseResponder(chid)
		if err != nil {
			return result, err
		}
	}
	return result, voucherErr
}

func (m *manager) acceptRequest(
	initiator peer.ID,
	incoming datatransfer.Request) (datatransfer.VoucherResult, error) {

	stor, err := incoming.Selector()
	if err != nil {
		return nil, err
	}

	voucher, result, err := m.validateVoucher(false, initiator, incoming, incoming.IsPull(), incoming.BaseCid(), stor)
	if err != nil && err != datatransfer.ErrPause {
		return result, err
	}
	voucherErr := err

	var dataSender, dataReceiver peer.ID
	if incoming.IsPull() {
		dataSender = m.peerID
		dataReceiver = initiator
	} else {
		dataSender = initiator
		dataReceiver = m.peerID
	}

	chid, err := m.channels.CreateNew(m.peerID, incoming.TransferID(), incoming.BaseCid(), stor, voucher, initiator, dataSender, dataReceiver)
	if err != nil {
		return result, err
	}
	if result != nil {
		err := m.channels.NewVoucherResult(chid, result)
		if err != nil {
			return result, err
		}
	}
	if err := m.channels.Accept(chid); err != nil {
		return result, err
	}
	processor, has := m.transportConfigurers.Processor(voucher.Type())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(initiator, chid.String())
	if voucherErr == datatransfer.ErrPause {
		err := m.channels.PauseResponder(chid)
		if err != nil {
			return result, err
		}
	}
	return result, voucherErr
}

// validateVoucher converts a voucher in an incoming message to its appropriate
// voucher struct, then runs the validator and returns the results.
// returns error if:
//   * reading voucher fails
//   * deserialization of selector fails
//   * validation fails
func (m *manager) validateVoucher(
	isRestart bool,
	sender peer.ID,
	incoming datatransfer.Request,
	isPull bool,
	baseCid cid.Cid,
	stor ipld.Node,
) (datatransfer.Voucher, datatransfer.VoucherResult, error) {
	vouch, err := m.decodeVoucher(incoming, m.validatedTypes)
	if err != nil {
		return nil, nil, err
	}
	var validatorFunc func(bool, peer.ID, datatransfer.Voucher, cid.Cid, ipld.Node) (datatransfer.VoucherResult, error)
	processor, _ := m.validatedTypes.Processor(vouch.Type())
	validator := processor.(datatransfer.RequestValidator)
	if isPull {
		validatorFunc = validator.ValidatePull
	} else {
		validatorFunc = validator.ValidatePush
	}

	result, err := validatorFunc(isRestart, sender, vouch, baseCid, stor)
	if isPull {
		log.Infof("ValidatePull, result=%s, err=%s", result, err)
	}

	return vouch, result, err
}

// revalidateVoucher converts a voucher in an incoming message to its appropriate
// voucher struct, then runs the revalidator and returns the results.
// returns error if:
//   * reading voucher fails
//   * deserialization of selector fails
//   * validation fails
func (m *manager) revalidateVoucher(chid datatransfer.ChannelID,
	incoming datatransfer.Request) (datatransfer.Voucher, datatransfer.VoucherResult, error) {
	vouch, err := m.decodeVoucher(incoming, m.revalidators)
	if err != nil {
		return nil, nil, err
	}
	processor, _ := m.revalidators.Processor(vouch.Type())
	validator := processor.(datatransfer.Revalidator)

	result, err := validator.Revalidate(chid, vouch)
	return vouch, result, err
}

func (m *manager) processUpdateVoucher(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {
	vouch, result, voucherErr := m.revalidateVoucher(chid, request)
	if vouch != nil {
		err := m.channels.NewVoucher(chid, vouch)
		if err != nil {
			return nil, err
		}
	}
	return m.processRevalidationResult(chid, result, voucherErr)
}

func (m *manager) revalidationResponse(chid datatransfer.ChannelID, result datatransfer.VoucherResult, resultErr error) (datatransfer.Response, error) {
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return nil, err
	}
	if chst.Status() == datatransfer.Finalizing {
		return m.completeResponse(resultErr, chid.ID, result)
	}
	return m.response(false, false, resultErr, chid.ID, result)
}

func (m *manager) processRevalidationResult(chid datatransfer.ChannelID, result datatransfer.VoucherResult, resultErr error) (datatransfer.Response, error) {
	vresMessage, err := m.revalidationResponse(chid, result, resultErr)

	if err != nil {
		return nil, err
	}
	if result != nil {
		err := m.channels.NewVoucherResult(chid, result)
		if err != nil {
			return nil, err
		}
	}

	if resultErr == nil {
		return vresMessage, nil
	}

	if resultErr == datatransfer.ErrPause {
		err := m.pause(chid)
		if err != nil {
			return nil, err
		}
		return vresMessage, datatransfer.ErrPause
	}

	if resultErr == datatransfer.ErrResume {
		err = m.resume(chid)
		if err != nil {
			return nil, err
		}
		return vresMessage, datatransfer.ErrResume
	}
	return vresMessage, resultErr
}

func (m *manager) completeMessage(chid datatransfer.ChannelID) (datatransfer.Response, error) {
	var result datatransfer.VoucherResult
	var resultErr error
	var handled bool
	_ = m.revalidators.Each(func(_ datatransfer.TypeIdentifier, _ encoding.Decoder, processor registry.Processor) error {
		revalidator := processor.(datatransfer.Revalidator)
		handled, result, resultErr = revalidator.OnComplete(chid)
		if handled {
			return errors.New("stop processing")
		}
		return nil
	})
	if result != nil {
		err := m.channels.NewVoucherResult(chid, result)
		if err != nil {
			return nil, err
		}
	}

	return m.completeResponse(resultErr, chid.ID, result)
}
