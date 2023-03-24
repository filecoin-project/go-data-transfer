package testnet

import (
	"github.com/libp2p/go-libp2p/core/peer"

	gsnet "github.com/filecoin-project/boost-graphsync/network"

	dtnet "github.com/filecoin-project/go-data-transfer/network"
)

// Network is an interface for generating graphsync network interfaces
// based on a test network.
type Network interface {
	Adapter() (peer.ID, gsnet.GraphSyncNetwork, dtnet.DataTransferNetwork)
	HasPeer(peer.ID) bool
}
