package podnetworking

import (
	"testing"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

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
