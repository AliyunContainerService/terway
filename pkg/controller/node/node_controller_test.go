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

	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	cc "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/internal/testutil"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

var _ = Describe("Node Controller", func() {
	var (
		ctx        context.Context
		openAPI    *mocks.OpenAPI
		vpcClient  *mocks.VPC
		ecsClient  *mocks.ECS
		efloClient *mocks.EFLO
	)

	BeforeEach(func() {
		ctx = context.Background()
		openAPI = mocks.NewOpenAPI(GinkgoT())
		vpcClient = mocks.NewVPC(GinkgoT())
		ecsClient = mocks.NewECS(GinkgoT())
		efloClient = mocks.NewEFLO(GinkgoT())

		openAPI.On("GetVPC").Return(vpcClient).Maybe()
		openAPI.On("GetECS").Return(ecsClient).Maybe()
		openAPI.On("GetEFLO").Return(efloClient).Maybe()
	})

	// Helper function to create reconciler
	createReconciler := func(centralizedIPAM, supportEFLO bool) *ReconcileNode {
		return &ReconcileNode{
			client:          k8sClient,
			scheme:          k8sClient.Scheme(),
			aliyun:          openAPI,
			record:          record.NewFakeRecorder(1000),
			centralizedIPAM: centralizedIPAM,
			supportEFLO:     supportEFLO,
		}
	}

	// Helper function to cleanup resources
	cleanupNode := func(ctx context.Context, nodeName string) {
		_ = k8sClient.Delete(ctx, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		})

		crNode := &networkv1beta1.Node{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, crNode)
		if err == nil {
			patch := cc.MergeFrom(crNode.DeepCopy())
			controllerutil.RemoveFinalizer(crNode, finalizer)
			_ = k8sClient.Patch(ctx, crNode, patch)

			_ = k8sClient.Delete(ctx, &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			})

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, &networkv1beta1.Node{})
				return k8sErr.IsNotFound(err)
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())
		}
	}

	// Helper to verify NetworkCardsCount
	verifyNetworkCardsCount := func(ctx context.Context, nodeName string, expectedCount int) {
		resource := &networkv1beta1.Node{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)
		Expect(err).NotTo(HaveOccurred())
		Expect(resource.Spec.NodeCap.NetworkCardsCount).NotTo(BeNil())
		Expect(*resource.Spec.NodeCap.NetworkCardsCount).To(Equal(expectedCount))
	}

	Context("Test init", func() {
		It("register should succeed", func() {
			v, ok := register.Controllers[ControllerName]
			Expect(ok).To(BeTrue())

			mgr, ctx := testutil.NewManager(cfg, openAPI, k8sClient)
			err := v.Creator(mgr, ctx)

			Expect(err).To(Not(HaveOccurred()))
		})
	})

	Context("ECS Node", func() {
		const nodeName = "test-ecs-node"

		BeforeEach(func() {
			ecsClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return([]ecs.InstanceType{
				{
					EniTotalQuantity:            5,
					EniQuantity:                 4,
					InstanceTypeId:              "ecs.g7.large",
					EniTrunkSupported:           true,
					EniPrivateIpAddressQuantity: 10,
					NetworkCards: ecs.NetworkCards{
						NetworkCardInfo: []ecs.NetworkCardInfo{
							{NetworkCardIndex: 0},
							{NetworkCardIndex: 1},
						},
					},
				},
			}, nil).Maybe()
		})

		AfterEach(func() {
			cleanupNode(ctx, nodeName)
		})

		Context("Shared ENI Mode (Default)", func() {
			It("should create Node CR with correct NetworkCardsCount", func() {
				k8sNode := testutil.NewK8sNodeBuilder(nodeName).
					WithInstanceType("ecs.g7.large").
					Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				reconciler := createReconciler(true, false)
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				resource := &networkv1beta1.Node{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)
				Expect(err).NotTo(HaveOccurred())
				Expect(resource.Labels["name"]).To(Equal(nodeName))
				verifyNetworkCardsCount(ctx, nodeName, 2)
			})
		})

		Context("Exclusive ENI Mode", func() {
			It("should create Node CR with NetworkCardsCount and correct allocatable resources", func() {
				k8sNode := testutil.NewK8sNodeBuilder(nodeName).
					WithInstanceType("ecs.g7.large").
					WithExclusiveENIMode().
					Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				resource := testutil.NewNodeCRDBuilder(nodeName).
					WithEnableTrunk(false).
					WithFlavor(networkv1beta1.Flavor{
						NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
						Count:                       2,
					}).
					Build()
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				resource.Status = networkv1beta1.NodeStatus{
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-1": {
							ID:                          "eni-1",
							NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Status:                      "InUse",
							IPv4: map[string]*networkv1beta1.IP{
								"192.168.0.1": {IP: "192.168.0.1", Status: "Valid"},
							},
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())

				reconciler := createReconciler(true, false)
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				verifyNetworkCardsCount(ctx, nodeName, 2)

				k8sNode = &corev1.Node{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, k8sNode)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sNode.Annotations["k8s.aliyun.com/trunk-on"]).To(Equal(""))
				Expect(k8sNode.Annotations["k8s.aliyun.com/max-available-ip"]).To(Equal("2"))

				quantity := k8sNode.Status.Allocatable["aliyun/eni"]
				Expect(quantity.Value()).To(Equal(int64(2)))

				_, ok := k8sNode.Status.Allocatable["aliyun/member-eni"]
				Expect(ok).To(BeFalse())
			})
		})

		Context("Trunk ENI Mode", func() {
			It("should set trunk annotation and calculate max-available-ip correctly", func() {
				k8sNode := testutil.NewK8sNodeBuilder(nodeName).
					WithInstanceType("ecs.g7.large").
					Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				resource := testutil.NewNodeCRDBuilder(nodeName).
					WithEnableTrunk(true).
					WithFlavor(networkv1beta1.Flavor{
						NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
						Count:                       2,
					}).
					Build()
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				resource.Status = networkv1beta1.NodeStatus{
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-trunk": {
							ID:                          "eni-trunk",
							NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Status:                      "InUse",
							IPv4: map[string]*networkv1beta1.IP{
								"192.168.0.1": {IP: "192.168.0.1", Status: "Valid"},
							},
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())

				reconciler := createReconciler(true, false)
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				verifyNetworkCardsCount(ctx, nodeName, 2)

				k8sNode = &corev1.Node{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, k8sNode)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sNode.Annotations["k8s.aliyun.com/trunk-on"]).To(Equal("eni-trunk"))
				Expect(k8sNode.Annotations["k8s.aliyun.com/max-available-ip"]).To(Equal("20"))
			})

			It("should handle multiple flavors with trunk", func() {
				k8sNode := testutil.NewK8sNodeBuilder(nodeName).
					WithInstanceType("ecs.g7.large").
					Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				resource := testutil.NewNodeCRDBuilder(nodeName).
					WithEnableTrunk(true).
					WithFlavor(
						networkv1beta1.Flavor{
							NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Count:                       2,
						},
						networkv1beta1.Flavor{
							NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Count:                       1,
						},
					).
					Build()
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				resource.Status = networkv1beta1.NodeStatus{
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-trunk": {
							ID:                          "eni-trunk",
							NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Status:                      "InUse",
							IPv4: map[string]*networkv1beta1.IP{
								"192.168.0.1": {IP: "192.168.0.1", Status: "Valid"},
							},
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, resource)).To(Succeed())

				reconciler := createReconciler(true, false)
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				verifyNetworkCardsCount(ctx, nodeName, 2)

				k8sNode = &corev1.Node{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, k8sNode)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sNode.Annotations["k8s.aliyun.com/trunk-on"]).To(Equal("eni-trunk"))
				Expect(k8sNode.Annotations["k8s.aliyun.com/max-available-ip"]).To(Equal("30"))
			})
		})
	})

	Context("EFLO Node (LingJun)", func() {
		Context("Standard LENI (without exclusive ENI)", func() {
			It("should create EFLO node with NetworkCardsCount=0 for standard LENI", func() {
				nodeName := "test-eflo-node-leni"
				defer cleanupNode(ctx, nodeName)

				k8sNode := testutil.NewK8sNodeBuilder(nodeName).
					WithEFLO().
					WithInstanceType("eflo.instance").
					WithProviderID("instanceID-leni").
					WithRegion("regionID").
					WithZone("zoneID").
					Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				efloClient.On("GetNodeInfoForPod", mock.Anything, "instanceID-leni").Return(&eflo.Content{
					LeniQuota:    20,
					LeniSipQuota: 10,
					HdeniQuota:   0,
				}, nil)
				openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil)

				reconciler := createReconciler(false, true)
				// First reconcile to create the Node CRD
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				// Second reconcile to update NodeCap
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				resource := &networkv1beta1.Node{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)
				Expect(err).NotTo(HaveOccurred())
				Expect(resource.Labels["name"]).To(Equal(nodeName))
				Expect(resource.Labels["alibabacloud.com/lingjun-worker"]).To(Equal("true"))

				// Print debug info
				GinkgoWriter.Printf("NodeCap: %+v\n", resource.Spec.NodeCap)
				GinkgoWriter.Printf("Metadata: %+v\n", resource.Spec.NodeMetadata)

				Expect(resource.Spec.NodeCap.Adapters).To(Equal(20))
				Expect(resource.Spec.NodeCap.TotalAdapters).To(Equal(20))
				Expect(resource.Spec.NodeCap.IPv4PerAdapter).To(Equal(10))
				verifyNetworkCardsCount(ctx, nodeName, 0)
			})
		})

		Context("ECS Standard ENI (APIEcs)", func() {
			It("should create EFLO node with ECS API and NetworkCardsCount=0", func() {
				nodeName := "test-eflo-node-ecs"
				defer cleanupNode(ctx, nodeName)

				k8sNode := testutil.NewK8sNodeBuilder(nodeName).
					WithEFLO().
					WithExclusiveENIMode().
					WithInstanceType("eflo.instance").
					WithProviderID("instanceID-ecs").
					Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				efloController := mocks.NewEFLOControl(GinkgoT())
				openAPI.On("GetEFLOController").Return(efloController).Maybe()

				efloController.On("DescribeNode", mock.Anything, mock.Anything).Return(&aliyunClient.DescribeNodeResponse{
					NodeType: "test-machine-type-normal",
				}, nil).Maybe()

				efloController.On("DescribeNodeType", mock.Anything, mock.Anything).Return(&aliyunClient.DescribeNodeTypeResponse{
					EniQuantity:                 4,
					EniPrivateIpAddressQuantity: 10,
					EniHighDenseQuantity:        8,
				}, nil).Maybe()

				openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: "eni-test",
						Type:               aliyunClient.ENITypePrimary,
					},
				}, nil).Maybe()

				reconciler := createReconciler(true, true)
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				resource := &networkv1beta1.Node{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)
				Expect(err).NotTo(HaveOccurred())

				Expect(resource.Spec.NodeCap.Adapters).To(Equal(4))
				Expect(resource.Spec.NodeCap.TotalAdapters).To(Equal(4))
				Expect(resource.Spec.NodeCap.IPv4PerAdapter).To(Equal(10))
				Expect(resource.Annotations[terwayTypes.ENOApi]).To(Equal(terwayTypes.APIEcs))
				Expect(resource.Labels["k8s.aliyun.com/exclusive-mode-eni-type"]).To(Equal("eniOnly"))
				verifyNetworkCardsCount(ctx, nodeName, 0)
			})
		})

		Context("ECS High Density ENI (APIEcsHDeni)", func() {
			It("should create EFLO node with high density ECS API and NetworkCardsCount=0", func() {
				nodeName := "test-eflo-node-ecs-hd"
				defer cleanupNode(ctx, nodeName)

				k8sNode := testutil.NewK8sNodeBuilder(nodeName).
					WithEFLO().
					WithExclusiveENIMode().
					WithInstanceType("eflo.instance").
					WithProviderID("instanceID-ecs-hd").
					Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				efloController := mocks.NewEFLOControl(GinkgoT())
				openAPI.On("GetEFLOController").Return(efloController).Maybe()

				efloController.On("DescribeNode", mock.Anything, mock.Anything).Return(&aliyunClient.DescribeNodeResponse{
					NodeType: "test-machine-type-hd",
				}, nil).Maybe()

				efloController.On("DescribeNodeType", mock.Anything, mock.Anything).Return(&aliyunClient.DescribeNodeTypeResponse{
					EniQuantity:                 1,
					EniPrivateIpAddressQuantity: 10,
					EniHighDenseQuantity:        8,
				}, nil).Maybe()

				openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: "eni-test",
						Type:               aliyunClient.ENITypePrimary,
					},
				}, nil).Maybe()

				reconciler := createReconciler(false, true)
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				resource := &networkv1beta1.Node{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)
				Expect(err).NotTo(HaveOccurred())

				Expect(resource.Spec.NodeCap.Adapters).To(Equal(8))
				Expect(resource.Spec.NodeCap.TotalAdapters).To(Equal(8))
				Expect(resource.Annotations[terwayTypes.ENOApi]).To(Equal(terwayTypes.APIEcsHDeni))
				verifyNetworkCardsCount(ctx, nodeName, 0)
			})
		})

		Context("ENO High Density (APIEnoHDeni) - Fallback", func() {
			It("should fallback to ENO high density when LeniQuota is low", func() {
				nodeName := "test-eflo-node-eno-hd"
				defer cleanupNode(ctx, nodeName)

				k8sNode := testutil.NewK8sNodeBuilder(nodeName).
					WithEFLO().
					WithExclusiveENIMode().
					WithInstanceType("eflo.instance").
					WithProviderID("instanceID-eno-hd").
					Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				efloClient.On("GetNodeInfoForPod", mock.Anything, "instanceID-eno-hd").Return(&eflo.Content{
					LeniQuota:   1,
					LniSipQuota: 10,
					HdeniQuota:  16,
				}, nil).Maybe()
				openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil).Maybe()

				reconciler := createReconciler(true, true)
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: nodeName},
				})
				Expect(err).NotTo(HaveOccurred())

				resource := &networkv1beta1.Node{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)
				Expect(err).NotTo(HaveOccurred())

				Expect(resource.Spec.NodeCap.Adapters).To(Equal(16))
				Expect(resource.Spec.NodeCap.TotalAdapters).To(Equal(16))
				Expect(resource.Annotations[terwayTypes.ENOApi]).To(Equal(terwayTypes.APIEnoHDeni))
				Expect(resource.Labels["k8s.aliyun.com/exclusive-mode-eni-type"]).To(Equal("eniOnly"))
				verifyNetworkCardsCount(ctx, nodeName, 0)
			})
		})
	})
})
