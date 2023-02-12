package impl

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/libp2p/go-libp2p/core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
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
func (m *manager) newRequest(ctx context.Context, selector datamodel.Node, isPull bool, voucher datatransfer.TypedVoucher, baseCid cid.Cid, to peer.ID) (datatransfer.Request, error) {
	// Generate a new transfer ID for the request
	tid := datatransfer.TransferID(m.transferIDGen.next())
	return message.NewRequest(tid, false, isPull, &voucher, baseCid, selector)
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
