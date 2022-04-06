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
)

// channelState is immutable channel data plus mutable state
type channelState struct {
	ic internal.ChannelState

	// additional voucherResults
	voucherResultDecoder DecoderByTypeFunc
	voucherDecoder       DecoderByTypeFunc
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

// Voucher returns the voucher for this data transfer
func (c channelState) Voucher() datatransfer.Voucher {
	if len(c.ic.Vouchers) == 0 {
		return nil
	}
	decoder, _ := c.voucherDecoder(c.ic.Vouchers[0].Type)
	encodable, _ := decoder.DecodeFromCbor(c.ic.Vouchers[0].Voucher.Raw)
	return encodable.(datatransfer.Voucher)
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
	if c.ic.Initiator == c.ic.Recipient {
		return datatransfer.ChannelID{ID: c.ic.TransferID, Initiator: c.ic.Recipient, Responder: c.ic.Sender}
	}
	return datatransfer.ChannelID{ID: c.ic.TransferID, Initiator: c.ic.Sender, Responder: c.ic.Recipient}
}

func (c channelState) Message() string {
	return c.ic.Message
}

func (c channelState) Vouchers() []datatransfer.Voucher {
	vouchers := make([]datatransfer.Voucher, 0, len(c.ic.Vouchers))
	for _, encoded := range c.ic.Vouchers {
		decoder, _ := c.voucherDecoder(encoded.Type)
		encodable, _ := decoder.DecodeFromCbor(encoded.Voucher.Raw)
		vouchers = append(vouchers, encodable.(datatransfer.Voucher))
	}
	return vouchers
}

func (c channelState) LastVoucher() datatransfer.Voucher {
	decoder, _ := c.voucherDecoder(c.ic.Vouchers[len(c.ic.Vouchers)-1].Type)
	encodable, _ := decoder.DecodeFromCbor(c.ic.Vouchers[len(c.ic.Vouchers)-1].Voucher.Raw)
	return encodable.(datatransfer.Voucher)
}

func (c channelState) LastVoucherResult() datatransfer.VoucherResult {
	decoder, _ := c.voucherResultDecoder(c.ic.VoucherResults[len(c.ic.VoucherResults)-1].Type)
	encodable, _ := decoder.DecodeFromCbor(c.ic.VoucherResults[len(c.ic.VoucherResults)-1].VoucherResult.Raw)
	return encodable.(datatransfer.VoucherResult)
}

func (c channelState) VoucherResults() []datatransfer.VoucherResult {
	voucherResults := make([]datatransfer.VoucherResult, 0, len(c.ic.VoucherResults))
	for _, encoded := range c.ic.VoucherResults {
		decoder, _ := c.voucherResultDecoder(encoded.Type)
		encodable, _ := decoder.DecodeFromCbor(encoded.VoucherResult.Raw)
		voucherResults = append(voucherResults, encodable.(datatransfer.VoucherResult))
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

func (c channelState) RevalidateToComplete() bool {
	return c.ic.RevalidateToComplete
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

func fromInternalChannelState(c internal.ChannelState, voucherDecoder DecoderByTypeFunc, voucherResultDecoder DecoderByTypeFunc) datatransfer.ChannelState {
	return channelState{
		ic:                   c,
		voucherResultDecoder: voucherResultDecoder,
		voucherDecoder:       voucherDecoder,
	}
}

var _ datatransfer.ChannelState = channelState{}
