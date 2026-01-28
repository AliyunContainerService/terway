package pod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestPredicateForPodEvent_Create(t *testing.T) {
	p := &predicateForPodEvent{}

	// Test with nil object
	createEvent := event.CreateEvent{
		Object: nil,
	}
	result := p.Create(createEvent)
	assert.False(t, result)

	// Test with non-pod object
	createEvent2 := event.CreateEvent{
		Object: &corev1.Node{},
	}
	result = p.Create(createEvent2)
	assert.False(t, result)
}

func TestPredicateForPodEvent_Update(t *testing.T) {
	p := &predicateForPodEvent{}

	// Test with nil object
	updateEvent := event.UpdateEvent{
		ObjectNew: nil,
	}
	result := p.Update(updateEvent)
	assert.False(t, result)

	// Test with non-pod object
	updateEvent2 := event.UpdateEvent{
		ObjectNew: &corev1.Node{},
	}
	result = p.Update(updateEvent2)
	assert.False(t, result)
}

func TestPredicateForPodEvent_Delete(t *testing.T) {
	p := &predicateForPodEvent{}

	// Test with nil object
	deleteEvent := event.DeleteEvent{
		Object: nil,
	}
	result := p.Delete(deleteEvent)
	assert.False(t, result)

	// Test with non-pod object
	deleteEvent2 := event.DeleteEvent{
		Object: &corev1.Node{},
	}
	result = p.Delete(deleteEvent2)
	assert.False(t, result)
}

func TestPredicateForPodEvent_Generic(t *testing.T) {
	p := &predicateForPodEvent{}

	// Test with nil object
	genericEvent := event.GenericEvent{
		Object: nil,
	}
	result := p.Generic(genericEvent)
	assert.False(t, result)

	// Test with non-pod object
	genericEvent2 := event.GenericEvent{
		Object: &corev1.Node{},
	}
	result = p.Generic(genericEvent2)
	assert.False(t, result)
}
