package node

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Test_isECSNode(t *testing.T) {
	type args struct {
		node *corev1.Node
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "normal node",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{},
				},
			},
			want: true,
		},
		{
			name: "vk node",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"type": "virtual-kubelet",
						},
					},
				},
			},
			want: false,
		},
		{
			name: "linjun node",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"alibabacloud.com/lingjun-worker": "true",
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isECSNode(tt.args.node); got != tt.want {
				t.Errorf("isECSNode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_predicateNode(t *testing.T) {
	type args struct {
		o client.Object
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "non node",
			args: args{
				o: &corev1.Pod{},
			},
			want: false,
		},
		{
			name: "empty node",
			args: args{
				o: &corev1.Node{},
			},
			want: false,
		},
		{
			name: "normal node",
			args: args{
				o: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"topology.kubernetes.io/region": "cn-hangzhou",
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := predicateNode(tt.args.o); got != tt.want {
				t.Errorf("predicateNode() = %v, want %v", got, tt.want)
			}
		})
	}
}
