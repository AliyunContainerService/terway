//go:build e2e

package trunk

import (
	"context"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/utils"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func Test_10KPod(t *testing.T) {
	restConf := ctrl.GetConfigOrDie()
	ns := "perf"
	utils.RegisterClients(restConf)
	ctx := context.Background()

	_, _ = utils.K8sClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns, Labels: map[string]string{"trunk": "trunk", "perf": "perf"}},
	}, metav1.CreateOptions{})

	pn := newPodNetworking("pn", nil, nil, nil,
		&metav1.LabelSelector{MatchLabels: map[string]string{"trunk": "trunk"}})
	_, _ = utils.NetworkClient.NetworkV1beta1().PodNetworkings().Create(ctx, pn, metav1.CreateOptions{})

	for i := 0; i < 10000; i++ {
		name := uuid.NewString()
		peni, err := utils.NetworkClient.NetworkV1beta1().PodENIs(ns).Create(ctx, &v1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{
				terwayTypes.PodNetworking: "pn",
				terwayTypes.PodENI:        "true",
			}},
			Spec: v1beta1.PodENISpec{
				Allocations: []v1beta1.Allocation{
					{
						AllocationType: v1beta1.AllocationType{},
						ENI: v1beta1.ENI{
							ID:               uuid.NewString(),
							MAC:              uuid.NewString(),
							Zone:             "",
							VSwitchID:        uuid.NewString(),
							SecurityGroupIDs: []string{uuid.NewString()},
						},
						IPv4:         "127.0.0.1",
						IPv6:         "::1",
						IPv4CIDR:     "127.0.0.1/8",
						IPv6CIDR:     "::1/8",
						Interface:    "eth0",
						DefaultRoute: false,
					},
				},
				Zone: "cn-hangzhou-x",
			},
		}, metav1.CreateOptions{})

		if err != nil {
			t.Error(err)
			continue
		}
		update := peni.DeepCopy()
		update.Status.Phase = v1beta1.ENIPhaseBind
		update.Status.PodLastSeen = metav1.NewTime(time.Now().Add(time.Hour))
		_, _ = utils.NetworkClient.NetworkV1beta1().PodENIs(update.Namespace).UpdateStatus(ctx, update, metav1.UpdateOptions{})

		_, _ = utils.K8sClient.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:            "foo",
						Image:           "registry.cn-hangzhou.aliyuncs.com/acs/pause:3.2",
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"/pause"},
					},
				},
			},
		}, metav1.CreateOptions{})
	}
}
