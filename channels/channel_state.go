package channels

import (
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	peer "github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels/internal"
)

// channelState is immutable channel data plus mutable state
type channelState struct {
	ic internal.ChannelState
}

// EmptyChannelState is the zero value for channel state, meaning not present
var EmptyChannelState = channelState{}

// Status is the current status of this channel
func (c channelState) Status() datatransfer.Status { return c.ic.Status }

// Received returns the number of bytes received
func (c channelState) Queued() uint64 { return c.ic.Queued }

// Sent returns the number of bytes sent
func (c channelState) Sent() uint64 { return c.ic.Sent }

// Received returns the number of bytes received
func (c channelState) Received() uint64 { return c.ic.Received }

// TransferID returns the transfer id for this channel
func (c channelState) TransferID() datatransfer.TransferID { return c.ic.TransferID }

// BaseCID returns the CID that is at the root of this data transfer
func (c channelState) BaseCID() cid.Cid { return c.ic.BaseCid }

// Selector returns the IPLD selector for this data transfer (represented as
// an IPLD node)
func (c channelState) Selector() datamodel.Node {
	return c.ic.Selector.Node
}

// Voucher returns the voucher for this data transfer
func (c channelState) Voucher() datatransfer.TypedVoucher {
	if len(c.ic.Vouchers) == 0 {
		return datatransfer.TypedVoucher{}
	}
	ev := c.ic.Vouchers[0]
	return datatransfer.TypedVoucher{Voucher: ev.Voucher.Node, Type: ev.Type}
}

// ReceivedIndex returns the index, a transport specific identifier for "where"
// we are in receiving data for a transfer
func (c channelState) ReceivedIndex() datamodel.Node {
	return c.ic.ReceivedIndex.Node
}

// QueuedIndex returns the index, a transport specific identifier for "where"
// we are in queing data for a transfer
func (c channelState) QueuedIndex() datamodel.Node {
	return c.ic.QueuedIndex.Node
}

// SentIndex returns the index, a transport specific identifier for "where"
// we are in sending data for a transfer
func (c channelState) SentIndex() datamodel.Node {
	return c.ic.SentIndex.Node
}

// Sender returns the peer id for the node that is sending data
func (c channelState) Sender() peer.ID { return c.ic.Sender }

// Recipient returns the peer id for the node that is receiving data
func (c channelState) Recipient() peer.ID { return c.ic.Recipient }

// TotalSize returns the total size for the data being transferred
func (c channelState) TotalSize() uint64 { return c.ic.TotalSize }

// IsPull returns whether this is a pull request based on who initiated it
func (c channelState) IsPull() bool {
	return c.ic.Initiator == c.ic.Recipient
}

func (c channelState) ChannelID() datatransfer.ChannelID {
	if c.IsPull() {
		return datatransfer.ChannelID{ID: c.ic.TransferID, Initiator: c.ic.Recipient, Responder: c.ic.Sender}
	}
	return datatransfer.ChannelID{ID: c.ic.TransferID, Initiator: c.ic.Sender, Responder: c.ic.Recipient}
}

func (c channelState) Message() string {
	return c.ic.Message
}

func (c channelState) Vouchers() []datatransfer.TypedVoucher {
	vouchers := make([]datatransfer.TypedVoucher, 0, len(c.ic.Vouchers))
	for _, encoded := range c.ic.Vouchers {
		vouchers = append(vouchers, datatransfer.TypedVoucher{Voucher: encoded.Voucher.Node, Type: encoded.Type})
	}
	return vouchers
}

func (c channelState) LastVoucher() datatransfer.TypedVoucher {
	ev := c.ic.Vouchers[len(c.ic.Vouchers)-1]

	return datatransfer.TypedVoucher{Voucher: ev.Voucher.Node, Type: ev.Type}
}

func (c channelState) LastVoucherResult() datatransfer.TypedVoucher {
	evr := c.ic.VoucherResults[len(c.ic.VoucherResults)-1]
	return datatransfer.TypedVoucher{Voucher: evr.VoucherResult.Node, Type: evr.Type}
}

func (c channelState) VoucherResults() []datatransfer.TypedVoucher {
	voucherResults := make([]datatransfer.TypedVoucher, 0, len(c.ic.VoucherResults))
	for _, encoded := range c.ic.VoucherResults {
		voucherResults = append(voucherResults, datatransfer.TypedVoucher{Voucher: encoded.VoucherResult.Node, Type: encoded.Type})
	}
	return voucherResults
}

func (c channelState) SelfPeer() peer.ID {
	return c.ic.SelfPeer
}

func (c channelState) OtherPeer() peer.ID {
	if c.ic.Sender == c.ic.SelfPeer {
		return c.ic.Recipient
	}
	return c.ic.Sender
}

func (c channelState) DataLimit() uint64 {
	return c.ic.DataLimit
}

func (c channelState) RequiresFinalization() bool {
	return c.ic.RequiresFinalization
}

func (c channelState) InitiatorPaused() bool {
	return c.ic.InitiatorPaused
}

func (c channelState) ResponderPaused() bool {
	return c.ic.ResponderPaused
}

func (c channelState) BothPaused() bool {
	return c.InitiatorPaused() && c.ResponderPaused()
}

func (c channelState) SelfPaused() bool {
	if c.ic.SelfPeer == c.ic.Initiator {
		return c.InitiatorPaused()
	}
	return c.ResponderPaused()
}

func (c channelState) TransferClosed() bool {
	return c.ic.TransferClosed
}

func (c channelState) ExceededDataLimit() bool {
	var limitFactor uint64
	if c.ic.SelfPeer == c.ic.Sender {
		limitFactor = c.ic.Queued
	} else {
		limitFactor = c.ic.Received
	}
	return c.ic.DataLimit != 0 && limitFactor >= c.ic.DataLimit
}

func (c channelState) AwaitingFinalization() bool {
	return c.Status().InFinalization() && c.ic.RequiresFinalization
}

func (c channelState) TransferOnHold() bool {
	return c.SelfPaused() || c.AwaitingFinalization() || c.ExceededDataLimit()
}

// Stages returns the current ChannelStages object, or an empty object.
// It is unsafe for the caller to modify the return value, and changes may not
// be persisted. It should be treated as immutable.
//
// EXPERIMENTAL; subject to change.
func (c channelState) Stages() *datatransfer.ChannelStages {
	if c.ic.Stages == nil {
		// return an empty placeholder; it will be discarded because the caller
		// is not supposed to mutate the value anyway.
		return &datatransfer.ChannelStages{}
	}

	return c.ic.Stages
}

func fromInternalChannelState(c internal.ChannelState) datatransfer.ChannelState {
	return channelState{ic: c}
}

var _ datatransfer.ChannelState = channelState{}
