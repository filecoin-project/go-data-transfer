package migrations

import (
	"github.com/ipfs/go-cid"
	peer "github.com/libp2p/go-libp2p-core/peer"

	versioning "github.com/filecoin-project/go-ds-versioning/pkg"
	"github.com/filecoin-project/go-ds-versioning/pkg/versioned"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels/internal"
)

//go:generate cbor-gen-for --map-encoding ChannelStateV2

// ChannelStateV2 is the internal representation on disk for the channel fsm, version 2
type ChannelStateV2 struct {
	// PeerId of the manager peer
	SelfPeer peer.ID
	// an identifier for this channel shared by request and responder, set by requester through protocol
	TransferID datatransfer.TransferID
	// Initiator is the person who intiated this datatransfer request
	Initiator peer.ID
	// Responder is the person who is responding to this datatransfer request
	Responder peer.ID
	// base CID for the piece being transferred
	BaseCid cid.Cid
	// portion of Piece to return, specified by an IPLD selector
	Selector internal.CborGenCompatibleNode
	// the party that is sending the data (not who initiated the request)
	Sender peer.ID
	// the party that is receiving the data (not who initiated the request)
	Recipient peer.ID
	// expected amount of data to be transferred
	TotalSize uint64
	// current status of this deal
	Status datatransfer.Status
	// total bytes read from this node and queued for sending (0 if receiver)
	Queued uint64
	// total bytes sent from this node (0 if receiver)
	Sent uint64
	// total bytes received by this node (0 if sender)
	Received uint64
	// more informative status on a channel
	Message        string
	Vouchers       []internal.EncodedVoucher
	VoucherResults []internal.EncodedVoucherResult
	// Number of blocks that have been received, including blocks that are
	// present in more than one place in the DAG
	ReceivedBlocksTotal int64
	// Number of blocks that have been queued, including blocks that are
	// present in more than one place in the DAG
	QueuedBlocksTotal int64
	// Number of blocks that have been sent, including blocks that are
	// present in more than one place in the DAG
	SentBlocksTotal int64
	// DataLimit is the maximum data that can be transferred on this channel before
	// revalidation. 0 indicates no limit.
	DataLimit uint64
	// RequiresFinalization indicates at the end of the transfer, the channel should
	// be left open for a final settlement
	RequiresFinalization bool
	// Stages traces the execution fo a data transfer.
	//
	// EXPERIMENTAL; subject to change.
	Stages *datatransfer.ChannelStages
}

func NoOpChannelState0To2(oldChannelState *ChannelStateV2) (*ChannelStateV2, error) {
	return oldChannelState, nil
}

func MigrateChannelState2To3(oldChannelState *ChannelStateV2) (*internal.ChannelState, error) {
	responderPaused := oldChannelState.Status == datatransfer.ResponderPaused || oldChannelState.Status == datatransfer.BothPaused
	initiatorPaused := oldChannelState.Status == datatransfer.InitiatorPaused || oldChannelState.Status == datatransfer.BothPaused
	newStatus := oldChannelState.Status
	if newStatus == datatransfer.ResponderPaused || newStatus == datatransfer.InitiatorPaused || newStatus == datatransfer.BothPaused {
		newStatus = datatransfer.Ongoing
	}
	return &internal.ChannelState{
		SelfPeer:             oldChannelState.SelfPeer,
		TransferID:           oldChannelState.TransferID,
		Initiator:            oldChannelState.Initiator,
		Responder:            oldChannelState.Responder,
		BaseCid:              oldChannelState.BaseCid,
		Selector:             oldChannelState.Selector,
		Sender:               oldChannelState.Sender,
		Recipient:            oldChannelState.Recipient,
		TotalSize:            oldChannelState.TotalSize,
		Status:               newStatus,
		Queued:               oldChannelState.Queued,
		Sent:                 oldChannelState.Sent,
		Received:             oldChannelState.Received,
		Message:              oldChannelState.Message,
		Vouchers:             oldChannelState.Vouchers,
		VoucherResults:       oldChannelState.VoucherResults,
		ReceivedBlocksTotal:  oldChannelState.ReceivedBlocksTotal,
		SentBlocksTotal:      oldChannelState.SentBlocksTotal,
		QueuedBlocksTotal:    oldChannelState.QueuedBlocksTotal,
		DataLimit:            oldChannelState.DataLimit,
		RequiresFinalization: oldChannelState.RequiresFinalization,
		InitiatorPaused:      initiatorPaused,
		ResponderPaused:      responderPaused,
		Stages:               oldChannelState.Stages,
	}, nil
}

// GetChannelStateMigrations returns a migration list for the channel states
func GetChannelStateMigrations(selfPeer peer.ID) (versioning.VersionedMigrationList, error) {
	return versioned.BuilderList{
		versioned.NewVersionedBuilder(NoOpChannelState0To2, "2"),
		versioned.NewVersionedBuilder(MigrateChannelState2To3, "3").OldVersion("2"),
	}.Build()
}
