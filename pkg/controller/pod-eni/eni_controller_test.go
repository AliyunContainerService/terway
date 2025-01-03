package podeni

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/mocks"
)

func TestReconcilePodENI_podRequirePodENI(t *testing.T) {
	type fields struct {
		client    client.Client
		scheme    *runtime.Scheme
		aliyun    register.Interface
		record    record.EventRecorder
		trunkMode bool
		crdMode   bool
	}
	type args struct {
		ctx context.Context
		pod *corev1.Pod
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "normal pod",
			fields: fields{
				client: func() client.Client {
					nodeReader := fake.NewClientBuilder()
					nodeReader.WithObjects(&corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: "foo",
						},
						Spec:   corev1.NodeSpec{},
						Status: corev1.NodeStatus{},
					})
					return nodeReader.Build()
				}(),
				scheme:    nil,
				aliyun:    nil,
				record:    nil,
				trunkMode: false,
				crdMode:   false,
			},
			args: args{
				ctx: context.Background(),
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						NodeName: "foo",
					},
					Status: corev1.PodStatus{},
				},
			},
			want: false,
		},
		{
			name: "trunk pod",
			fields: fields{
				client: func() client.Client {
					nodeReader := fake.NewClientBuilder()
					nodeReader.WithObjects(&corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: "foo",
						},
						Spec:   corev1.NodeSpec{},
						Status: corev1.NodeStatus{},
					})
					return nodeReader.Build()
				}(),
				scheme:    nil,
				aliyun:    nil,
				record:    nil,
				trunkMode: false,
				crdMode:   false,
			},
			args: args{
				ctx: context.Background(),
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
						Annotations: map[string]string{
							"k8s.aliyun.com/pod-eni": "true",
						},
					},
					Spec: corev1.PodSpec{
						NodeName: "foo",
					},
					Status: corev1.PodStatus{},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &ReconcilePodENI{
				client:    tt.fields.client,
				scheme:    tt.fields.scheme,
				aliyun:    tt.fields.aliyun,
				record:    tt.fields.record,
				trunkMode: tt.fields.trunkMode,
				crdMode:   tt.fields.crdMode,
			}
			if got := m.podRequirePodENI(tt.args.ctx, tt.args.pod); got != tt.want {
				t.Errorf("podRequirePodENI() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconcilePodENI_deleteMemberENI(t *testing.T) {
	m := mocks.NewInterface(t)
	m.On("DeleteNetworkInterface", mock.Anything, "foo").Return(nil)

	c := &ReconcilePodENI{
		client: fake.NewClientBuilder().Build(),
		aliyun: m,
		record: record.NewFakeRecorder(10),
	}
	err := c.deleteMemberENI(context.Background(), &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
		Spec: v1beta1.PodENISpec{
			Allocations: []v1beta1.Allocation{
				{
					ENI: v1beta1.ENI{ID: "foo"},
				},
			},
			Zone: "",
		},
	})
	assert.NoError(t, err)
}

func TestReconcilePodENI_detachMemberENI(t *testing.T) {
	m := mocks.NewInterface(t)
	m.On("WaitForNetworkInterface", mock.Anything, "foo", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	m.On("DetachNetworkInterface", mock.Anything, "foo", "i-x", "eni-x").Return(nil)

	c := &ReconcilePodENI{
		client: fake.NewClientBuilder().Build(),
		aliyun: m,
		record: record.NewFakeRecorder(10),
	}
	err := c.detachMemberENI(context.Background(), &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
		Spec: v1beta1.PodENISpec{
			Allocations: []v1beta1.Allocation{
				{
					ENI: v1beta1.ENI{ID: "foo"},
				},
			},
			Zone: "",
		},
		Status: v1beta1.PodENIStatus{
			Phase:       v1beta1.ENIPhaseBind,
			InstanceID:  "i-x",
			TrunkENIID:  "eni-x",
			Msg:         "",
			PodLastSeen: metav1.Time{},
			ENIInfos:    nil,
		},
	})
	assert.NoError(t, err)
}

func TestReconcilePodENI_attachENI(t *testing.T) {
	m := mocks.NewInterface(t)
	m.On("WaitForNetworkInterface", mock.Anything, "foo", mock.Anything, mock.Anything, mock.Anything).Return(&aliyunClient.NetworkInterface{
		NetworkInterfaceID: "foo",
	}, nil)
	m.On("AttachNetworkInterface", mock.Anything, "foo", "i-x", "eni-x").Return(nil)

	c := &ReconcilePodENI{
		client: fake.NewClientBuilder().Build(),
		aliyun: m,
		record: record.NewFakeRecorder(10),
	}
	podENI := &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
		Spec: v1beta1.PodENISpec{
			Allocations: []v1beta1.Allocation{
				{
					ENI: v1beta1.ENI{ID: "foo"},
				},
			},
			Zone: "",
		},
		Status: v1beta1.PodENIStatus{
			InstanceID:  "i-x",
			TrunkENIID:  "eni-x",
			Msg:         "",
			PodLastSeen: metav1.Time{},
			ENIInfos:    nil,
		},
	}
	err := c.attachENI(context.Background(), podENI)
	assert.NoError(t, err)

	assert.Equal(t, v1beta1.ENIStatusBind, podENI.Status.ENIInfos["foo"].Status)
}
