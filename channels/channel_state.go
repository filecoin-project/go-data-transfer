package channels

import (
	"bytes"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	peer "github.com/libp2p/go-libp2p-core/peer"
	cbg "github.com/whyrusleeping/cbor-gen"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/channels/internal"
	"github.com/filecoin-project/go-data-transfer/ipldutil"
)

// channelState is immutable channel data plus mutable state
type channelState struct {
	// peerId of the manager peer
	selfPeer peer.ID
	// an identifier for this channel shared by request and responder, set by requester through protocol
	transferID datatransfer.TransferID
	// base CID for the piece being transferred
	baseCid cid.Cid
	// portion of Piece to return, specified by an IPLD selector
	selector *cbg.Deferred
	// the party that is sending the data (not who initiated the request)
	sender peer.ID
	// the party that is receiving the data (not who initiated the request)
	recipient peer.ID
	// expected amount of data to be transferred
	totalSize uint64
	// current status of this deal
	status datatransfer.Status
	// isPull indicates if this is a push or pull request
	isPull bool
	// total bytes read from this node and queued for sending (0 if receiver)
	queued uint64
	// total bytes sent from this node (0 if receiver)
	sent uint64
	// total bytes received by this node (0 if sender)
	received uint64
	// number of blocks that have been received, including blocks that are
	// present in more than one place in the DAG
	receivedBlocksTotal int64
	// Number of blocks that have been queued, including blocks that are
	// present in more than one place in the DAG
	queuedBlocksTotal int64
	// Number of blocks that have been sent, including blocks that are
	// present in more than one place in the DAG
	sentBlocksTotal int64
	// more informative status on a channel
	message string
	// additional vouchers
	vouchers []internal.EncodedVoucher
	// additional voucherResults
	voucherResults []internal.EncodedVoucherResult

	// stages tracks the timeline of events related to a data transfer, for
	// traceability purposes.
	stages *datatransfer.ChannelStages
}

// EmptyChannelState is the zero value for channel state, meaning not present
var EmptyChannelState = channelState{}

// Status is the current status of this channel
func (c channelState) Status() datatransfer.Status { return c.status }

// Received returns the number of bytes received
func (c channelState) Queued() uint64 { return c.queued }

// Sent returns the number of bytes sent
func (c channelState) Sent() uint64 { return c.sent }

// Received returns the number of bytes received
func (c channelState) Received() uint64 { return c.received }

// TransferID returns the transfer id for this channel
func (c channelState) TransferID() datatransfer.TransferID { return c.transferID }

// BaseCID returns the CID that is at the root of this data transfer
func (c channelState) BaseCID() cid.Cid { return c.baseCid }

// Selector returns the IPLD selector for this data transfer (represented as
// an IPLD node)
func (c channelState) Selector() ipld.Node {
	node, err := ipldutil.NodeFromDagCbor(bytes.NewReader(c.selector.Raw))
	if err != nil {
		log.Error(err)
	}
	return node
}

// Voucher returns the voucher for this data transfer
func (c channelState) Voucher() datatransfer.Voucher {
	if len(c.vouchers) == 0 {
		return nil
	}
	voucher, _ := ipldutil.NodeFromDagCbor(bytes.NewReader(c.vouchers[0].Voucher.Raw))
	return voucher
}

// VoucherType returns the type of voucher for this data transfer
func (c channelState) VoucherType() datatransfer.TypeIdentifier {
	if len(c.vouchers) == 0 {
		return ""
	}
	return c.vouchers[0].Type
}

// ReceivedCidsTotal returns the number of (non-unique) cids received so far
// on the channel - note that a block can exist in more than one place in the DAG
func (c channelState) ReceivedCidsTotal() int64 {
	return c.receivedBlocksTotal
}

// QueuedCidsTotal returns the number of (non-unique) cids queued so far
// on the channel - note that a block can exist in more than one place in the DAG
func (c channelState) QueuedCidsTotal() int64 {
	return c.queuedBlocksTotal
}

// SentCidsTotal returns the number of (non-unique) cids sent so far
// on the channel - note that a block can exist in more than one place in the DAG
func (c channelState) SentCidsTotal() int64 {
	return c.sentBlocksTotal
}

// Sender returns the peer id for the node that is sending data
func (c channelState) Sender() peer.ID { return c.sender }

// Recipient returns the peer id for the node that is receiving data
func (c channelState) Recipient() peer.ID { return c.recipient }

// TotalSize returns the total size for the data being transferred
func (c channelState) TotalSize() uint64 { return c.totalSize }

// IsPull returns whether this is a pull request based on who initiated it
func (c channelState) IsPull() bool {
	return c.isPull
}

func (c channelState) ChannelID() datatransfer.ChannelID {
	if c.isPull {
		return datatransfer.ChannelID{ID: c.transferID, Initiator: c.recipient, Responder: c.sender}
	}
	return datatransfer.ChannelID{ID: c.transferID, Initiator: c.sender, Responder: c.recipient}
}

func (c channelState) Message() string {
	return c.message
}

func (c channelState) Vouchers() []datatransfer.Voucher {
	vouchers := make([]datatransfer.Voucher, 0, len(c.vouchers))
	for _, encoded := range c.vouchers {
		voucher, _ := ipldutil.NodeFromDagCbor(bytes.NewReader(encoded.Voucher.Raw))
		vouchers = append(vouchers, voucher)
	}
	return vouchers
}

// LastVoucherType returns the type of the last voucher for this data transfer
func (c channelState) LastVoucherType() datatransfer.TypeIdentifier {
	if len(c.vouchers) == 0 {
		return ""
	}
	return c.vouchers[len(c.vouchers)-1].Type
}

func (c channelState) LastVoucher() datatransfer.Voucher {
	voucher, _ := ipldutil.NodeFromDagCbor(bytes.NewReader(c.vouchers[len(c.vouchers)-1].Voucher.Raw))
	return voucher
}

func (c channelState) LastVoucherResult() datatransfer.VoucherResult {
	voucher, _ := ipldutil.NodeFromDagCbor(bytes.NewReader(c.voucherResults[len(c.voucherResults)-1].VoucherResult.Raw))
	return voucher
}

func (c channelState) VoucherResults() []datatransfer.VoucherResult {
	voucherResults := make([]datatransfer.VoucherResult, 0, len(c.voucherResults))
	for _, encoded := range c.voucherResults {
		voucherResult, _ := ipldutil.NodeFromDagCbor(bytes.NewReader(encoded.VoucherResult.Raw))
		voucherResults = append(voucherResults, voucherResult)
	}
	return voucherResults
}

func (c channelState) SelfPeer() peer.ID {
	return c.selfPeer
}

func (c channelState) OtherPeer() peer.ID {
	if c.sender == c.selfPeer {
		return c.recipient
	}
	return c.sender
}

// Stages returns the current ChannelStages object, or an empty object.
// It is unsafe for the caller to modify the return value, and changes may not
// be persisted. It should be treated as immutable.
//
// EXPERIMENTAL; subject to change.
func (c channelState) Stages() *datatransfer.ChannelStages {
	if c.stages == nil {
		// return an empty placeholder; it will be discarded because the caller
		// is not supposed to mutate the value anyway.
		return &datatransfer.ChannelStages{}
	}

	return c.stages
}

func fromInternalChannelState(c internal.ChannelState) datatransfer.ChannelState {
	return channelState{
		selfPeer:            c.SelfPeer,
		isPull:              c.Initiator == c.Recipient,
		transferID:          c.TransferID,
		baseCid:             c.BaseCid,
		selector:            c.Selector,
		sender:              c.Sender,
		recipient:           c.Recipient,
		totalSize:           c.TotalSize,
		status:              c.Status,
		queued:              c.Queued,
		sent:                c.Sent,
		received:            c.Received,
		receivedBlocksTotal: c.ReceivedBlocksTotal,
		queuedBlocksTotal:   c.QueuedBlocksTotal,
		sentBlocksTotal:     c.SentBlocksTotal,
		message:             c.Message,
		vouchers:            c.Vouchers,
		voucherResults:      c.VoucherResults,
		stages:              c.Stages,
	}
}

var _ datatransfer.ChannelState = channelState{}
