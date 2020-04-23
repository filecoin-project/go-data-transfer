package pubsub_test

import (
	"errors"
	"testing"

	"github.com/filecoin-project/go-data-transfer/pubsub"
	"github.com/stretchr/testify/assert"
)

type fakeEvent struct {
	value uint64
}

type fakeSubscriber func(value uint64) error

func dispatcher(evt pubsub.Event, subscriberFn pubsub.SubscriberFn) error {
	fe := evt.(fakeEvent)
	fs := subscriberFn.(fakeSubscriber)
	return fs(fe.value)
}

func TestPubSub(t *testing.T) {
	t.Run("subscribe / unsubscribe", func(t *testing.T) {
		ps := pubsub.New(dispatcher)

		subscriber1Count := 0
		lastValue1 := uint64(0)
		var subscriber fakeSubscriber = func(value uint64) error {
			lastValue1 = value
			subscriber1Count++
			return nil
		}

		subscriber2Count := 0
		lastValue2 := uint64(0)
		var subscriber2 fakeSubscriber = func(value uint64) error {
			lastValue2 = value
			subscriber2Count++
			return nil
		}

		unsubFunc := ps.Subscribe(subscriber)

		ev1 := fakeEvent{1}
		err := ps.Publish(ev1)
		assert.NoError(t, err)
		assert.Equal(t, 1, subscriber1Count)
		assert.Equal(t, 0, subscriber2Count)
		assert.Equal(t, uint64(1), lastValue1)
		assert.Equal(t, uint64(0), lastValue2)

		unsubFunc2 := ps.Subscribe(subscriber2)
		ev2 := fakeEvent{2}
		err = ps.Publish(ev2)
		assert.NoError(t, err)
		assert.Equal(t, 2, subscriber1Count)
		assert.Equal(t, 1, subscriber2Count)
		assert.Equal(t, uint64(2), lastValue1)
		assert.Equal(t, uint64(2), lastValue2)

		//  ensure subsequent calls don't cause errors, and also check that the right item
		// is removed, i.e. no false positives.
		unsubFunc()
		unsubFunc()
		ev3 := fakeEvent{3}
		err = ps.Publish(ev3)
		assert.NoError(t, err)
		assert.Equal(t, 2, subscriber1Count)
		assert.Equal(t, 2, subscriber2Count)
		assert.Equal(t, uint64(2), lastValue1)
		assert.Equal(t, uint64(3), lastValue2)

		// ensure it can delete all elems
		unsubFunc2()
		ev4 := fakeEvent{4}
		err = ps.Publish(ev4)
		assert.NoError(t, err)
		assert.Equal(t, 2, subscriber1Count)
		assert.Equal(t, 2, subscriber2Count)
		assert.Equal(t, uint64(2), lastValue1)
		assert.Equal(t, uint64(3), lastValue2)
	})

	t.Run("test errors", func(t *testing.T) {
		ps := pubsub.New(dispatcher)

		subscriber1Count := 0
		subscriber1Err := errors.New("disaster")
		var subscriber fakeSubscriber = func(value uint64) error {
			subscriber1Count++
			if value == 2 {
				return subscriber1Err
			}
			return nil
		}

		subscriber2Count := 0
		subscriber2Err := errors.New("calamity")
		var subscriber2 fakeSubscriber = func(value uint64) error {
			subscriber2Count++
			if value == 3 {
				return subscriber2Err
			}
			return nil
		}

		_ = ps.Subscribe(subscriber)
		_ = ps.Subscribe(subscriber2)

		ev1 := fakeEvent{1}
		err := ps.Publish(ev1)
		assert.NoError(t, err)
		assert.Equal(t, 1, subscriber1Count)
		assert.Equal(t, 1, subscriber2Count)

		// on an error, it should pass through as return value,
		// an no further subscribers should be notified
		ev2 := fakeEvent{2}
		err = ps.Publish(ev2)
		assert.EqualError(t, err, subscriber1Err.Error())
		assert.Equal(t, 2, subscriber1Count)
		assert.Equal(t, 1, subscriber2Count)

		// an error that happens later should not does not prevent
		// earlier subscribers from being notified
		ev3 := fakeEvent{3}
		err = ps.Publish(ev3)
		assert.EqualError(t, err, subscriber2Err.Error())
		assert.Equal(t, 3, subscriber1Count)
		assert.Equal(t, 2, subscriber2Count)
	})
}
