package testutil

import (
	cid "github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

type MockChannelStateParams struct {
	ReceivedCids []cid.Cid
	ChannelID    datatransfer.ChannelID
	Queued       uint64
	Sent         uint64
	Received     uint64
	Complete     bool
}

func NewMockChannelState(params MockChannelStateParams) *MockChannelState {
	return &MockChannelState{
		receivedCids: params.ReceivedCids,
		chid:         params.ChannelID,
		queued:       params.Queued,
		sent:         params.Sent,
		received:     params.Received,
		complete:     params.Complete,
	}
}

type MockChannelState struct {
	receivedCids []cid.Cid
	chid         datatransfer.ChannelID
	queued       uint64
	sent         uint64
	received     uint64
	complete     bool
}

var _ datatransfer.ChannelState = (*MockChannelState)(nil)

func (m *MockChannelState) Queued() uint64 {
	return m.queued
}

func (m *MockChannelState) SetQueued(queued uint64) {
	m.queued = queued
}

func (m *MockChannelState) Sent() uint64 {
	return m.sent
}

func (m *MockChannelState) SetSent(sent uint64) {
	m.sent = sent
}

func (m *MockChannelState) Received() uint64 {
	return m.received
}

func (m *MockChannelState) SetReceived(received uint64) {
	m.received = received
}

func (m *MockChannelState) ChannelID() datatransfer.ChannelID {
	return m.chid
}

func (m *MockChannelState) SetComplete(complete bool) {
	m.complete = complete
}
func (m *MockChannelState) Status() datatransfer.Status {
	if m.complete {
		return datatransfer.Completed
	}
	return datatransfer.Ongoing
}

func (m *MockChannelState) ReceivedCids() []cid.Cid {
	return m.receivedCids
}

func (m *MockChannelState) ReceivedCidsLen() int {
	return len(m.receivedCids)
}

func (m *MockChannelState) ReceivedCidsTotal() int64 {
	return (int64)(len(m.receivedCids))
}

func (m *MockChannelState) QueuedCidsTotal() int64 {
	panic("implement me")
}

func (m *MockChannelState) SentCidsTotal() int64 {
	panic("implement me")
}

func (m *MockChannelState) TransferID() datatransfer.TransferID {
	panic("implement me")
}

func (m *MockChannelState) BaseCID() cid.Cid {
	panic("implement me")
}

func (m *MockChannelState) Selector() datamodel.Node {
	panic("implement me")
}

func (m *MockChannelState) Voucher() datatransfer.TypedVoucher {
	panic("implement me")
}

func (m *MockChannelState) Sender() peer.ID {
	panic("implement me")
}

func (m *MockChannelState) Recipient() peer.ID {
	panic("implement me")
}

func (m *MockChannelState) TotalSize() uint64 {
	panic("implement me")
}

func (m *MockChannelState) IsPull() bool {
	panic("implement me")
}

func (m *MockChannelState) OtherPeer() peer.ID {
	panic("implement me")
}

func (m *MockChannelState) SelfPeer() peer.ID {
	panic("implement me")
}

func (m *MockChannelState) Message() string {
	panic("implement me")
}

func (m *MockChannelState) Vouchers() []datatransfer.TypedVoucher {
	panic("implement me")
}

func (m *MockChannelState) VoucherResults() []datatransfer.TypedVoucher {
	panic("implement me")
}

func (m *MockChannelState) LastVoucher() datatransfer.TypedVoucher {
	panic("implement me")
}

func (m *MockChannelState) LastVoucherResult() datatransfer.TypedVoucher {
	panic("implement me")
}

func (m *MockChannelState) Stages() *datatransfer.ChannelStages {
	panic("implement me")
}

func (m *MockChannelState) DataLimit() uint64 {
	panic("implement me")
}

func (m *MockChannelState) RequiresFinalization() bool {
	panic("implement me")
}
