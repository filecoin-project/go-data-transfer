package channels

import (
	"bytes"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	peer "github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels/internal"
	ipldutils "github.com/filecoin-project/go-data-transfer/v2/ipldutils"
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
func (c channelState) Selector() ipld.Node {
	builder := basicnode.Prototype.Any.NewBuilder()
	reader := bytes.NewReader(c.ic.Selector.Raw)
	err := dagcbor.Decode(builder, reader)
	if err != nil {
		log.Error(err)
	}
	return builder.Build()
}

func (c channelState) VoucherType() datatransfer.TypeIdentifier {
	return c.ic.VoucherType
}

func (c channelState) VoucherResultType() datatransfer.TypeIdentifier {
	return c.ic.VoucherResultType
}

// Voucher returns the voucher for this data transfer
func (c channelState) Voucher() (ipld.Node, error) {
	if len(c.ic.Vouchers) == 0 {
		return nil, nil
	}
	return ipldutils.NodeFromBytes(c.ic.Vouchers[0].Raw)
}

// ReceivedCidsTotal returns the number of (non-unique) cids received so far
// on the channel - note that a block can exist in more than one place in the DAG
func (c channelState) ReceivedCidsTotal() int64 {
	return c.ic.ReceivedBlocksTotal
}

// QueuedCidsTotal returns the number of (non-unique) cids queued so far
// on the channel - note that a block can exist in more than one place in the DAG
func (c channelState) QueuedCidsTotal() int64 {
	return c.ic.QueuedBlocksTotal
}

// SentCidsTotal returns the number of (non-unique) cids sent so far
// on the channel - note that a block can exist in more than one place in the DAG
func (c channelState) SentCidsTotal() int64 {
	return c.ic.SentBlocksTotal
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

func (c channelState) Vouchers() ([]ipld.Node, error) {
	vouchers := make([]ipld.Node, 0, len(c.ic.Vouchers))
	for _, encoded := range c.ic.Vouchers {
		n, err := ipldutils.NodeFromBytes(encoded.Raw)
		if err != nil {
			return nil, err
		}
		vouchers = append(vouchers, n)
	}
	return vouchers, nil
}

func (c channelState) LastVoucher() (ipld.Node, error) {
	return ipldutils.NodeFromBytes(c.ic.Vouchers[len(c.ic.Vouchers)-1].Raw)
}

func (c channelState) LastVoucherResult() (ipld.Node, error) {
	return ipldutils.NodeFromBytes(c.ic.VoucherResults[len(c.ic.VoucherResults)-1].Raw)
}

func (c channelState) VoucherResults() ([]ipld.Node, error) {
	voucherResults := make([]ipld.Node, 0, len(c.ic.VoucherResults))
	for _, encoded := range c.ic.VoucherResults {
		n, err := ipldutils.NodeFromBytes(encoded.Raw)
		if err != nil {
			return nil, err
		}
		voucherResults = append(voucherResults, n)
	}
	return voucherResults, nil
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
