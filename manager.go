package datatransfer

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/libp2p/go-libp2p-core/peer"
)

// ValidationResult describes the result of validating a voucher
type ValidationResult struct {
	// VoucherResult provides information to the other party about what happened
	// with the voucher
	VoucherResult
	// Accepted indicates where the voucher was accepted
	// if a voucher is not accepted, the request fails
	Accepted bool
	// LeaveRequestPaused indicates whether the request should stay paused
	// even if the voucher was accepted
	LeaveRequestPaused bool
	// DataLimit specifies how much data this voucher is good for. When the amount
	// amount data specified is reached (or shortly after), the request will pause
	// pending revalidation. 0 indicates no limit.
	DataLimit uint64
	// RevalidateToComplete indicates at the end of the transfer, the channel should
	// be left open for a final settlement
	RevalidateToComplete bool
}

// RequestValidator is an interface implemented by the client of the
// data transfer module to validate requests
type RequestValidator interface {
	// ValidatePush validates a push request received from the peer that will send data
	// All information about the validation operation is contained in ValidationResult
	// -- including if it was rejected.
	// error indicates something went wrong with the actual process of trying to validate
	ValidatePush(
		chid ChannelID,
		sender peer.ID,
		voucher Voucher,
		baseCid cid.Cid,
		selector ipld.Node) (ValidationResult, error)
	// ValidatePull validates a pull request received from the peer that will receive data
	// All information about the validation operation is contained in ValidationResult
	// -- including if it was rejected.
	// error indicates something went wrong with the actual process of trying to validate
	ValidatePull(
		chid ChannelID,
		receiver peer.ID,
		voucher Voucher,
		baseCid cid.Cid,
		selector ipld.Node) (ValidationResult, error)

	// Revalidate revalidates a request with a new voucher
	// All information about the validation operation is contained in ValidationResult
	// -- including if it was rejected.
	// error indicates something went wrong with the actual process of trying to validate
	Revalidate(channelID ChannelID, channel ChannelState) (ValidationResult, error)
}

// TransportConfigurer provides a mechanism to provide transport specific configuration for a given voucher type
type TransportConfigurer func(chid ChannelID, voucher Voucher, transport Transport)

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
	RegisterVoucherType(voucherType Voucher, validator RequestValidator) error

	// RegisterVoucherResultType allows deserialization of a voucher result,
	// so that a listener can read the metadata
	RegisterVoucherResultType(resultType VoucherResult) error

	// RegisterTransportConfigurer registers the given transport configurer to be run on requests with the given voucher
	// type
	RegisterTransportConfigurer(voucherType Voucher, configurer TransportConfigurer) error

	// open a data transfer that will send data to the recipient peer and
	// transfer parts of the piece that match the selector
	OpenPushDataChannel(ctx context.Context, to peer.ID, voucher Voucher, baseCid cid.Cid, selector ipld.Node) (ChannelID, error)

	// open a data transfer that will request data from the sending peer and
	// transfer parts of the piece that match the selector
	OpenPullDataChannel(ctx context.Context, to peer.ID, voucher Voucher, baseCid cid.Cid, selector ipld.Node) (ChannelID, error)

	// send an intermediate voucher as needed when the receiver sends a request for revalidation
	SendVoucher(ctx context.Context, chid ChannelID, voucher Voucher) error

	// send information from the responder to update the initiator on the state of their voucher
	SendVoucherResult(ctx context.Context, chid ChannelID, voucher VoucherResult) error

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
