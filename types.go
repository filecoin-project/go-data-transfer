package datatransfer

import (
	"fmt"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/libp2p/go-libp2p-core/peer"
	cbg "github.com/whyrusleeping/cbor-gen"
)

//go:generate cbor-gen-for ChannelID ChannelStages ChannelStage Log

// TypeIdentifier is a unique string identifier for a type of encodable object in a
// registry
type TypeIdentifier string

// EmptyTypeIdentifier means there is no voucher present
const EmptyTypeIdentifier = TypeIdentifier("")

// TypedVoucher is a voucher or voucher result in IPLD form and an associated
// type identifier for that voucher or voucher result
type TypedVoucher struct {
	Voucher datamodel.Node
	Type    TypeIdentifier
}

// Equals is a utility to compare that two TypedVouchers are the same - both type
// and the voucher's IPLD content
func (tv1 TypedVoucher) Equals(tv2 TypedVoucher) bool {
	return tv1.Type == tv2.Type && ipld.DeepEqual(tv1.Voucher, tv2.Voucher)
}

// TransferID is an identifier for a data transfer, shared between
// request/responder and unique to the requester
type TransferID uint64

// ChannelID is a unique identifier for a channel, distinct by both the other
// party's peer ID + the transfer ID
type ChannelID struct {
	Initiator peer.ID
	Responder peer.ID
	ID        TransferID
}

func (c ChannelID) String() string {
	return fmt.Sprintf("%s-%s-%d", c.Initiator, c.Responder, c.ID)
}

// OtherParty returns the peer on the other side of the request, depending
// on whether this peer is the initiator or responder
func (c ChannelID) OtherParty(thisPeer peer.ID) peer.ID {
	if thisPeer == c.Initiator {
		return c.Responder
	}
	return c.Initiator
}

// Channel represents all the parameters for a single data transfer
type Channel interface {
	// TransferID returns the transfer id for this channel
	TransferID() TransferID

	// BaseCID returns the CID that is at the root of this data transfer
	BaseCID() cid.Cid

	// Selector returns the IPLD selector for this data transfer (represented as
	// an IPLD node)
	Selector() datamodel.Node

	// Voucher returns the initial voucher for this data transfer
	Voucher() TypedVoucher

	// Sender returns the peer id for the node that is sending data
	Sender() peer.ID

	// Recipient returns the peer id for the node that is receiving data
	Recipient() peer.ID

	// TotalSize returns the total size for the data being transferred
	TotalSize() uint64

	// IsPull returns whether this is a pull request
	IsPull() bool

	// ChannelID returns the ChannelID for this request
	ChannelID() ChannelID

	// OtherPeer returns the counter party peer for this channel
	OtherPeer() peer.ID
}

// ChannelState is channel parameters plus it's current state
type ChannelState interface {
	Channel

	// SelfPeer returns the peer this channel belongs to
	SelfPeer() peer.ID

	// Status is the current status of this channel
	Status() Status

	// Sent returns the number of bytes sent
	Sent() uint64

	// Received returns the number of bytes received
	Received() uint64

	// Message offers additional information about the current status
	Message() string

	// Vouchers returns all vouchers sent on this channel
	Vouchers() []TypedVoucher

	// VoucherResults are results of vouchers sent on the channel
	VoucherResults() []TypedVoucher

	// LastVoucher returns the last voucher sent on the channel
	LastVoucher() TypedVoucher

	// LastVoucherResult returns the last voucher result sent on the channel
	LastVoucherResult() TypedVoucher

	// ReceivedIndex returns the index, a transport specific identifier for "where"
	// we are in receiving data for a transfer
	ReceivedIndex() datamodel.Node

	// QueuedIndex returns the index, a transport specific identifier for "where"
	// we are in queing data for a transfer
	QueuedIndex() datamodel.Node

	// SentIndex returns the index, a transport specific identifier for "where"
	// we are in sending data for a transfer
	SentIndex() datamodel.Node

	// Queued returns the number of bytes read from the node and queued for sending
	Queued() uint64

	// DataLimit is the maximum data that can be transferred on this channel before
	// revalidation. 0 indicates no limit.
	DataLimit() uint64

	// RequiresFinalization indicates at the end of the transfer, the channel should
	// be left open for a final settlement
	RequiresFinalization() bool

	// InitiatorPaused indicates whether the initiator of this channel is in a paused state
	InitiatorPaused() bool

	// ResponderPaused indicates whether the responder of this channel is in a paused state
	ResponderPaused() bool

	// BothPaused indicates both sides of the transfer have paused the transfer
	BothPaused() bool

	// SelfPaused indicates whether the local peer for this channel is in a paused state
	SelfPaused() bool

	// Stages returns the timeline of events this data transfer has gone through,
	// for observability purposes.
	//
	// It is unsafe for the caller to modify the return value, and changes
	// may not be persisted. It should be treated as immutable.
	Stages() *ChannelStages
}

// ChannelStages captures a timeline of the progress of a data transfer channel,
// grouped by stages.
//
// EXPERIMENTAL; subject to change.
type ChannelStages struct {
	// Stages contains an entry for every stage the channel has gone through.
	// Each stage then contains logs.
	Stages []*ChannelStage
}

// ChannelStage traces the execution of a data transfer channel stage.
//
// EXPERIMENTAL; subject to change.
type ChannelStage struct {
	// Human-readable fields.
	// TODO: these _will_ need to be converted to canonical representations, so
	//  they are machine readable.
	Name        string
	Description string

	// Timestamps.
	// TODO: may be worth adding an exit timestamp. It _could_ be inferred from
	//  the start of the next stage, or from the timestamp of the last log line
	//  if this is a terminal stage. But that's non-determistic and it relies on
	//  assumptions.
	CreatedTime cbg.CborTime
	UpdatedTime cbg.CborTime

	// Logs contains a detailed timeline of events that occurred inside
	// this stage.
	Logs []*Log
}

// Log represents a point-in-time event that occurred inside a channel stage.
//
// EXPERIMENTAL; subject to change.
type Log struct {
	// Log is a human readable message.
	//
	// TODO: this _may_ need to be converted to a canonical data model so it
	//  is machine-readable.
	Log string

	UpdatedTime cbg.CborTime
}

// AddLog adds a log to the specified stage, creating the stage if
// it doesn't exist yet.
//
// EXPERIMENTAL; subject to change.
func (cs *ChannelStages) AddLog(stage, msg string) {
	if cs == nil {
		return
	}

	now := curTime()
	st := cs.GetStage(stage)
	if st == nil {
		st = &ChannelStage{
			CreatedTime: now,
		}
		cs.Stages = append(cs.Stages, st)
	}

	st.Name = stage
	st.UpdatedTime = now
	if msg != "" && (len(st.Logs) == 0 || st.Logs[len(st.Logs)-1].Log != msg) {
		// only add the log if it's not a duplicate.
		st.Logs = append(st.Logs, &Log{msg, now})
	}
}

// GetStage returns the ChannelStage object for a named stage, or nil if not found.
//
// TODO: the input should be a strongly-typed enum instead of a free-form string.
// TODO: drop Get from GetStage to make this code more idiomatic. Return a
//  second ok boolean to make it even more idiomatic.
//
// EXPERIMENTAL; subject to change.
func (cs *ChannelStages) GetStage(stage string) *ChannelStage {
	if cs == nil {
		return nil
	}

	for _, s := range cs.Stages {
		if s.Name == stage {
			return s
		}
	}

	return nil
}

func curTime() cbg.CborTime {
	now := time.Now()
	return cbg.CborTime(time.Unix(0, now.UnixNano()).UTC())
}
