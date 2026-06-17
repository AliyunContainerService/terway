package pod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/AliyunContainerService/terway/types"
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

func TestNeedProcess(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "pod with no node name returns false",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{NodeName: ""},
			},
			want: false,
		},
		{
			name: "host network pod returns false",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					NodeName:    "node-1",
					HostNetwork: true,
				},
			},
			want: false,
		},
		{
			name: "ignored by terway returns false",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{types.IgnoreByTerway: "true"},
				},
				Spec: corev1.PodSpec{NodeName: "node-1"},
			},
			want: false,
		},
		{
			name: "pod use ENI returns false",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{types.PodENI: "true"},
				},
				Spec: corev1.PodSpec{NodeName: "node-1"},
			},
			want: false,
		},
		{
			name: "pod with IP and running sandbox returns false",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{NodeName: "node-1"},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.1",
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
					},
				},
			},
			want: false,
		},
		{
			name: "pod with node name and no IP and no host network should process",
			pod: &corev1.Pod{
				Spec:   corev1.PodSpec{NodeName: "node-1"},
				Status: corev1.PodStatus{PodIP: ""},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needProcess(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}
