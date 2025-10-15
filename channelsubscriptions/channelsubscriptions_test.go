package channelsubscriptions_test

import (
	"testing"

	"github.com/ipfs/go-test/random"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channelsubscriptions"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
)

func TestChannelSubscriptions(t *testing.T) {
	peers := random.Peers(2)
	tid1 := datatransfer.TransferID(0)
	tid2 := datatransfer.TransferID(1)

	chid1 := datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid1}
	chid2 := datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: tid2}

	ms := &mockSubscriptionAPI{}
	cs := channelsubscriptions.NewChannelSubscriptions(ms)
	require.NotNil(t, ms.subscriber)
	var events []datatransfer.EventCode
	// no events while not subscribed
	ms.subscriber(datatransfer.Event{Code: datatransfer.Open}, testutil.NewMockChannelState(testutil.MockChannelStateParams{
		ChannelID: chid1,
	}))
	require.Empty(t, events)
	cs.Subscribe(chid1, func(evt datatransfer.Event, state datatransfer.ChannelState) {
		events = append(events, evt.Code)
	})
	// receives events after subscription
	ms.subscriber(datatransfer.Event{Code: datatransfer.Open}, testutil.NewMockChannelState(testutil.MockChannelStateParams{
		ChannelID: chid1,
	}))
	require.Len(t, events, 1)
	require.Equal(t, events[0], datatransfer.Open)
	ms.subscriber(datatransfer.Event{Code: datatransfer.Accept}, testutil.NewMockChannelState(testutil.MockChannelStateParams{
		ChannelID: chid1,
	}))
	require.Len(t, events, 2)
	require.Equal(t, events[1], datatransfer.Accept)
	// does not receive events for other channels
	ms.subscriber(datatransfer.Event{Code: datatransfer.TransferInitiated}, testutil.NewMockChannelState(testutil.MockChannelStateParams{
		ChannelID: chid2,
	}))
	require.Len(t, events, 2)

	// send final event
	ms.subscriber(datatransfer.Event{Code: datatransfer.CleanupComplete}, testutil.NewMockChannelState(testutil.MockChannelStateParams{
		ChannelID: chid1,
		Complete:  true,
	}))
	require.Len(t, events, 3)
	require.Equal(t, events[2], datatransfer.CleanupComplete)

	// receives no more events after complete
	ms.subscriber(datatransfer.Event{Code: datatransfer.EventCode(datatransfer.DataSent)}, testutil.NewMockChannelState(testutil.MockChannelStateParams{
		ChannelID: chid1,
	}))
	require.Len(t, events, 3)

	// verify stop unsubscribes
	cs.Stop()
	require.Nil(t, ms.subscriber)
}

type mockSubscriptionAPI struct {
	subscriber datatransfer.Subscriber
}

func (ms *mockSubscriptionAPI) SubscribeToEvents(subscriber datatransfer.Subscriber) datatransfer.Unsubscribe {
	ms.subscriber = subscriber
	return func() {
		ms.subscriber = nil
	}
}
