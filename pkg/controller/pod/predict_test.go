package pod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/types"
)

func TestProcessPod(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "normal pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{NodeName: "node-1"},
			},
			want: true,
		},
		{
			name: "no node name",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{NodeName: ""},
			},
			want: false,
		},
		{
			name: "host network",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{NodeName: "node-1", HostNetwork: true},
			},
			want: false,
		},
		{
			name: "ignored by terway",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{types.IgnoreByTerway: "true"},
				},
				Spec: corev1.PodSpec{NodeName: "node-1"},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, processPod(tt.pod))
		})
	}
}

func TestProcessPod_NonPodObject(t *testing.T) {
	assert.False(t, processPod(&corev1.Node{}))
}

func TestProcessNode(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "normal node",
			node: &corev1.Node{},
			want: true,
		},
		{
			name: "ignored by terway",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{types.IgnoreByTerway: "true"},
				},
			},
			want: false,
		},
		{
			name: "vk node",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"type": "virtual-kubelet"},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, processNode(tt.node))
		})
	}
}
