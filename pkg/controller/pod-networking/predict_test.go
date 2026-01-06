package podnetworking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func TestPredicateForPodNetworkingEvent_Update(t *testing.T) {
	p := &predicateForPodnetwokringEvent{}

	t.Run("ObjectNew is not PodNetworking", func(t *testing.T) {
		e := event.UpdateEvent{
			ObjectOld: &v1beta1.PodNetworking{},
			ObjectNew: &corev1.Pod{}, // Not a PodNetworking
		}
		result := p.Update(e)
		assert.False(t, result)
	})

	t.Run("Status is empty string", func(t *testing.T) {
		e := event.UpdateEvent{
			ObjectOld: &v1beta1.PodNetworking{},
			ObjectNew: &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Status: v1beta1.PodNetworkingStatus{
					Status: "", // Empty status
				},
			},
		}
		result := p.Update(e)
		assert.True(t, result)
	})

	t.Run("Status is NetworkingStatusFail", func(t *testing.T) {
		e := event.UpdateEvent{
			ObjectOld: &v1beta1.PodNetworking{},
			ObjectNew: &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Status: v1beta1.PodNetworkingStatus{
					Status: v1beta1.NetworkingStatusFail,
				},
			},
		}
		result := p.Update(e)
		assert.True(t, result)
	})

	t.Run("Status is Ready and VSwitches changed", func(t *testing.T) {
		e := event.UpdateEvent{
			ObjectOld: &v1beta1.PodNetworking{},
			ObjectNew: &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1beta1.PodNetworkingSpec{
					VSwitchOptions: []string{"vsw-1", "vsw-2"},
				},
				Status: v1beta1.PodNetworkingStatus{
					Status: v1beta1.NetworkingStatusReady,
					VSwitches: []v1beta1.VSwitch{
						{ID: "vsw-1"}, // Missing vsw-2
					},
				},
			},
		}
		result := p.Update(e)
		assert.True(t, result)
	})

	t.Run("Status is Ready and VSwitches not changed", func(t *testing.T) {
		e := event.UpdateEvent{
			ObjectOld: &v1beta1.PodNetworking{},
			ObjectNew: &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1beta1.PodNetworkingSpec{
					VSwitchOptions: []string{"vsw-1", "vsw-2"},
				},
				Status: v1beta1.PodNetworkingStatus{
					Status: v1beta1.NetworkingStatusReady,
					VSwitches: []v1beta1.VSwitch{
						{ID: "vsw-1"},
						{ID: "vsw-2"},
					},
				},
			},
		}
		result := p.Update(e)
		assert.False(t, result)
	})
}

func Test_changed(t *testing.T) {
	type args struct {
		pn *v1beta1.PodNetworking
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "un sorted",
			args: args{
				pn: &v1beta1.PodNetworking{
					Spec: v1beta1.PodNetworkingSpec{
						VSwitchOptions: []string{"foo", "bar"},
					},
					Status: v1beta1.PodNetworkingStatus{
						VSwitches: []v1beta1.VSwitch{
							{
								ID: "bar",
							},
							{
								ID: "foo",
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "modify",
			args: args{
				pn: &v1beta1.PodNetworking{
					Spec: v1beta1.PodNetworkingSpec{
						VSwitchOptions: []string{"foo", "bar"},
					},
					Status: v1beta1.PodNetworkingStatus{
						VSwitches: []v1beta1.VSwitch{
							{
								ID: "var",
							},
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := changed(tt.args.pn); got != tt.want {
				t.Errorf("changed() = %v, want %v", got, tt.want)
			}
		})
	}
}
