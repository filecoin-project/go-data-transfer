package impl

import (
	"context"

	"github.com/filecoin-project/go-data-transfer/channels"
	"github.com/ipfs/go-cid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer"
)

type receiver struct {
	manager *manager
}

// ReceiveRequest takes an incoming data transfer request, validates the voucher and
// processes the message.
func (r *receiver) ReceiveRequest(
	ctx context.Context,
	initiator peer.ID,
	incoming datatransfer.Request) {
	err := r.receiveRequest(ctx, initiator, incoming)
	if err != nil {
		log.Warn(err)
	}
}

func (r *receiver) receiveRequest(ctx context.Context, initiator peer.ID, incoming datatransfer.Request) error {
	chid := datatransfer.ChannelID{Initiator: initiator, Responder: r.manager.peerID, ID: incoming.TransferID()}
	response, receiveErr := r.manager.OnRequestReceived(chid, incoming)

	if receiveErr == datatransfer.ErrResume {
		chst, err := r.manager.channels.GetByID(ctx, chid)
		if err != nil {
			return err
		}
		if resumeTransportStatesResponder.Contains(chst.Status()) {
			return r.manager.transport.(datatransfer.PauseableTransport).ResumeChannel(ctx, response, chid)
		}
		receiveErr = nil
	}

	if response != nil {
		if (response.IsNew() || response.IsRestart()) && response.Accepted() && !incoming.IsPull() {
			var doNotSendCids []cid.Cid
			if response.IsRestart() {
				channel, err := r.manager.channels.GetByID(ctx, chid)
				if err != nil {
					return err
				}
				doNotSendCids = channel.ReceivedCids()
			}

			stor, _ := incoming.Selector()
			if err := r.manager.transport.OpenChannel(ctx, initiator, chid, cidlink.Link{Cid: incoming.BaseCid()}, stor, doNotSendCids, response); err != nil {
				return err
			}
		} else {
			if err := r.manager.dataTransferNetwork.SendMessage(ctx, initiator, response); err != nil {
				return err
			}
		}
	}

	if receiveErr == datatransfer.ErrPause {
		return r.manager.transport.(datatransfer.PauseableTransport).PauseChannel(ctx, chid)
	}

	if receiveErr != nil {
		_ = r.manager.transport.CloseChannel(ctx, chid)
		return receiveErr
	}

	return nil
}

// ReceiveResponse handles responses to our  Push or Pull data transfer request.
// It schedules a transfer only if our Pull Request is accepted.
func (r *receiver) ReceiveResponse(
	ctx context.Context,
	sender peer.ID,
	incoming datatransfer.Response) {
	err := r.receiveResponse(ctx, sender, incoming)
	if err != nil {
		log.Error(err)
	}
}
func (r *receiver) receiveResponse(
	ctx context.Context,
	sender peer.ID,
	incoming datatransfer.Response) error {
	chid := datatransfer.ChannelID{Initiator: r.manager.peerID, Responder: sender, ID: incoming.TransferID()}
	err := r.manager.OnResponseReceived(chid, incoming)
	if err == datatransfer.ErrPause {
		return r.manager.transport.(datatransfer.PauseableTransport).PauseChannel(ctx, chid)
	}
	if err != nil {
		_ = r.manager.transport.CloseChannel(ctx, chid)
		return err
	}
	return nil
}

func (r *receiver) ReceiveError(err error) {
	log.Errorf("received error message on data transfer: %s", err.Error())
}

func (r *receiver) ReceiveRestartExistingChannelRequest(ctx context.Context,
	sender peer.ID,
	incoming datatransfer.Request) {

	ch, err := incoming.RestartChannelId()
	if err != nil {
		log.Error(err)
	}

	// validate channel exists -> in non-terminal state and that the sender matches
	channel, err := r.manager.channels.GetByID(ctx, ch)
	if err != nil {
		// nothing to do here, we wont handle the request
	}

	// initator should be me
	if channel.ChannelID().Initiator != r.manager.peerID {
		log.Error("channel initiator is not the manager peer")
	}

	// other peer should be the counter party on the channel
	if channel.OtherParty(r.manager.peerID) != sender {
		log.Error("channel counterpart is not the sender peer")
	}

	// channel should NOT be terminated
	if channels.IsChannelTerminated(channel.Status()) {
		log.Error("channel is already terminated")
	}

	switch r.manager.channelDataTransferType(channel) {
	case ManagerPeerCreatePush:
		r.manager.openPushRestartChannel(ctx, channel)
	case ManagerPeerCreatePull:
		r.manager.openPullRestartChannel(ctx, channel)
	default:
		log.Error("peer is not the creator of the channel")
	}
}
