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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

var _ = Describe("Node Controller", func() {
	Context("no-op", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}
		node := &corev1.Node{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Node")
			err := k8sClient.Get(ctx, typeNamespacedName, node)
			if err != nil && errors.IsNotFound(err) {
				resource := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
						Labels: map[string]string{
							"topology.kubernetes.io/zone":   "cn-hangzhou-k",
							"topology.kubernetes.io/region": "cn-hangzhou",
						},
					},
					Spec: corev1.NodeSpec{
						ProviderID: "cn-hangzhou.instance-id",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &corev1.Node{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Node")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		switchPool, err := vswpool.NewSwitchPool(100, "10m")
		Expect(err).NotTo(HaveOccurred())

		disable := false
		controlplane.SetConfig(&controlplane.Config{EnableTrunk: &disable})

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &ReconcileNode{
				client: k8sClient,
				swPool: switchPool,
				aliyun: openAPI,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
