package pushchannelmonitor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer"
)

func TestPushChannelMonitor(t *testing.T) {
	type testCase struct {
		name                 string
		restartErrs          int
		expRestartAttempts   int
		dataQueued           uint64
		dataSent             uint64
		errorEvent           bool
		completeAfterRestart bool
	}
	testCases := []testCase{{
		name:                 "attempt restart",
		restartErrs:          0,
		expRestartAttempts:   1,
		dataQueued:           10,
		dataSent:             5,
		completeAfterRestart: true,
	}, {
		name:               "fail attempt restart",
		restartErrs:        1,
		expRestartAttempts: 1,
		dataQueued:         10,
		dataSent:           5,
	}, {
		name:                 "error event",
		restartErrs:          0,
		expRestartAttempts:   1,
		dataQueued:           10,
		dataSent:             10,
		errorEvent:           true,
		completeAfterRestart: true,
	}, {
		name:               "error event then fail attempt restart",
		restartErrs:        1,
		expRestartAttempts: 1,
		dataQueued:         10,
		dataSent:           10,
		errorEvent:         true,
	}}

	ch1 := datatransfer.ChannelID{
		Initiator: "initiator",
		Responder: "responder",
		ID:        1,
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ch := &mockChannelState{chid: ch1}
			mockAPI := newMockMonitorAPI(ch, tc.restartErrs)

			m := NewPushChannelMonitor(mockAPI, &PushMonitorConfig{
				Interval:     10 * time.Millisecond,
				MinBytesSent: 1,
			})
			m.Start()
			m.AddChannel(ch1)

			mockAPI.dataQueued(tc.dataQueued)
			mockAPI.dataSent(tc.dataSent)
			if tc.errorEvent {
				mockAPI.errorEvent()
			}

			if !tc.completeAfterRestart {
				// If there is no recovery from restart, wait for the push
				// channel to be closed
				<-mockAPI.closed
				require.Equal(t, tc.expRestartAttempts, mockAPI.restartAttempts)
				return
			}

			require.Len(t, m.channels, 1)
			var mch *monitoredChannel
			for mch = range m.channels {
			}

			// Simulate sending the remaining data
			delta := tc.dataQueued - tc.dataSent
			if delta > 0 {
				mockAPI.dataSent(delta)
			}

			// Simulate the complete event
			mockAPI.completed()

			// Verify that channel has been shutdown
			select {
			case <-time.After(100 * time.Millisecond):
				require.Fail(t, "failed to shutdown channel")
			case <-mch.ctx.Done():
			}
		})
	}
}

type mockMonitorAPI struct {
	ch              *mockChannelState
	lk              sync.Mutex
	subscriber      datatransfer.Subscriber
	restartAttempts int
	restartErrors   chan error
	closed          chan struct{}
}

func newMockMonitorAPI(ch *mockChannelState, restartErrsCount int) *mockMonitorAPI {
	m := &mockMonitorAPI{
		ch:            ch,
		closed:        make(chan struct{}),
		restartErrors: make(chan error, restartErrsCount),
	}
	for i := 0; i < restartErrsCount; i++ {
		m.restartErrors <- xerrors.Errorf("restart err")
	}
	return m
}

func (m *mockMonitorAPI) SubscribeToEvents(subscriber datatransfer.Subscriber) datatransfer.Unsubscribe {
	m.lk.Lock()
	defer m.lk.Unlock()

	m.subscriber = subscriber

	return func() {
		m.lk.Lock()
		defer m.lk.Unlock()

		m.subscriber = nil
	}
}

func (m *mockMonitorAPI) callSubscriber(e datatransfer.Event, state datatransfer.ChannelState) {
	m.subscriber(e, state)
}

func (m *mockMonitorAPI) RestartDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	m.lk.Lock()
	defer m.lk.Unlock()

	m.restartAttempts++

	select {
	case err := <-m.restartErrors:
		return err
	default:
		return nil
	}
}

func (m *mockMonitorAPI) CloseDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	close(m.closed)
	return nil
}

func (m *mockMonitorAPI) dataQueued(n uint64) {
	m.ch.queued = n
	m.callSubscriber(datatransfer.Event{Code: datatransfer.DataQueued}, m.ch)
}

func (m *mockMonitorAPI) dataSent(n uint64) {
	m.ch.sent = n
	m.callSubscriber(datatransfer.Event{Code: datatransfer.DataSent}, m.ch)
}

func (m *mockMonitorAPI) completed() {
	m.ch.complete = true
	m.callSubscriber(datatransfer.Event{Code: datatransfer.Complete}, m.ch)
}

func (m *mockMonitorAPI) errorEvent() {
	m.callSubscriber(datatransfer.Event{Code: datatransfer.Error}, m.ch)
}

type mockChannelState struct {
	chid     datatransfer.ChannelID
	queued   uint64
	sent     uint64
	complete bool
}

func (m *mockChannelState) Queued() uint64 {
	return m.queued
}

func (m *mockChannelState) Sent() uint64 {
	return m.sent
}

func (m *mockChannelState) ChannelID() datatransfer.ChannelID {
	return m.chid
}

func (m *mockChannelState) Status() datatransfer.Status {
	if m.complete {
		return datatransfer.Completed
	}
	return datatransfer.Ongoing
}

func (m *mockChannelState) TransferID() datatransfer.TransferID {
	panic("implement me")
}

func (m *mockChannelState) BaseCID() cid.Cid {
	panic("implement me")
}

func (m *mockChannelState) Selector() ipld.Node {
	panic("implement me")
}

func (m *mockChannelState) Voucher() datatransfer.Voucher {
	panic("implement me")
}

func (m *mockChannelState) Sender() peer.ID {
	panic("implement me")
}

func (m *mockChannelState) Recipient() peer.ID {
	panic("implement me")
}

func (m *mockChannelState) TotalSize() uint64 {
	panic("implement me")
}

func (m *mockChannelState) IsPull() bool {
	panic("implement me")
}

func (m *mockChannelState) OtherPeer() peer.ID {
	panic("implement me")
}

func (m *mockChannelState) SelfPeer() peer.ID {
	panic("implement me")
}

func (m *mockChannelState) Received() uint64 {
	panic("implement me")
}

func (m *mockChannelState) Message() string {
	panic("implement me")
}

func (m *mockChannelState) Vouchers() []datatransfer.Voucher {
	panic("implement me")
}

func (m *mockChannelState) VoucherResults() []datatransfer.VoucherResult {
	panic("implement me")
}

func (m *mockChannelState) LastVoucher() datatransfer.Voucher {
	panic("implement me")
}

func (m *mockChannelState) LastVoucherResult() datatransfer.VoucherResult {
	panic("implement me")
}

func (m *mockChannelState) ReceivedCids() []cid.Cid {
	panic("implement me")
}
