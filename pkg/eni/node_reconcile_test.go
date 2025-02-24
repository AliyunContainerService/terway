package eni

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance/mocks"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var _ = Describe("Node controller", func() {
	Context("Create Node", func() {
		const (
			nodeName = "foo"
		)

		It("New EFLO node", func() {
			ctx := context.Background()
			ins := mocks.NewInterface(GinkgoT())
			ins.On("GetInstanceID").Return("i-foo", nil)
			ins.On("GetRegionID").Return("cn-hangzhou", nil)
			ins.On("GetInstanceType").Return("ecs", nil)
			ins.On("GetZoneID").Return("cn-hangzhou-i", nil)
			instance.Init(ins)

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"alibabacloud.com/lingjun-worker": "true",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": "{ \"vswitches\": {\"cn-hangzhou-i\":[\"vsw-xx\"] } ,\"security_group\":\"sg-x\" , \"vswitch_selection_policy\": \"ordered\" }",
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-foo",
						InstanceType: "ecs",
						ZoneID:       "cn-hangzhou-i",
					},
					ENISpec: &networkv1beta1.ENISpec{
						VSwitchOptions:      []string{"vsw-xx"},
						SecurityGroupIDs:    []string{"foo"},
						VSwitchSelectPolicy: "ordered",
					},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			By("Reconciling the created resource")

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			Expect(err).NotTo(HaveOccurred())

			Expect(node.Labels["alibabacloud.com/lingjun-worker"]).To(Equal("true"))
			Expect(node.Spec.NodeMetadata.RegionID).To(Equal("cn-hangzhou"))
			Expect(node.Spec.NodeMetadata.InstanceType).To(Equal("ecs"))
			Expect(node.Spec.NodeMetadata.InstanceID).To(Equal("i-foo"))
			Expect(node.Spec.NodeMetadata.ZoneID).To(Equal("cn-hangzhou-i"))
		})
	})
})
