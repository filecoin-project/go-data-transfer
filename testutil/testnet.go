package testutil

import (
	"github.com/filecoin-project/go-data-transfer/network"
	"context"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/libp2p/go-libp2p-core/peer"
)

// FakeSentMessage is a recording of a message sent on the FakeNetwork
type FakeSentMessage struct {
	PeerID peer.ID
	Message message.DataTransferMessage
}
// FakeNetwork is a network that satisfies the DataTransferNetwork interface but
// does not actually do anything
type FakeNetwork struct {
	PeerID peer.ID
	SentMessages []FakeSentMessage
	Delegate network.Receiver
}

// NewFakeNetwork returns a new fake data transfer network instance
func NewFakeNetwork(id peer.ID) *FakeNetwork {
	return &FakeNetwork{PeerID: id}
}

// SendMessage sends a GraphSync message to a peer.
func (fn *FakeNetwork) SendMessage(ctx context.Context, p peer.ID, m message.DataTransferMessage) error {
	fn.SentMessages = append(fn.SentMessages, FakeSentMessage{p, m})
	return nil
}

// SetDelegate registers the Reciver to handle messages received from the
// network.
func (fn *FakeNetwork) SetDelegate(receiver network.Receiver) {
	fn.Delegate = receiver
}

// ConnectTo establishes a connection to the given peer
func (fn *FakeNetwork) ConnectTo(_ context.Context, _ peer.ID) error {
	panic("not implemented")
}
