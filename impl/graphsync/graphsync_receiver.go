package graphsyncimpl

import (
	"context"
	"time"

	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/message"
)

type graphsyncReceiver struct {
	impl *graphsyncImpl
}

// ReceiveRequest takes an incoming data transfer request, validates the voucher and
// processes the message.
func (receiver *graphsyncReceiver) ReceiveRequest(
	ctx context.Context,
	initiator peer.ID,
	incoming message.DataTransferRequest) {
	response, err := receiver.impl.receiveRequest(initiator, incoming)
	if err != nil {
		log.Error(err)
		return
	}
	if response.Accepted() && !incoming.IsPull() {
		stor, _ := incoming.Selector()
		receiver.impl.sendGsRequest(ctx, initiator, initiator, cidlink.Link{Cid: incoming.BaseCid()}, stor, response)
		return
	}
	err = receiver.impl.dataTransferNetwork.SendMessage(ctx, initiator, response)
	if err != nil {
		log.Error(err)
	}
}

// ReceiveResponse handles responses to our  Push or Pull data transfer request.
// It schedules a graphsync transfer only if our Pull Request is accepted.
func (receiver *graphsyncReceiver) ReceiveResponse(
	ctx context.Context,
	sender peer.ID,
	incoming message.DataTransferResponse) {
	evt := datatransfer.Event{
		Code:      datatransfer.Error,
		Message:   "",
		Timestamp: time.Now(),
	}
	chid := datatransfer.ChannelID{Initiator: receiver.impl.peerID, ID: incoming.TransferID()}
	chst, err := receiver.impl.channels.GetByID(chid)
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
				receiver.impl.sendGsRequest(ctx, receiver.impl.peerID, sender, root, chst.Selector(), request)
			}
		}
	}
	err = receiver.impl.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
}

func (receiver *graphsyncReceiver) ReceiveError(err error) {
	log.Errorf("received error message on data transfer: %s", err.Error())
}
