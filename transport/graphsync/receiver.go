package graphsync

import (
	"context"

	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

type receiver struct {
	transport *Transport
}

// ReceiveRequest takes an incoming data transfer request, validates the voucher and
// processes the message.
func (r *receiver) ReceiveRequest(
	ctx context.Context,
	initiator peer.ID,
	incoming datatransfer.Request) {
	err := r.receiveRequest(ctx, initiator, incoming)
	if err != nil {
		log.Warnf("error processing request from %s: %s", initiator, err)
	}
}

func (r *receiver) receiveRequest(ctx context.Context, initiator peer.ID, incoming datatransfer.Request) error {
	chid := datatransfer.ChannelID{Initiator: initiator, Responder: r.transport.peerID, ID: incoming.TransferID()}
	ctxAugment := r.transport.events.OnContextAugment(chid)
	if ctxAugment != nil {
		ctx = ctxAugment(ctx)
	}
	ctx, span := otel.Tracer("gs-data-transfer").Start(ctx, "receiveRequest", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
		attribute.String("baseCid", incoming.BaseCid().String()),
		attribute.Bool("isNew", incoming.IsNew()),
		attribute.Bool("isRestart", incoming.IsRestart()),
		attribute.Bool("isUpdate", incoming.IsUpdate()),
		attribute.Bool("isCancel", incoming.IsCancel()),
		attribute.Bool("isPaused", incoming.IsPaused()),
	))
	defer span.End()
	isNewOrRestart := incoming.IsNew() || incoming.IsRestart()
	// a graphsync pull request MUST come in via graphsync
	if isNewOrRestart && incoming.IsPull() {
		return datatransfer.ErrUnsupported
	}
	log.Debugf("received request for channel: %s", chid)
	response, receiveErr := r.transport.events.OnRequestReceived(chid, incoming)
	initiateGraphsyncRequest := isNewOrRestart && response != nil && receiveErr == nil
	ch, err := r.transport.getDTChannel(chid)
	if err != nil {
		if !initiateGraphsyncRequest || receiveErr != nil {
			if response != nil {
				if sendErr := r.transport.dtNet.SendMessage(ctx, initiator, transportID, response); sendErr != nil {
					return sendErr
				}
				return receiveErr
			}
			return receiveErr
		}
		ch = r.transport.trackDTChannel(chid)
	}

	if receiveErr != nil {
		if response != nil {
			if err := r.transport.dtNet.SendMessage(ctx, initiator, transportID, response); err != nil {
				return err
			}
			_ = ch.Close(ctx)
			return receiveErr
		}
	}

	if isNewOrRestart {
		r.transport.dtNet.Protect(initiator, chid.String())
	}
	chst, err := r.transport.events.ChannelState(ctx, chid)
	if err != nil {
		return err
	}

	err = ch.UpdateFromChannelState(chst)
	if err != nil {
		return err
	}

	if initiateGraphsyncRequest {
		stor, _ := incoming.Selector()
		if err := r.transport.openRequest(ctx, initiator, chid, cidlink.Link{Cid: incoming.BaseCid()}, stor, response); err != nil {
			return err
		}
		response = nil
	}

	action := ch.ActionFromChannelState(chst)
	return r.transport.processAction(ctx, chid, ch, action, response)
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
	chid := datatransfer.ChannelID{Initiator: r.transport.peerID, Responder: sender, ID: incoming.TransferID()}
	ctx = r.transport.events.OnContextAugment(chid)(ctx)
	ctx, span := otel.Tracer("gs-data-transfer").Start(ctx, "receiveResponse", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
		attribute.Bool("accepted", incoming.Accepted()),
		attribute.Bool("isComplete", incoming.IsComplete()),
		attribute.Bool("isNew", incoming.IsNew()),
		attribute.Bool("isRestart", incoming.IsRestart()),
		attribute.Bool("isUpdate", incoming.IsUpdate()),
		attribute.Bool("isCancel", incoming.IsCancel()),
		attribute.Bool("isPaused", incoming.IsPaused()),
	))
	defer span.End()
	receiveErr := r.transport.events.OnResponseReceived(chid, incoming)
	ch, err := r.transport.getDTChannel(chid)
	if err != nil {
		return err
	}
	if receiveErr != nil {
		log.Warnf("closing channel %s after getting error processing response from %s: %s",
			chid, sender, err)

		_ = ch.Close(ctx)
		return receiveErr
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
		log.Errorf("cannot restart channel: failed to fetch channel Id: %w", err)
		return
	}

	ctx = r.transport.events.OnContextAugment(ch)(ctx)
	ctx, span := otel.Tracer("gs-data-transfer").Start(ctx, "receiveRequest", trace.WithAttributes(
		attribute.String("channelID", ch.String()),
	))
	defer span.End()
	log.Infof("channel %s: received restart existing channel request from %s", ch, sender)

	// initiator should be me
	if ch.Initiator != r.transport.peerID {
		log.Errorf("cannot restart channel %s: channel initiator is not the manager peer", ch)
		return
	}

	if ch.Responder != sender {
		log.Errorf("cannot restart channel %s: channel counterparty is not the sender peer", ch)
		return
	}

	r.transport.events.OnTransportEvent(ch, datatransfer.TransportReceivedRestartExistingChannelRequest{})
	return
}
