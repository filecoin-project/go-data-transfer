package impl

import (
	"github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

type channelEnvironment struct {
	m *manager
}

func (ce *channelEnvironment) ID() peer.ID {
	return ce.m.peerID
}

func (ce *channelEnvironment) CleanupChannel(chid datatransfer.ChannelID) {
	ce.m.transport.CleanupChannel(chid)
	ce.m.spansIndex.EndChannelSpan(chid)
}
