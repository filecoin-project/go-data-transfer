package datatransfer

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/libp2p/go-libp2p-core/peer"
)

// ValidationResult describes the result of validating a voucher
type ValidationResult struct {
	// Accepted indicates whether the request was accepted. If a request is not
	// accepted, the request fails. This is true for revalidation as well
	Accepted bool
	// VoucherResult provides information to the other party about what happened
	// with the voucher
	VoucherResult *TypedVoucher
	// ForcePause indicates whether the request should be paused, regardless
	// of data limit and finalization status
	ForcePause bool
	// DataLimit specifies how much data this voucher is good for. When the amount
	// of data specified is reached (or shortly after), the request will pause
	// pending revalidation. 0 indicates no limit.
	DataLimit uint64
	// RequiresFinalization indicates at the end of the transfer, the channel should
	// be left open for a final settlement
	RequiresFinalization bool
}

// Equals checks the deep equality of two ValidationResult values
func (vr ValidationResult) Equals(vr2 ValidationResult) bool {
	return vr.Accepted == vr2.Accepted &&
		vr.ForcePause == vr2.ForcePause &&
		vr.DataLimit == vr2.DataLimit &&
		vr.RequiresFinalization == vr2.RequiresFinalization &&
		(vr.VoucherResult == nil) == (vr2.VoucherResult == nil) &&
		(vr.VoucherResult == nil || vr.VoucherResult.Equals(*vr2.VoucherResult))
}

// LeaveRequestPaused indicates whether all conditions are met to resume a request
func (vr ValidationResult) LeaveRequestPaused(chst ChannelState) bool {
	if vr.ForcePause {
		return true
	}
	if vr.RequiresFinalization && chst.Status().InFinalization() {
		return true
	}
	var limitFactor uint64
	if chst.IsPull() {
		limitFactor = chst.Queued()
	} else {
		limitFactor = chst.Received()
	}
	return vr.DataLimit != 0 && limitFactor >= vr.DataLimit
}

// RequestValidator is an interface implemented by the client of the
// data transfer module to validate requests
type RequestValidator interface {
	// ValidatePush validates a push request received from the peer that will send data
	// -- All information about the validation operation is contained in ValidationResult,
	// including if it was rejected. Information about why a rejection occurred is embedded
	// in the VoucherResult.
	// -- error indicates something went wrong with the actual process of trying to validate
	ValidatePush(
		chid ChannelID,
		sender peer.ID,
		voucher datamodel.Node,
		baseCid cid.Cid,
		selector datamodel.Node) (ValidationResult, error)
	// ValidatePull validates a pull request received from the peer that will receive data
	// -- All information about the validation operation is contained in ValidationResult,
	// including if it was rejected. Information about why a rejection occurred should be embedded
	// in the VoucherResult.
	// -- error indicates something went wrong with the actual process of trying to validate
	ValidatePull(
		chid ChannelID,
		receiver peer.ID,
		voucher datamodel.Node,
		baseCid cid.Cid,
		selector datamodel.Node) (ValidationResult, error)

	// ValidateRestart validates restarting a request
	// -- All information about the validation operation is contained in ValidationResult,
	// including if it was rejected. Information about why a rejection occurred should be embedded
	// in the VoucherResult.
	// -- error indicates something went wrong with the actual process of trying to validate
	ValidateRestart(channelID ChannelID, channel ChannelState) (ValidationResult, error)
}

// TransportConfigurer provides a mechanism to provide transport specific configuration for a given voucher type
type TransportConfigurer func(chid ChannelID, voucher TypedVoucher, transport Transport)

// ReadyFunc is function that gets called once when the data transfer module is ready
type ReadyFunc func(error)

// Manager is the core interface presented by all implementations of
// of the data transfer sub system
type Manager interface {

	// Start initializes data transfer processing
	Start(ctx context.Context) error

	// OnReady registers a listener for when the data transfer comes on line
	OnReady(ReadyFunc)

	// Stop terminates all data transfers and ends processing
	Stop(ctx context.Context) error

	// RegisterVoucherType registers a validator for the given voucher type
	// will error if voucher type does not implement voucher
	// or if there is a voucher type registered with an identical identifier
	RegisterVoucherType(voucherType TypeIdentifier, validator RequestValidator) error

	// RegisterVoucherResultType allows deserialization of a voucher result,
	// so that a listener can read the metadata
	RegisterVoucherResultType(resultType TypeIdentifier) error

	// RegisterTransportConfigurer registers the given transport configurer to be run on requests with the given voucher
	// type
	RegisterTransportConfigurer(voucherType TypeIdentifier, configurer TransportConfigurer) error

	// open a data transfer that will send data to the recipient peer and
	// transfer parts of the piece that match the selector
	OpenPushDataChannel(ctx context.Context, to peer.ID, voucher TypedVoucher, baseCid cid.Cid, selector datamodel.Node) (ChannelID, error)

	// open a data transfer that will request data from the sending peer and
	// transfer parts of the piece that match the selector
	OpenPullDataChannel(ctx context.Context, to peer.ID, voucher TypedVoucher, baseCid cid.Cid, selector datamodel.Node) (ChannelID, error)

	// send an intermediate voucher as needed when the receiver sends a request for revalidation
	SendVoucher(ctx context.Context, chid ChannelID, voucher TypedVoucher) error

	// send information from the responder to update the initiator on the state of their voucher
	SendVoucherResult(ctx context.Context, chid ChannelID, voucherResult TypedVoucher) error

	// Update the validation status for a given channel, to change data limits, finalization, accepted status, and pause state
	// and send new voucher results as
	UpdateValidationStatus(ctx context.Context, chid ChannelID, validationResult ValidationResult) error

	// close an open channel (effectively a cancel)
	CloseDataTransferChannel(ctx context.Context, chid ChannelID) error

	// pause a data transfer channel (only allowed if transport supports it)
	PauseDataTransferChannel(ctx context.Context, chid ChannelID) error

	// resume a data transfer channel (only allowed if transport supports it)
	ResumeDataTransferChannel(ctx context.Context, chid ChannelID) error

	// get status of a transfer
	TransferChannelStatus(ctx context.Context, x ChannelID) Status

	// get channel state
	ChannelState(ctx context.Context, chid ChannelID) (ChannelState, error)

	// get notified when certain types of events happen
	SubscribeToEvents(subscriber Subscriber) Unsubscribe

	// get all in progress transfers
	InProgressChannels(ctx context.Context) (map[ChannelID]ChannelState, error)

	// RestartDataTransferChannel restarts an existing data transfer channel
	RestartDataTransferChannel(ctx context.Context, chid ChannelID) error
}
