package testharness

import (
	"context"
	"testing"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/transport/helpers/network"
)

// FakeSentMessage is a recording of a message sent on the FakeNetwork
type FakeSentMessage struct {
	PeerID      peer.ID
	TransportID datatransfer.TransportID
	Message     datatransfer.Message
}

type FakeDelegates struct {
	TransportID datatransfer.TransportID
	Versions    []datatransfer.Version
	Receiver    network.Receiver
}

type ConnectWithRetryAttempt struct {
	PeerID      peer.ID
	TransportID datatransfer.TransportID
}

type TaggedPeer struct {
	PeerID peer.ID
	Tag    string
}

// FakeNetwork is a network that satisfies the DataTransferNetwork interface but
// does not actually do anything
type FakeNetwork struct {
	SentMessages             []FakeSentMessage
	Delegates                []FakeDelegates
	ConnectWithRetryAttempts []ConnectWithRetryAttempt
	ProtectedPeers           []TaggedPeer
	UnprotectedPeers         []TaggedPeer

	ReturnedPeerDescription       network.ProtocolDescription
	ReturnedPeerID                peer.ID
	ReturnedSendMessageError      error
	ReturnedConnectWithRetryError error
}

// NewFakeNetwork returns a new fake data transfer network instance
func NewFakeNetwork(id peer.ID) *FakeNetwork {
	return &FakeNetwork{ReturnedPeerID: id}
}

var _ network.DataTransferNetwork = (*FakeNetwork)(nil)

// SendMessage sends a GraphSync message to a peer.
func (fn *FakeNetwork) SendMessage(ctx context.Context,
	p peer.ID,
	t datatransfer.TransportID,
	m datatransfer.Message) error {
	fn.SentMessages = append(fn.SentMessages, FakeSentMessage{p, t, m})
	return fn.ReturnedSendMessageError
}

// SetDelegate registers the Reciver to handle messages received from the
// network.
func (fn *FakeNetwork) SetDelegate(t datatransfer.TransportID, v []datatransfer.Version, r network.Receiver) {
	fn.Delegates = append(fn.Delegates, FakeDelegates{t, v, r})
}

// ConnectTo establishes a connection to the given peer
func (fn *FakeNetwork) ConnectTo(_ context.Context, _ peer.ID) error {
	return nil
}

func (fn *FakeNetwork) ConnectWithRetry(ctx context.Context, p peer.ID, transportID datatransfer.TransportID) error {
	fn.ConnectWithRetryAttempts = append(fn.ConnectWithRetryAttempts, ConnectWithRetryAttempt{p, transportID})
	return fn.ReturnedConnectWithRetryError
}

// ID returns a stubbed id for host of this network
func (fn *FakeNetwork) ID() peer.ID {
	return fn.ReturnedPeerID
}

// Protect does nothing on the fake network
func (fn *FakeNetwork) Protect(id peer.ID, tag string) {
	fn.ProtectedPeers = append(fn.ProtectedPeers, TaggedPeer{id, tag})
}

// Unprotect does nothing on the fake network
func (fn *FakeNetwork) Unprotect(id peer.ID, tag string) bool {
	fn.UnprotectedPeers = append(fn.UnprotectedPeers, TaggedPeer{id, tag})
	return false
}

func (fn *FakeNetwork) Protocol(ctx context.Context, id peer.ID, transportID datatransfer.TransportID) (network.ProtocolDescription, error) {
	return fn.ReturnedPeerDescription, nil
}

func (fn *FakeNetwork) AssertSentMessage(t *testing.T, sentMessage FakeSentMessage) {
	require.Contains(t, fn.SentMessages, sentMessage)
}
