package eni

import (
	"sync"

	"k8s.io/klog/v2"
)

// Notifier provides a simple message subscription-based notification mechanism
type Notifier struct {
	mu          sync.RWMutex
	subscribers []chan struct{} // Simple list of channels
	closed      bool
}

// NewNotifier creates a new notifier
func NewNotifier() *Notifier {
	return &Notifier{
		subscribers: make([]chan struct{}, 0),
	}
}

// Subscribe registers a subscriber and returns a subscription channel
func (n *Notifier) Subscribe() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.closed {
		// If the notifier is closed, return a closed channel
		ch := make(chan struct{})
		close(ch)
		return ch
	}

	ch := make(chan struct{}, 1) // Use struct{} to save memory, buffer size 1 to avoid blocking
	n.subscribers = append(n.subscribers, ch)
	return ch
}

// Notify sends notifications to all subscribers
func (n *Notifier) Notify() {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if n.closed {
		return
	}

	if len(n.subscribers) == 0 {
		return
	}

	// Send notification to all subscribers
	for _, ch := range n.subscribers {
		select {
		case ch <- struct{}{}:
			// Send successful
		default:
			// Channel is full, skip this subscriber
			klog.V(4).Info("subscriber channel is full, skipping notification")
		}
	}
}

// Unsubscribe removes a subscriber by channel reference
func (n *Notifier) Unsubscribe(ch <-chan struct{}) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.closed {
		return
	}

	// Find and remove the channel from the list
	for i, subscriber := range n.subscribers {
		if subscriber == ch {
			// Remove from slice
			n.subscribers = append(n.subscribers[:i], n.subscribers[i+1:]...)
			close(subscriber)
			break
		}
	}
}

// Close closes the notifier and cleans up all subscribers
func (n *Notifier) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.closed {
		return
	}

	n.closed = true

	// Clean up all subscribers
	for _, ch := range n.subscribers {
		close(ch)
	}
	n.subscribers = make([]chan struct{}, 0)
}

// GetSubscriberCount gets the current number of subscribers (for debugging and monitoring)
func (n *Notifier) GetSubscriberCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.subscribers)
}

// IsClosed checks if the notifier is closed
func (n *Notifier) IsClosed() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.closed
}
