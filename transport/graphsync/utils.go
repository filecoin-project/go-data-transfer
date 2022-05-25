package graphsync

import (
	"sync"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/dtchannel"
	"github.com/ipfs/go-graphsync"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"
)

func (t *Transport) trackDTChannel(chid datatransfer.ChannelID) *dtchannel.Channel {
	t.dtChannelsLk.Lock()
	defer t.dtChannelsLk.Unlock()

	ch, ok := t.dtChannels[chid]
	if !ok {
		ch = dtchannel.NewChannel(chid, t.gs)
		t.dtChannels[chid] = ch
	}

	return ch
}

func (t *Transport) getDTChannel(chid datatransfer.ChannelID) (*dtchannel.Channel, error) {
	if t.events == nil {
		return nil, datatransfer.ErrHandlerNotSet
	}

	t.dtChannelsLk.RLock()
	defer t.dtChannelsLk.RUnlock()

	ch, ok := t.dtChannels[chid]
	if !ok {
		return nil, xerrors.Errorf("channel %s: %w", chid, datatransfer.ErrChannelNotFound)
	}
	return ch, nil
}

func (t *Transport) otherPeer(chid datatransfer.ChannelID) peer.ID {
	if chid.Initiator == t.peerID {
		return chid.Responder
	}
	return chid.Initiator
}

type channelInfo struct {
	sending   bool
	channelID datatransfer.ChannelID
}

// Used in graphsync callbacks to map from graphsync request to the
// associated data-transfer channel ID.
type requestIDToChannelIDMap struct {
	lk sync.RWMutex
	m  map[graphsync.RequestID]channelInfo
}

func newRequestIDToChannelIDMap() *requestIDToChannelIDMap {
	return &requestIDToChannelIDMap{
		m: make(map[graphsync.RequestID]channelInfo),
	}
}

// get the value for a key
func (m *requestIDToChannelIDMap) load(key graphsync.RequestID) (datatransfer.ChannelID, bool) {
	m.lk.RLock()
	defer m.lk.RUnlock()

	val, ok := m.m[key]
	return val.channelID, ok
}

// get the value if any of the keys exists in the map
func (m *requestIDToChannelIDMap) any(ks ...graphsync.RequestID) (datatransfer.ChannelID, bool) {
	m.lk.RLock()
	defer m.lk.RUnlock()

	for _, k := range ks {
		val, ok := m.m[k]
		if ok {
			return val.channelID, ok
		}
	}
	return datatransfer.ChannelID{}, false
}

// set the value for a key
func (m *requestIDToChannelIDMap) set(key graphsync.RequestID, sending bool, chid datatransfer.ChannelID) {
	m.lk.Lock()
	defer m.lk.Unlock()

	m.m[key] = channelInfo{sending, chid}
}

// call f for each key / value in the map
func (m *requestIDToChannelIDMap) forEach(f func(k graphsync.RequestID, isSending bool, chid datatransfer.ChannelID)) {
	m.lk.RLock()
	defer m.lk.RUnlock()

	for k, ch := range m.m {
		f(k, ch.sending, ch.channelID)
	}
}

// delete any keys that reference this value
func (m *requestIDToChannelIDMap) deleteRefs(id datatransfer.ChannelID) {
	m.lk.Lock()
	defer m.lk.Unlock()

	for k, ch := range m.m {
		if ch.channelID == id {
			delete(m.m, k)
		}
	}
}
