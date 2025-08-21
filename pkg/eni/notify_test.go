package eni

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotifier_Subscribe(t *testing.T) {
	notifier := NewNotifier()
	assert.NotNil(t, notifier)
	assert.Equal(t, 0, notifier.GetSubscriberCount())

	// Subscribe
	ch1 := notifier.Subscribe()
	assert.NotNil(t, ch1)
	assert.Equal(t, 1, notifier.GetSubscriberCount())

	// Subscribe again
	ch2 := notifier.Subscribe()
	assert.NotNil(t, ch2)
	assert.Equal(t, 2, notifier.GetSubscriberCount())

	// Verify channels are different
	assert.NotEqual(t, ch1, ch2)
}

func TestNotifier_Notify(t *testing.T) {
	notifier := NewNotifier()

	// Create subscribers
	ch1 := notifier.Subscribe()
	ch2 := notifier.Subscribe()

	// Channel for synchronization
	var wg sync.WaitGroup
	wg.Add(2)

	// Subscriber 1
	go func() {
		defer wg.Done()
		select {
		case <-ch1:
			// Received notification
		case <-time.After(1 * time.Second):
			t.Error("ch1 timeout waiting for notification")
		}
	}()

	// Subscriber 2
	go func() {
		defer wg.Done()
		select {
		case <-ch2:
			// Received notification
		case <-time.After(1 * time.Second):
			t.Error("ch2 timeout waiting for notification")
		}
	}()

	// Send notification
	notifier.Notify()

	// Wait for all subscribers to complete processing
	wg.Wait()
}

func TestNotifier_Unsubscribe(t *testing.T) {
	notifier := NewNotifier()

	// Subscribe
	ch := notifier.Subscribe()
	assert.Equal(t, 1, notifier.GetSubscriberCount())

	// Unsubscribe
	notifier.Unsubscribe(ch)
	assert.Equal(t, 0, notifier.GetSubscriberCount())

	// Verify channel is closed
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	default:
		t.Error("channel should be closed")
	}
}

func TestNotifier_Close(t *testing.T) {
	notifier := NewNotifier()

	// Subscribe
	ch1 := notifier.Subscribe()
	ch2 := notifier.Subscribe()
	assert.Equal(t, 2, notifier.GetSubscriberCount())

	// Close notifier
	notifier.Close()
	assert.True(t, notifier.IsClosed())
	assert.Equal(t, 0, notifier.GetSubscriberCount())

	// Verify all channels are closed
	select {
	case _, ok := <-ch1:
		assert.False(t, ok, "ch1 channel should be closed")
	default:
		t.Error("ch1 channel should be closed")
	}

	select {
	case _, ok := <-ch2:
		assert.False(t, ok, "ch2 channel should be closed")
	default:
		t.Error("ch2 channel should be closed")
	}
}

func TestNotifier_SubscribeAfterClose(t *testing.T) {
	notifier := NewNotifier()

	// Close notifier
	notifier.Close()
	assert.True(t, notifier.IsClosed())

	// Try to subscribe
	ch := notifier.Subscribe()
	assert.NotNil(t, ch)

	// Verify returned channel is closed
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	default:
		t.Error("channel should be closed")
	}
}

func TestNotifier_NotifyAfterClose(t *testing.T) {
	notifier := NewNotifier()

	// Subscribe
	ch := notifier.Subscribe()

	// Close notifier
	notifier.Close()

	// Try to send notification (should not panic)
	notifier.Notify()

	// Verify channel is closed
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	default:
		t.Error("channel should be closed")
	}
}
