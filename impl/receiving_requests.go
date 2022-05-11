package impl

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
)

// this file contains methods for processing incoming request messages

// receiveNewRequest handles an incoming new request message
func (m *manager) receiveNewRequest(chid datatransfer.ChannelID, incoming datatransfer.Request) (datatransfer.Response, error) {
	log.Infof("channel %s: received new channel request from %s", chid, chid.Initiator)

	// process the new message, including validations
	result, err := m.acceptRequest(chid, incoming)

	// generate a response message
	msg, msgErr := message.ValidationResultResponse(types.NewMessage, incoming.TransferID(), result, err, result.ForcePause)
	if msgErr != nil {
		return nil, msgErr
	}

	// return the response message and any errors
	return msg, m.requestError(result, err, result.ForcePause)
}

// acceptRequest performs processing (including validation) on a new incoming request
func (m *manager) acceptRequest(chid datatransfer.ChannelID, incoming datatransfer.Request) (datatransfer.ValidationResult, error) {

	// read the voucher and validate the request
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

	// if an error occurred during validation or the request was not accepted, return
	if err != nil || !result.Accepted {
		return result, err
	}

	// create the channel
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

	// record that the channel was accepted
	log.Debugw("successfully created and started tracking channel", "channelID", chid)
	if err := m.channels.Accept(chid); err != nil {
		return result, err
	}

	// read the channel state
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return datatransfer.ValidationResult{}, err
	}

	// record validation events
	if err := m.recordAcceptedValidationEvents(chst, result); err != nil {
		return result, err
	}

	// configure the transport
	processor, has := m.transportConfigurers.Processor(voucher.Type())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(chid.Initiator, chid.String())

	return result, nil
}

// receiveRestartRequest handles an incoming restart request message
func (m *manager) receiveRestartRequest(chid datatransfer.ChannelID, incoming datatransfer.Request) (datatransfer.Response, error) {
	log.Infof("channel %s: received restart request", chid)

	// process the restart message, including validations
	stayPaused, result, err := m.restartRequest(chid, incoming)

	// generate a response message
	msg, msgErr := message.ValidationResultResponse(types.RestartMessage, incoming.TransferID(), result, err, stayPaused)
	if msgErr != nil {
		return nil, msgErr
	}

	// return the response message and any errors
	return msg, m.requestError(result, err, result.ForcePause)
}

// restartRequest performs processing (including validation) on a incoming restart request
func (m *manager) restartRequest(chid datatransfer.ChannelID,
	incoming datatransfer.Request) (bool, datatransfer.ValidationResult, error) {

	// restart requests are invalid if we the initiator
	// (the responder must send a "restart existing channel request")
	initiator := chid.Initiator
	if m.peerID == initiator {
		return false, datatransfer.ValidationResult{}, xerrors.New("initiator cannot be manager peer for a restart request")
	}

	// valide that the request parameters match the original request
	// TODO: not sure this is needed -- the request parameters cannot change,
	// so perhaps the solution is just to ignore them in the message
	if err := m.validateRestartRequest(context.Background(), initiator, chid, incoming); err != nil {
		return false, datatransfer.ValidationResult{}, xerrors.Errorf("restart request for channel %s failed validation: %w", chid, err)
	}

	// read the channel state
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return false, datatransfer.ValidationResult{}, err
	}

	// perform a revalidation against the last voucher
	result, err := m.validateRestart(chst)
	stayPaused := result.LeaveRequestPaused(chst)

	// if an error occurred during validation return
	if err != nil {
		return stayPaused, result, err
	}

	// if the request is now rejected, error the channel
	if !result.Accepted {
		return stayPaused, result, m.recordRejectedValidationEvents(chid, result)
	}

	// record the restart events
	if err := m.channels.Restart(chid); err != nil {
		return stayPaused, result, xerrors.Errorf("failed to restart channel %s: %w", chid, err)
	}

	// record validation events
	if err := m.recordAcceptedValidationEvents(chst, result); err != nil {
		return stayPaused, result, err
	}

	// configure the transport
	voucher, err := m.decodeVoucher(incoming)
	if err != nil {
		return stayPaused, result, err
	}
	processor, has := m.transportConfigurers.Processor(voucher.Type())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(initiator, chid.String())
	return stayPaused, result, nil
}

// processUpdateVoucher handles an incoming request message with an updated voucher
func (m *manager) processUpdateVoucher(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {
	// decode the voucher and save it on the channel
	vouch, err := m.decodeVoucher(request)
	if err != nil {
		return nil, err
	}
	return nil, m.channels.NewVoucher(chid, vouch)
}

// receiveUpdateRequest handles an incoming request message with an updated voucher
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

// requestError generates an error message for the transport, adding
// ErrPause / ErrResume based off the validation result
// TODO: get away from using ErrPause/ErrResume to indicate pause resume,
// which would remove the need for most of this method
func (m *manager) requestError(result datatransfer.ValidationResult, resultErr error, stayPaused bool) error {
	if resultErr != nil {
		return resultErr
	}
	if !result.Accepted {
		return datatransfer.ErrRejected
	}
	if stayPaused {
		return datatransfer.ErrPause
	}
	return nil
}

// recordRejectedValidationEvents sends changes based on an reject validation to the state machine
func (m *manager) recordRejectedValidationEvents(chid datatransfer.ChannelID, result datatransfer.ValidationResult) error {
	if result.VoucherResult != nil {
		if err := m.channels.NewVoucherResult(chid, result.VoucherResult); err != nil {
			return err
		}
	}

	return m.channels.Error(chid, datatransfer.ErrRejected)
}

// recordAcceptedValidationEvents sends changes based on an accepted validation to the state machine
func (m *manager) recordAcceptedValidationEvents(chst datatransfer.ChannelState, result datatransfer.ValidationResult) error {
	chid := chst.ChannelID()

	// record the voucher result if present
	if result.VoucherResult != nil {
		err := m.channels.NewVoucherResult(chid, result.VoucherResult)
		if err != nil {
			return err
		}
	}

	// record the change in data limit if different
	if result.DataLimit != chst.DataLimit() {
		err := m.channels.SetDataLimit(chid, result.DataLimit)
		if err != nil {
			return err
		}
	}

	// record the finalization state if different
	if result.RequiresFinalization != chst.RequiresFinalization() {
		err := m.channels.SetRequiresFinalization(chid, result.RequiresFinalization)
		if err != nil {
			return err
		}
	}

	// pause or resume the request as neccesary
	if result.LeaveRequestPaused(chst) {
		if !chst.Status().IsResponderPaused() {
			err := m.channels.PauseResponder(chid)
			if err != nil {
				return err
			}
		}
	} else {
		if chst.Status().IsResponderPaused() {
			err := m.channels.ResumeResponder(chid)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// validateRestart looks up the appropriate validator and validates a restart
func (m *manager) validateRestart(chst datatransfer.ChannelState) (datatransfer.ValidationResult, error) {
	processor, _ := m.validatedTypes.Processor(chst.Voucher().Type())
	validator := processor.(datatransfer.RequestValidator)

	return validator.ValidateRestart(chst.ChannelID(), chst)
}
