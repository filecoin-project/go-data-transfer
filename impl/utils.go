package impl

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/message"
)

type statusList []datatransfer.Status

func (sl statusList) Contains(s datatransfer.Status) bool {
	for _, ts := range sl {
		if ts == s {
			return true
		}
	}
	return false
}

var resumeTransportStatesResponder = statusList{
	datatransfer.Requested,
	datatransfer.Ongoing,
	datatransfer.InitiatorPaused,
}

// newRequest encapsulates message creation
func (m *manager) newRequest(ctx context.Context, selector ipld.Node, isPull bool, voucherType datatransfer.TypeIdentifier, voucher datatransfer.Voucher, baseCid cid.Cid, to peer.ID) (datatransfer.Request, error) {
	// Generate a new transfer ID for the request
	tid := datatransfer.TransferID(m.transferIDGen.next())
	return message.NewRequest(tid, false, isPull, voucherType, voucher, baseCid, selector)
}

func (m *manager) response(isRestart bool, isNew bool, err error, tid datatransfer.TransferID, voucherResultType datatransfer.TypeIdentifier, voucherResult datatransfer.VoucherResult) (datatransfer.Response, error) {
	isAccepted := err == nil || err == datatransfer.ErrPause || err == datatransfer.ErrResume
	isPaused := err == datatransfer.ErrPause
	resultType := datatransfer.EmptyTypeIdentifier
	if voucherResult != nil {
		resultType = voucherResultType
	}
	if isRestart {
		return message.RestartResponse(tid, isAccepted, isPaused, resultType, voucherResult)
	}

	if isNew {
		return message.NewResponse(tid, isAccepted, isPaused, resultType, voucherResult)
	}
	return message.VoucherResultResponse(tid, isAccepted, isPaused, resultType, voucherResult)
}

func (m *manager) completeResponse(err error, tid datatransfer.TransferID, voucherResultType datatransfer.TypeIdentifier, voucherResult datatransfer.VoucherResult) (datatransfer.Response, error) {
	isAccepted := err == nil || err == datatransfer.ErrPause || err == datatransfer.ErrResume
	isPaused := err == datatransfer.ErrPause
	resultType := datatransfer.EmptyTypeIdentifier
	if voucherResult != nil {
		resultType = voucherResultType
	}
	return message.CompleteResponse(tid, isAccepted, isPaused, resultType, voucherResult)
}

func (m *manager) resume(chid datatransfer.ChannelID) error {
	if chid.Initiator == m.peerID {
		return m.channels.ResumeInitiator(chid)
	}
	return m.channels.ResumeResponder(chid)
}

func (m *manager) pause(chid datatransfer.ChannelID) error {
	if chid.Initiator == m.peerID {
		return m.channels.PauseInitiator(chid)
	}
	return m.channels.PauseResponder(chid)
}

func (m *manager) resumeOther(chid datatransfer.ChannelID) error {
	if chid.Responder == m.peerID {
		return m.channels.ResumeInitiator(chid)
	}
	return m.channels.ResumeResponder(chid)
}

func (m *manager) pauseOther(chid datatransfer.ChannelID) error {
	if chid.Responder == m.peerID {
		return m.channels.PauseInitiator(chid)
	}
	return m.channels.PauseResponder(chid)
}

func (m *manager) resumeMessage(chid datatransfer.ChannelID) datatransfer.Message {
	if chid.Initiator == m.peerID {
		return message.UpdateRequest(chid.ID, false)
	}
	return message.UpdateResponse(chid.ID, false)
}

func (m *manager) pauseMessage(chid datatransfer.ChannelID) datatransfer.Message {
	if chid.Initiator == m.peerID {
		return message.UpdateRequest(chid.ID, true)
	}
	return message.UpdateResponse(chid.ID, true)
}

func (m *manager) cancelMessage(chid datatransfer.ChannelID) datatransfer.Message {
	if chid.Initiator == m.peerID {
		return message.CancelRequest(chid.ID)
	}
	return message.CancelResponse(chid.ID)
}
