package impl

import (
	"context"
	"time"

	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/message"
)

type receiver struct {
	manager *manager
}

// ReceiveRequest takes an incoming data transfer request, validates the voucher and
// processes the message.
func (r *receiver) ReceiveRequest(
	ctx context.Context,
	initiator peer.ID,
	incoming message.DataTransferRequest) {
	response, err := r.manager.receiveRequest(initiator, incoming)
	if err != nil {
		log.Error(err)
		return
	}
	if response.Accepted() && !incoming.IsPull() {
		stor, _ := incoming.Selector()
		r.manager.transport.OpenChannel(ctx, initiator, datatransfer.ChannelID{Initiator: initiator, ID: incoming.TransferID()}, cidlink.Link{Cid: incoming.BaseCid()}, stor, response)
		return
	}
	err = r.manager.dataTransferNetwork.SendMessage(ctx, initiator, response)
	if err != nil {
		log.Error(err)
	}
}

// ReceiveResponse handles responses to our  Push or Pull data transfer request.
// It schedules a transfer only if our Pull Request is accepted.
func (r *receiver) ReceiveResponse(
	ctx context.Context,
	sender peer.ID,
	incoming message.DataTransferResponse) {
	evt := datatransfer.Event{
		Code:      datatransfer.Error,
		Message:   "",
		Timestamp: time.Now(),
	}
	chid := datatransfer.ChannelID{Initiator: r.manager.peerID, ID: incoming.TransferID()}
	chst, err := r.manager.channels.GetByID(chid)
	if err != nil {
		log.Warnf("received response from unknown peer %s, transfer ID %d", sender, incoming.TransferID)
		return
	}

	if incoming.Accepted() {
		evt.Code = datatransfer.Progress
		// if we are handling a response to a pull request then they are sending data and the
		// initiator is us
		if chst.Sender() == sender {
			baseCid := chst.BaseCID()
			root := cidlink.Link{Cid: baseCid}
			request, err := message.NewRequest(chid.ID, true, chst.Voucher().Type(), chst.Voucher(), baseCid, chst.Selector())
			if err == nil {
				r.manager.transport.OpenChannel(ctx, sender, chid, root, chst.Selector(), request)
			}
		}
	}
	err = r.manager.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
}

func (r *receiver) ReceiveError(err error) {
	log.Errorf("received error message on data transfer: %s", err.Error())
}
