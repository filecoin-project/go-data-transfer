package testutil

import (
	cid "github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/libp2p/go-libp2p/core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

type MockChannelStateParams struct {
	ReceivedCidsTotal int64
	SentCidsTotal     int64
	QueuedCidsTotal   int64
	ChannelID         datatransfer.ChannelID
	Queued            uint64
	Sent              uint64
	Received          uint64
	Complete          bool
	BaseCID           cid.Cid
	Selector          ipld.Node
	Voucher           datatransfer.TypedVoucher
	IsPull            bool
	Self              peer.ID
	DataLimit         uint64
	InitiatorPaused   bool
	ResponderPaused   bool
}

func NewMockChannelState(params MockChannelStateParams) *MockChannelState {
	return &MockChannelState{
		receivedIndex:   params.ReceivedCidsTotal,
		sentIndex:       params.SentCidsTotal,
		queuedIndex:     params.QueuedCidsTotal,
		dataLimit:       params.DataLimit,
		chid:            params.ChannelID,
		queued:          params.Queued,
		sent:            params.Sent,
		received:        params.Received,
		complete:        params.Complete,
		isPull:          params.IsPull,
		self:            params.Self,
		baseCID:         params.BaseCID,
		initiatorPaused: params.InitiatorPaused,
		responderPaused: params.ResponderPaused,
	}
}

type MockChannelState struct {
	receivedIndex   int64
	sentIndex       int64
	queuedIndex     int64
	dataLimit       uint64
	chid            datatransfer.ChannelID
	queued          uint64
	sent            uint64
	received        uint64
	complete        bool
	isPull          bool
	baseCID         cid.Cid
	selector        ipld.Node
	voucher         datatransfer.TypedVoucher
	self            peer.ID
	initiatorPaused bool
	responderPaused bool
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

func (m *MockChannelState) SetReceivedCidsTotal(receivedIndex int64) {
	m.receivedIndex = receivedIndex
}

func (m *MockChannelState) ReceivedCidsTotal() int64 {
	return m.receivedIndex
}

func (m *MockChannelState) QueuedCidsTotal() int64 {
	return m.queuedIndex
}

func (m *MockChannelState) SetQueuedCidsTotal(queuedIndex int64) {
	m.queuedIndex = queuedIndex
}

func (m *MockChannelState) SentCidsTotal() int64 {
	return m.sentIndex
}

func (m *MockChannelState) SetSentCidsTotal(sentIndex int64) {
	m.sentIndex = sentIndex
}

func (m *MockChannelState) TransferID() datatransfer.TransferID {
	return m.chid.ID
}

func (m *MockChannelState) BaseCID() cid.Cid {
	return m.baseCID
}

func (m *MockChannelState) Selector() datamodel.Node {
	return m.selector
}

func (m *MockChannelState) Voucher() datatransfer.TypedVoucher {
	return m.voucher
}

func (m *MockChannelState) Sender() peer.ID {
	if m.isPull {
		return m.chid.Responder
	}
	return m.chid.Initiator
}

func (m *MockChannelState) Recipient() peer.ID {
	if m.isPull {
		return m.chid.Initiator
	}
	return m.chid.Responder
}

func (m *MockChannelState) TotalSize() uint64 {
	panic("implement me")
}

func (m *MockChannelState) IsPull() bool {
	return m.isPull
}

func (m *MockChannelState) OtherPeer() peer.ID {
	if m.self == m.chid.Initiator {
		return m.chid.Responder
	}
	return m.chid.Initiator
}

func (m *MockChannelState) SelfPeer() peer.ID {
	return m.self
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

func (m *MockChannelState) SetDataLimit(dataLimit uint64) {
	m.dataLimit = dataLimit
}

func (m *MockChannelState) DataLimit() uint64 {
	return m.dataLimit
}

func (m *MockChannelState) RequiresFinalization() bool {
	panic("implement me")
}

func (m *MockChannelState) SetResponderPaused(responderPaused bool) {
	m.responderPaused = responderPaused
}

func (m *MockChannelState) ResponderPaused() bool {
	return m.responderPaused
}

func (m *MockChannelState) SetInitiatorPaused(initiatorPaused bool) {
	m.initiatorPaused = initiatorPaused
}

func (m *MockChannelState) InitiatorPaused() bool {
	return m.initiatorPaused
}

func (m *MockChannelState) BothPaused() bool {
	return m.initiatorPaused && m.responderPaused
}

func (m *MockChannelState) SelfPaused() bool {
	if m.self == m.chid.Initiator {
		return m.initiatorPaused
	}
	return m.responderPaused
}
