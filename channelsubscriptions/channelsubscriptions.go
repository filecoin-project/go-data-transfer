package channelsubscriptions

import (
	"sync"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels"
)

type ChannelSubscriptions struct {
	subscriptions   map[datatransfer.ChannelID][]datatransfer.Subscriber
	subscriptionsLk sync.RWMutex
	unsub           datatransfer.Unsubscribe
}

type SubscriptionAPI interface {
	SubscribeToEvents(subscriber datatransfer.Subscriber) datatransfer.Unsubscribe
}

func NewChannelSubscriptions(subscriptionAPI SubscriptionAPI) *ChannelSubscriptions {
	cs := &ChannelSubscriptions{
		subscriptions: make(map[datatransfer.ChannelID][]datatransfer.Subscriber),
	}
	cs.unsub = subscriptionAPI.SubscribeToEvents(cs.subscriber)
	return cs
}

func (cs *ChannelSubscriptions) Stop() {
	cs.unsub()
}

func (cs *ChannelSubscriptions) Subscribe(chid datatransfer.ChannelID, cb datatransfer.Subscriber) {
	cs.subscriptionsLk.Lock()
	defer cs.subscriptionsLk.Unlock()
	cs.subscriptions[chid] = append(cs.subscriptions[chid], cb)
}

func (cs *ChannelSubscriptions) subscriber(evt datatransfer.Event, state datatransfer.ChannelState) {
	cs.subscriptionsLk.RLock()
	cbs := cs.subscriptions[state.ChannelID()]
	for _, cb := range cbs {
		cb(evt, state)
	}
	cs.subscriptionsLk.RUnlock()

	if channels.IsChannelTerminated(state.Status()) {
		cs.subscriptionsLk.Lock()
		delete(cs.subscriptions, state.ChannelID())
		cs.subscriptionsLk.Unlock()
	}
}
