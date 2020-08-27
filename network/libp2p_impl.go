package network

import (
	"context"
	"fmt"
	"io"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p-core/helpers"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/message"
)

var log = logging.Logger("data_transfer_network")

var sendMessageTimeout = time.Minute * 10

// NewFromLibp2pHost returns a GraphSyncNetwork supported by underlying Libp2p host.
func NewFromLibp2pHost(host host.Host) DataTransferNetwork {
	dataTransferNetwork := libp2pDataTransferNetwork{
		host: host,
	}

	return &dataTransferNetwork
}

// libp2pDataTransferNetwork transforms the libp2p host interface, which sends and receives
// NetMessage objects, into the graphsync network interface.
type libp2pDataTransferNetwork struct {
	host host.Host
	// inbound messages from the network are forwarded to the receiver
	receiver Receiver
}

func (dtnet *libp2pDataTransferNetwork) newStreamToPeer(ctx context.Context, p peer.ID) (network.Stream, error) {
	return dtnet.host.NewStream(ctx, p, ProtocolDataTransfer)
}

func (dtnet *libp2pDataTransferNetwork) SendMessage(
	ctx context.Context,
	p peer.ID,
	outgoing datatransfer.Message) error {

	s, err := dtnet.newStreamToPeer(ctx, p)
	if err != nil {
		return err
	}

	if err = msgToStream(ctx, s, outgoing); err != nil {
		if err2 := s.Reset(); err2 != nil {
			log.Error(err)
			return err2
		}
		return err
	}

	// TODO(https://github.com/libp2p/go-libp2p-net/issues/28): Avoid this goroutine.
	go helpers.AwaitEOF(s) // nolint: errcheck,gosec
	return s.Close()

}

func (dtnet *libp2pDataTransferNetwork) SetDelegate(r Receiver) {
	dtnet.receiver = r
	dtnet.host.SetStreamHandler(ProtocolDataTransfer, dtnet.handleNewStream)
}

func (dtnet *libp2pDataTransferNetwork) ConnectTo(ctx context.Context, p peer.ID) error {
	return dtnet.host.Connect(ctx, peer.AddrInfo{ID: p})
}

// handleNewStream receives a new stream from the network.
func (dtnet *libp2pDataTransferNetwork) handleNewStream(s network.Stream) {
	defer s.Close() // nolint: errcheck,gosec

	if dtnet.receiver == nil {
		s.Reset() // nolint: errcheck,gosec
		return
	}

	for {
		received, err := message.FromNet(s)
		if err != nil {
			if err != io.EOF {
				s.Reset() // nolint: errcheck,gosec
				go dtnet.receiver.ReceiveError(err)
				log.Debugf("graphsync net handleNewStream from %s error: %s", s.Conn().RemotePeer(), err)
			}
			return
		}

		p := s.Conn().RemotePeer()
		ctx := context.Background()
		log.Debugf("graphsync net handleNewStream from %s", s.Conn().RemotePeer())
		if received.IsRequest() {
			receivedRequest, ok := received.(datatransfer.Request)
			if ok {
				dtnet.receiver.ReceiveRequest(ctx, p, receivedRequest)
			}
		} else {
			receivedResponse, ok := received.(datatransfer.Response)
			if ok {
				dtnet.receiver.ReceiveResponse(ctx, p, receivedResponse)
			}
		}
	}
}

func (dtnet *libp2pDataTransferNetwork) ID() peer.ID {
	return dtnet.host.ID()
}

func (dtnet *libp2pDataTransferNetwork) Protect(id peer.ID, tag string) {
	dtnet.host.ConnManager().Protect(id, tag)
}

func (dtnet *libp2pDataTransferNetwork) Unprotect(id peer.ID, tag string) bool {
	return dtnet.host.ConnManager().Unprotect(id, tag)
}

func msgToStream(ctx context.Context, s network.Stream, msg datatransfer.Message) error {
	if msg.IsRequest() {
		log.Debugf("Outgoing request message for transfer ID: %d", msg.TransferID())
	}

	deadline := time.Now().Add(sendMessageTimeout)
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl
	}
	if err := s.SetWriteDeadline(deadline); err != nil {
		log.Warnf("error setting deadline: %s", err)
	}

	switch s.Protocol() {
	case ProtocolDataTransfer:
		if err := msg.ToNet(s); err != nil {
			log.Debugf("error: %s", err)
			return err
		}
	default:
		return fmt.Errorf("unrecognized protocol on remote: %s", s.Protocol())
	}

	if err := s.SetWriteDeadline(time.Time{}); err != nil {
		log.Warnf("error resetting deadline: %s", err)
	}
	return nil
}
