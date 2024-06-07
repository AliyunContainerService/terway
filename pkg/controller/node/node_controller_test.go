/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package node

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	cc "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var _ = Describe("Node Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}

		BeforeEach(func() {
			By("create a k8s node")
			k8sNode := &corev1.Node{}
			err := k8sClient.Get(ctx, typeNamespacedName, k8sNode)
			if err != nil && errors.IsNotFound(err) {
				resource := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
						Labels: map[string]string{
							"node.kubernetes.io/instance-type": "xxx",
							"topology.kubernetes.io/zone":      "z1",
							"topology.kubernetes.io/region":    "region",
						},
					},
					Spec: corev1.NodeSpec{
						ProviderID: "xx.xx",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			err := k8sClient.Delete(ctx, &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName},
			})
			if err != nil {
				Expect(err).To(BeNil())
			}

			crNode := &networkv1beta1.Node{}

			err = k8sClient.Get(ctx, typeNamespacedName, crNode)
			if err != nil && errors.IsNotFound(err) {
				return
			}
			if err != nil {
				Expect(err).To(BeNil())
			}

			patch := cc.MergeFrom(crNode.DeepCopy())
			controllerutil.RemoveFinalizer(crNode, finalizer)

			err = k8sClient.Patch(ctx, crNode, patch)
			if err != nil {
				Expect(err).To(BeNil())
			}

			_ = k8sClient.Delete(ctx, &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName},
			})
			Eventually(func() bool {
				crNode := &networkv1beta1.Node{}
				err := k8sClient.Get(context.TODO(), typeNamespacedName, crNode)
				return k8sErr.IsNotFound(err)
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())
		})

		It("should successfully create cr", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ReconcileNode{
				client: k8sClient,
				scheme: k8sClient.Scheme(),
				aliyun: aliyun,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			resource := &networkv1beta1.Node{}
			err = k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())
			Expect(resource.Labels["name"]).To(Equal(resourceName))
		})

		It("should successfully create cr", func() {
			resource := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: resourceName,
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{},
					NodeCap:      networkv1beta1.NodeCap{},
					ENISpec: &networkv1beta1.ENISpec{
						Tag:                 nil,
						TagFilter:           nil,
						VSwitchOptions:      nil,
						SecurityGroupIDs:    nil,
						ResourceGroupID:     "",
						EnableIPv4:          true,
						EnableIPv6:          false,
						EnableERDMA:         false,
						EnableTrunk:         true,
						VSwitchSelectPolicy: "",
					},
					Pool:   nil,
					Flavor: nil,
				},
			}

			By("create cr")
			err := k8sClient.Create(ctx, resource)
			Expect(err).NotTo(HaveOccurred())

			update := resource.DeepCopy()
			_, err = controllerutil.CreateOrPatch(ctx, k8sClient, update, func() error {
				update.Status = networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.Time{},
					LastSyncOpenAPITime: metav1.Time{},
					NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
						"eni-1": {
							ID:                          "eni-1",
							NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							SecurityGroupIDs:            []string{"ff"},
						},
					},
				}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the created resource")
			controllerReconciler := &ReconcileNode{
				client: k8sClient,
				scheme: k8sClient.Scheme(),
				aliyun: aliyun,
			}

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			k8sNode := &corev1.Node{}
			err = k8sClient.Get(ctx, typeNamespacedName, k8sNode)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sNode.Annotations["k8s.aliyun.com/trunk-on"]).To(Equal("eni-1"))
		})
	})
})
