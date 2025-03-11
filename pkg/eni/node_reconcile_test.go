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

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var _ = Describe("Node controller", func() {
	Context("Create Node", func() {
		const (
			nodeName = "foo"
		)

		It("New EFLO node", func() {
			ctx := context.Background()

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
					Labels: map[string]string{
						"alibabacloud.com/lingjun-worker": "true",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-foo",
						InstanceType: "ecs",
						ZoneID:       "cn-hangzhou-i",
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

			Expect(node.Spec.NodeMetadata.RegionID).To(Equal("cn-hangzhou"))
			Expect(node.Spec.NodeMetadata.InstanceType).To(Equal("ecs"))
			Expect(node.Spec.NodeMetadata.InstanceID).To(Equal("i-foo"))
			Expect(node.Spec.NodeMetadata.ZoneID).To(Equal("cn-hangzhou-i"))

			Expect(node.Spec.ENISpec, Not(BeNil()))
			Expect(node.Spec.ENISpec.VSwitchOptions).To(Equal([]string{"vsw-xx"}))
			Expect(node.Spec.ENISpec.SecurityGroupIDs).To(Equal([]string{"sg-x"}))
			Expect(node.Spec.ENISpec.VSwitchSelectPolicy).To(Equal(networkv1beta1.VSwitchSelectionPolicyMost))

			Expect(node.Spec.Pool, BeNil())
		})
	})
})
