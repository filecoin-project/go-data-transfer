package network

import (
	"context"
	"errors"
	"strings"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

const (
	// ProtocolFilDataTransfer1_2 is the legacy filecoin data transfer protocol
	// that assumes a graphsync transport
	ProtocolFilDataTransfer1_2 protocol.ID = "/fil/datatransfer/1.2.0"
	// ProtocolDataTransfer1_2 is the protocol identifier for current data transfer
	// protocol which wraps transport information in the protocol
	ProtocolDataTransfer1_2 protocol.ID = "/datatransfer/1.2.0"
)

// MessageVersion extracts the message version from the full protocol
func MessageVersion(protocol protocol.ID) (datatransfer.MessageVersion, error) {
	protocolParts := strings.Split(string(protocol), "/")
	if len(protocolParts) == 0 {
		return datatransfer.MessageVersion{}, errors.New("no protocol to parse")
	}
	return datatransfer.MessageVersionFromString(protocolParts[len(protocolParts)-1])
}

// DataTransferNetwork provides network connectivity for GraphSync.
type DataTransferNetwork interface {
	Protect(id peer.ID, tag string)
	Unprotect(id peer.ID, tag string) bool

	// SendMessage sends a GraphSync message to a peer.
	SendMessage(
		context.Context,
		peer.ID,
		datatransfer.TransportID,
		datatransfer.Message) error

	// SetDelegate registers the Reciver to handle messages received from the
	// network.
	SetDelegate(datatransfer.TransportID, Receiver)

	// ConnectTo establishes a connection to the given peer
	ConnectTo(context.Context, peer.ID) error

	// ConnectWithRetry establishes a connection to the given peer, retrying if
	// necessary, and opens a stream on the data-transfer protocol to verify
	// the peer will accept messages on the protocol
	ConnectWithRetry(ctx context.Context, p peer.ID) error

	// ID returns the peer id of this libp2p host
	ID() peer.ID

	// Protocol returns the protocol version of the peer, connecting to
	// the peer if necessary
	Protocol(context.Context, peer.ID) (protocol.ID, error)
}

// Receiver is an interface for receiving messages from the GraphSyncNetwork.
type Receiver interface {
	ReceiveRequest(
		ctx context.Context,
		sender peer.ID,
		incoming datatransfer.Request)

	ReceiveResponse(
		ctx context.Context,
		sender peer.ID,
		incoming datatransfer.Response)

	ReceiveRestartExistingChannelRequest(ctx context.Context, sender peer.ID, incoming datatransfer.Request)
}
