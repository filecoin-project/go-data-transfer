package testharness

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

type ReceivedTransportEvent struct {
	ChannelID      datatransfer.ChannelID
	TransportEvent datatransfer.TransportEvent
}

type FakeEvents struct {
	// function return value parameters
	ReturnedRequestReceivedResponse datatransfer.Response
	ReturnedRequestReceivedError    error
	ReturnedResponseReceivedError   error
	ReturnedChannelState            datatransfer.ChannelState
	ReturnedOnContextAugmentFunc    func(context.Context) context.Context

	// recording of actions
	OnRequestReceivedCalled  bool
	ReceivedRequest          datatransfer.Request
	OnResponseReceivedCalled bool
	ReceivedResponse         datatransfer.Response
	ReceivedTransportEvents  []ReceivedTransportEvent
}

func (fe *FakeEvents) OnTransportEvent(chid datatransfer.ChannelID, evt datatransfer.TransportEvent) {
	fe.ReceivedTransportEvents = append(fe.ReceivedTransportEvents, ReceivedTransportEvent{chid, evt})
}

func (fe *FakeEvents) AssertTransportEvent(t *testing.T, chid datatransfer.ChannelID, evt datatransfer.TransportEvent) {
	require.Contains(t, fe.ReceivedTransportEvents, ReceivedTransportEvent{chid, evt})
}

func (fe *FakeEvents) RefuteTransportEvent(t *testing.T, chid datatransfer.ChannelID, evt datatransfer.TransportEvent) {
	require.NotContains(t, fe.ReceivedTransportEvents, ReceivedTransportEvent{chid, evt})
}
func (fe *FakeEvents) OnRequestReceived(chid datatransfer.ChannelID, request datatransfer.Request) (datatransfer.Response, error) {
	fe.OnRequestReceivedCalled = true
	fe.ReceivedRequest = request
	return fe.ReturnedRequestReceivedResponse, fe.ReturnedRequestReceivedError
}

func (fe *FakeEvents) OnResponseReceived(chid datatransfer.ChannelID, response datatransfer.Response) error {
	fe.OnResponseReceivedCalled = true
	fe.ReceivedResponse = response
	return fe.ReturnedResponseReceivedError
}

func (fe *FakeEvents) OnContextAugment(chid datatransfer.ChannelID) func(context.Context) context.Context {
	return fe.ReturnedOnContextAugmentFunc
}

func (fe *FakeEvents) ChannelState(ctx context.Context, chid datatransfer.ChannelID) (datatransfer.ChannelState, error) {
	return fe.ReturnedChannelState, nil
}
