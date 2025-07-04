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

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	cc "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var _ = Describe("Node Controller", func() {
	var (
		openAPI    *mocks.OpenAPI
		vpcClient  *mocks.VPC
		ecsClient  *mocks.ECS
		efloClient *mocks.EFLO
	)

	BeforeEach(func() {
		openAPI = mocks.NewOpenAPI(GinkgoT())
		vpcClient = mocks.NewVPC(GinkgoT())
		ecsClient = mocks.NewECS(GinkgoT())
		efloClient = mocks.NewEFLO(GinkgoT())

		openAPI.On("GetVPC").Return(vpcClient).Maybe()
		openAPI.On("GetECS").Return(ecsClient).Maybe()
		openAPI.On("GetEFLO").Return(efloClient).Maybe()
	})

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
			if err != nil && k8sErr.IsNotFound(err) {
				resource := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
						Labels: map[string]string{
							"node.kubernetes.io/instance-type": "instanceType",
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
			ecsClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return([]ecs.InstanceType{
				{
					EniTotalQuantity:            5,
					EniQuantity:                 4,
					InstanceTypeId:              "instanceType",
					EniTrunkSupported:           true,
					EniPrivateIpAddressQuantity: 10,
				},
			}, nil).Maybe()

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
			if err != nil && k8sErr.IsNotFound(err) {
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
				client:          k8sClient,
				scheme:          k8sClient.Scheme(),
				aliyun:          openAPI,
				centralizedIPAM: true,
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
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "foo",
						InstanceType: "foo",
						InstanceID:   "foo",
						ZoneID:       "foo",
					},
					NodeCap: networkv1beta1.NodeCap{},
					ENISpec: &networkv1beta1.ENISpec{
						Tag:                 nil,
						TagFilter:           nil,
						VSwitchOptions:      []string{"foo"},
						SecurityGroupIDs:    []string{"foo"},
						ResourceGroupID:     "",
						EnableIPv4:          true,
						EnableIPv6:          false,
						EnableERDMA:         false,
						EnableTrunk:         true,
						VSwitchSelectPolicy: "ordered",
					},
					Pool: nil,
					Flavor: []networkv1beta1.Flavor{
						{
							NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Count:                       2,
						},
					},
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
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-1": {
							ID:                          "eni-1",
							NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							SecurityGroupIDs:            []string{"ff"},
							Status:                      "InUse",
							IPv4: map[string]*networkv1beta1.IP{
								"192.168.0.1": {
									IP:     "192.168.0.1",
									Status: "Valid",
								},
							},
						},
					},
				}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the created resource")
			controllerReconciler := &ReconcileNode{
				client:          k8sClient,
				scheme:          k8sClient.Scheme(),
				aliyun:          openAPI,
				centralizedIPAM: true,
			}

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			k8sNode := &corev1.Node{}
			err = k8sClient.Get(ctx, typeNamespacedName, k8sNode)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sNode.Annotations["k8s.aliyun.com/trunk-on"]).To(Equal("eni-1"))
			Expect(k8sNode.Annotations["k8s.aliyun.com/max-available-ip"]).To(Equal("20"))
		})

		It("should successfully create cr", func() {
			resource := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: resourceName,
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "foo",
						InstanceType: "foo",
						InstanceID:   "foo",
						ZoneID:       "foo",
					},
					NodeCap: networkv1beta1.NodeCap{},
					ENISpec: &networkv1beta1.ENISpec{
						Tag:                 nil,
						TagFilter:           nil,
						VSwitchOptions:      []string{"foo"},
						SecurityGroupIDs:    []string{"foo"},
						ResourceGroupID:     "",
						EnableIPv4:          true,
						EnableIPv6:          false,
						EnableERDMA:         false,
						EnableTrunk:         true,
						VSwitchSelectPolicy: "ordered",
					},
					Pool: nil,
					Flavor: []networkv1beta1.Flavor{
						{
							NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Count:                       2,
						},
						{
							NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Count:                       1,
						},
					},
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
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-1": {
							ID:                          "eni-1",
							NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							SecurityGroupIDs:            []string{"ff"},
							Status:                      "InUse",
							IPv4: map[string]*networkv1beta1.IP{
								"192.168.0.1": {
									IP:     "192.168.0.1",
									Status: "Valid",
								},
							}},
					},
				}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the created resource")
			controllerReconciler := &ReconcileNode{
				client:          k8sClient,
				scheme:          k8sClient.Scheme(),
				aliyun:          openAPI,
				centralizedIPAM: true,
			}

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			k8sNode := &corev1.Node{}
			err = k8sClient.Get(ctx, typeNamespacedName, k8sNode)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sNode.Annotations["k8s.aliyun.com/trunk-on"]).To(Equal("eni-1"))
			Expect(k8sNode.Annotations["k8s.aliyun.com/max-available-ip"]).To(Equal("30"))
		})
	})

	Context("EFLO node", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}

		BeforeEach(func() {
			By("create a k8s node")
			k8sNode := &corev1.Node{}
			err := k8sClient.Get(ctx, typeNamespacedName, k8sNode)
			if err != nil && k8sErr.IsNotFound(err) {
				resource := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
						Labels: map[string]string{
							"alibabacloud.com/lingjun-worker":  "true",
							"node.kubernetes.io/instance-type": "instanceType",
							"topology.kubernetes.io/region":    "regionID",
							"topology.kubernetes.io/zone":      "zoneID",
						},
					},

					Spec: corev1.NodeSpec{
						ProviderID: "instanceID",
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
			if err != nil && k8sErr.IsNotFound(err) {
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

		It("new eflo node", func() {
			By("Reconciling the created resource")

			efloClient.On("GetNodeInfoForPod", mock.Anything, "instanceID").Return(&eflo.Content{
				LeniQuota:   20,
				LniSipQuota: 10,
			}, nil).Maybe()

			controllerReconciler := &ReconcileNode{
				client:          k8sClient,
				scheme:          k8sClient.Scheme(),
				aliyun:          openAPI,
				supportEFLO:     true,
				centralizedIPAM: true,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			resource := &networkv1beta1.Node{}
			err = k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())
			Expect(resource.Labels["name"]).To(Equal(resourceName))
			Expect(resource.Labels["alibabacloud.com/lingjun-worker"]).To(Equal("true"))

			Expect(resource.Spec.NodeCap.Adapters, 20)
			Expect(resource.Spec.NodeCap.TotalAdapters, 20)
			Expect(resource.Spec.NodeCap.IPv4PerAdapter, 10)
		})
	})

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
			if err != nil && k8sErr.IsNotFound(err) {
				resource := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
						Labels: map[string]string{
							"node.kubernetes.io/instance-type":       "instanceType",
							"topology.kubernetes.io/zone":            "z1",
							"topology.kubernetes.io/region":          "region",
							"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly",
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
			if err != nil && k8sErr.IsNotFound(err) {
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
			By("empty node")
			controllerReconciler := &ReconcileNode{
				client:          k8sClient,
				scheme:          k8sClient.Scheme(),
				aliyun:          openAPI,
				centralizedIPAM: true,
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
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "foo",
						InstanceType: "foo",
						InstanceID:   "foo",
						ZoneID:       "foo",
					},
					NodeCap: networkv1beta1.NodeCap{},
					ENISpec: &networkv1beta1.ENISpec{
						Tag:                 nil,
						TagFilter:           nil,
						VSwitchOptions:      []string{"foo"},
						SecurityGroupIDs:    []string{"foo"},
						ResourceGroupID:     "",
						EnableIPv4:          true,
						EnableIPv6:          false,
						EnableERDMA:         false,
						EnableTrunk:         true,
						VSwitchSelectPolicy: "ordered",
					},
					Pool: nil,
					Flavor: []networkv1beta1.Flavor{
						{
							NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Count:                       2,
						},
					},
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
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-1": {
							ID:                          "eni-1",
							NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							SecurityGroupIDs:            []string{"ff"},
							Status:                      "InUse",
							IPv4: map[string]*networkv1beta1.IP{
								"192.168.0.1": {
									IP:     "192.168.0.1",
									Status: "Valid",
								},
							},
						},
					},
				}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the created resource")
			controllerReconciler := &ReconcileNode{
				client:          k8sClient,
				scheme:          k8sClient.Scheme(),
				aliyun:          openAPI,
				centralizedIPAM: true,
			}

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			k8sNode := &corev1.Node{}
			err = k8sClient.Get(ctx, typeNamespacedName, k8sNode)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sNode.Annotations["k8s.aliyun.com/trunk-on"]).To(Equal(""))
			Expect(k8sNode.Annotations["k8s.aliyun.com/max-available-ip"]).To(Equal("2"))

			quantity := k8sNode.Status.Allocatable["aliyun/eni"]
			quantity.Value()
			Expect(quantity.Value()).To(Equal(int64(2)))

			_, ok := k8sNode.Status.Allocatable["aliyun/member-eni"]
			Expect(ok).To(BeFalse())
		})
	})
})
