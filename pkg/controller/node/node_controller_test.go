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
	"errors"
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
	"k8s.io/client-go/tools/events"
	cc "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	ctrlStatus "github.com/AliyunContainerService/terway/pkg/controller/status"
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

	It("publishes IPv6-only capacity using the IPv6 quota", func() {
		k8sNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ipv6-capacity"}}
		Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, k8sNode)).To(Succeed()) })
		node := &networkv1beta1.Node{Spec: networkv1beta1.NodeSpec{
			ENISpec: &networkv1beta1.ENISpec{EnableIPv6: true},
			NodeCap: networkv1beta1.NodeCap{IPv4PerAdapter: 2, IPv6PerAdapter: 10},
			Flavor: []networkv1beta1.Flavor{{
				NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
				NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
				Count:                       2,
			}},
		}}
		r := &ReconcileNode{client: k8sClient}
		Expect(r.k8sAnno(ctx, k8sNode, node)).To(Succeed())
		Expect(k8sClient.Get(ctx, cc.ObjectKeyFromObject(k8sNode), k8sNode)).To(Succeed())
		Expect(k8sNode.Annotations[string(terwayTypes.NormalIPTypeIPs)]).To(Equal("20"))
	})

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
			record:          events.NewFakeRecorder(1000),
			centralizedIPAM: centralizedIPAM,
			supportEFLO:     supportEFLO,
			nodeStatusCache: ctrlStatus.NewCache[ctrlStatus.NodeStatus](),
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

		Context("capacity annotations", func() {
			for _, centralized := range []bool{false, true} {
				for _, exclusive := range []bool{false, true} {
					It("should report LENI capacity from Spec", func() {
						nodeName := "test-eflo-capacity"
						defer cleanupNode(ctx, nodeName)

						builder := testutil.NewK8sNodeBuilder(nodeName).
							WithEFLO().
							WithInstanceType("eflo.instance").
							WithProviderID("instanceID-capacity")
						if exclusive {
							builder = builder.WithExclusiveENIMode()
						}
						k8sNode := builder.Build()
						Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

						openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).
							Return([]*aliyunClient.NetworkInterface{}, nil).Once()
						efloClient.On("GetNodeInfoForPod", mock.Anything, "instanceID-capacity").
							Return(&eflo.Content{LeniQuota: 4, LeniSipQuota: 10}, nil).Once()

						reconciler := createReconciler(centralized, true)
						request := reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}}
						_, err := reconciler.Reconcile(ctx, request)
						Expect(err).NotTo(HaveOccurred())
						Expect(k8sClient.Get(ctx, request.NamespacedName, k8sNode)).To(Succeed())
						Expect(k8sNode.Annotations).NotTo(HaveKey(string(terwayTypes.NormalIPTypeIPs)))

						// The daemon initializes the allocation Spec after the Node CR is created.
						resource := &networkv1beta1.Node{}
						Expect(k8sClient.Get(ctx, request.NamespacedName, resource)).To(Succeed())
						resource.Spec.ENISpec = &networkv1beta1.ENISpec{
							EnableIPv4:          true,
							SecurityGroupIDs:    []string{"sg-test"},
							VSwitchOptions:      []string{"vsw-test"},
							VSwitchSelectPolicy: networkv1beta1.VSwitchSelectionPolicyRandom,
						}
						resource.Spec.Flavor = []networkv1beta1.Flavor{{
							NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
							NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							Count:                       3,
						}}
						Expect(k8sClient.Update(ctx, resource)).To(Succeed())

						// Existing Spec is sufficient; no further cloud API calls are expected.
						_, err = reconciler.Reconcile(ctx, request)
						Expect(err).NotTo(HaveOccurred())
						Expect(k8sClient.Get(ctx, request.NamespacedName, k8sNode)).To(Succeed())
						expected := "30"
						if exclusive {
							expected = "3"
						}
						Expect(k8sNode.Annotations[string(terwayTypes.NormalIPTypeIPs)]).To(Equal(expected))
					})
				}
			}
		})

		Context("ECS Standard ENI (APIEcs)", func() {
			DescribeTable("should discover or refresh card capabilities", func(suffix string, specificationChanged bool, cards []networkv1beta1.NetworkCard, reported []aliyunClient.NetworkCard, expected []networkv1beta1.NetworkCard) {
				nodeName := "test-eflo-card-refresh-" + suffix
				defer cleanupNode(ctx, nodeName)
				k8sNode := testutil.NewK8sNodeBuilder(nodeName).WithEFLO().WithExclusiveENIMode().WithProviderID("i-xxx").Build()
				Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

				resource := testutil.NewNodeCRDBuilder(nodeName).WithEFLO().WithNetworkCardsCount(len(cards)).Build()
				resource.Annotations[terwayTypes.ENOApi] = terwayTypes.APIEcs
				resource.Spec.NodeCap.NetworkCards = cards
				if specificationChanged {
					resource.Spec.NodeMetadata.InstanceType = "previous-type"
				} else {
					resource.Finalizers = []string{finalizer}
					Expect(resource.Spec.NodeMetadata.InstanceType).To(Equal(k8sNode.Labels[corev1.LabelInstanceTypeStable]))
					Expect(resource.Spec.NodeMetadata.InstanceID).To(Equal(k8sNode.Spec.ProviderID))
					Expect(resource.Spec.NodeMetadata.RegionID).To(Equal(k8sNode.Labels[corev1.LabelTopologyRegion]))
					Expect(resource.Spec.NodeMetadata.ZoneID).To(Equal(k8sNode.Labels[corev1.LabelTopologyZone]))
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				efloController := mocks.NewEFLOControl(GinkgoT())
				openAPI.On("GetEFLOController").Return(efloController).Maybe()
				openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
					{NetworkInterfaceID: "eni-test", Type: aliyunClient.ENITypePrimary},
				}, nil).Once()
				efloController.On("DescribeNode", mock.Anything, mock.Anything).Return(&aliyunClient.DescribeNodeResponse{
					NodeType: "card-refresh-type-" + suffix,
				}, nil).Once()
				efloController.On("DescribeNodeType", mock.Anything, mock.Anything).Return(&aliyunClient.DescribeNodeTypeResponse{
					EniQuantity: 38, EniPrivateIpAddressQuantity: 10, NetworkCardQuantity: len(reported),
					NetworkCards: reported,
				}, nil).Once()

				reconciler := createReconciler(specificationChanged, true)
				if !specificationChanged {
					// A persisted zero count is initialized; it must not trigger automatic discovery.
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}})
					Expect(err).NotTo(HaveOccurred())
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)).To(Succeed())
					verifyNetworkCardsCount(ctx, nodeName, 0)
					Expect(resource.Spec.NodeCap.NetworkCards).To(BeNil())

					reconciler.nodeStatusCache.LoadOrStore(nodeName, ctrlStatus.NewNodeStatus(0))
					// Normal CR deletion must evict the shared placement cache first.
					Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
					_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}})
					Expect(err).NotTo(HaveOccurred())
					_, cached := reconciler.nodeStatusCache.Get(nodeName)
					Expect(cached).To(BeFalse())
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, &networkv1beta1.Node{})).To(Satisfy(k8sErr.IsNotFound))
				}
				previousUID := resource.UID
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}})
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)).To(Succeed())
				if !specificationChanged {
					Expect(resource.UID).NotTo(Equal(previousUID))
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, k8sNode)).To(Succeed())
				}
				Expect(resource.Spec.NodeMetadata.ZoneID).To(Equal(k8sNode.Labels[corev1.LabelTopologyZone]))
				Expect(resource.Spec.NodeMetadata.InstanceType).To(Equal(k8sNode.Labels[corev1.LabelInstanceTypeStable]))
				verifyNetworkCardsCount(ctx, nodeName, len(expected))
				Expect(resource.Spec.NodeCap.NetworkCards).To(Equal(expected))
				limit, err := aliyunClient.GetLimitProvider().GetLimit(efloController, "card-refresh-type-"+suffix)
				Expect(err).NotTo(HaveOccurred())
				Expect(limit.NetworkCards).To(Equal(reported))

				// Persisted capabilities must prevent rediscovery even after recreating the reconciler.
				reconciler = createReconciler(true, true)
				_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}})
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)).To(Succeed())
				verifyNetworkCardsCount(ctx, nodeName, len(expected))
				Expect(resource.Spec.NodeCap.NetworkCards).To(Equal(expected))
			}, Entry("refresh card quantity and indices", "dual", true, []networkv1beta1.NetworkCard{{Index: 0}, {Index: 1}},
				[]aliyunClient.NetworkCard{{Index: 2}, {Index: 0}, {Index: 1}}, []networkv1beta1.NetworkCard{{Index: 2}, {Index: 0}, {Index: 1}}),
				Entry("enable dual-card attachment for legacy nodes on specification updates", "legacy", true, []networkv1beta1.NetworkCard(nil),
					[]aliyunClient.NetworkCard{{Index: 0}, {Index: 1}}, []networkv1beta1.NetworkCard{{Index: 0}, {Index: 1}}),
				Entry("upgrade a single-card node", "single", true, []networkv1beta1.NetworkCard{{Index: 0}},
					[]aliyunClient.NetworkCard{{Index: 0}, {Index: 1}}, []networkv1beta1.NetworkCard{{Index: 0}, {Index: 1}}),
				Entry("retain an explicit single card on specification updates", "single-update", true, []networkv1beta1.NetworkCard{{Index: 0}, {Index: 1}},
					[]aliyunClient.NetworkCard{{Index: 0}}, []networkv1beta1.NetworkCard{{Index: 0}}),
				Entry("clear card mapping when the specification omits card fields", "no-mapping", true, []networkv1beta1.NetworkCard{{Index: 0}, {Index: 1}},
					[]aliyunClient.NetworkCard(nil), []networkv1beta1.NetworkCard(nil)),
				Entry("discover dual cards for legacy nodes after recreating the Node CR", "discover-dual", false, []networkv1beta1.NetworkCard(nil),
					[]aliyunClient.NetworkCard{{Index: 0}, {Index: 1}}, []networkv1beta1.NetworkCard{{Index: 0}, {Index: 1}}),
				Entry("keep omitted card fields empty after recreating the Node CR", "discover-single", false, []networkv1beta1.NetworkCard(nil),
					[]aliyunClient.NetworkCard(nil), []networkv1beta1.NetworkCard(nil)),
				Entry("retain an explicit single card after recreating the Node CR", "discover-explicit-single", false, []networkv1beta1.NetworkCard(nil),
					[]aliyunClient.NetworkCard{{Index: 0}}, []networkv1beta1.NetworkCard{{Index: 0}}),
				Entry("keep zero card capabilities empty after recreating the Node CR", "discover-zero", false, []networkv1beta1.NetworkCard(nil),
					[]aliyunClient.NetworkCard{}, []networkv1beta1.NetworkCard(nil)))

			DescribeTable("should initialize EFLO ECS ENI card capabilities", func(suffix string, reported []aliyunClient.NetworkCard, expected []networkv1beta1.NetworkCard) {
				nodeName := "test-eflo-node-ecs-" + suffix
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
					NodeType: "test-machine-type-normal-" + suffix,
				}, nil).Once()

				efloController.On("DescribeNodeType", mock.Anything, mock.Anything).Return(&aliyunClient.DescribeNodeTypeResponse{
					EniQuantity:                 4,
					EniPrivateIpAddressQuantity: 10,
					EniHighDenseQuantity:        8,
					NetworkCardQuantity:         len(reported),
					NetworkCards:                reported,
				}, nil).Once()

				openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: "eni-test",
						Type:               aliyunClient.ENITypePrimary,
					},
				}, nil).Once()

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
				verifyNetworkCardsCount(ctx, nodeName, len(expected))
				Expect(resource.Spec.NodeCap.NetworkCards).To(Equal(expected))
				// Once discovered, cards and quota are retained without a specification update.
				resource.Spec.NodeCap.Adapters = 38
				resource.Spec.NodeCap.TotalAdapters = 38
				Expect(k8sClient.Update(ctx, resource)).To(Succeed())
				reconciler.nodeStatusCache.LoadOrStore(nodeName, ctrlStatus.NewNodeStatus(0))
				_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}})
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, resource)).To(Succeed())
				Expect(resource.Spec.NodeCap.Adapters).To(Equal(38))
				Expect(resource.Spec.NodeCap.TotalAdapters).To(Equal(38))
				verifyNetworkCardsCount(ctx, nodeName, len(expected))
				Expect(resource.Spec.NodeCap.NetworkCards).To(Equal(expected))
				_, cached := reconciler.nodeStatusCache.Get(nodeName)
				Expect(cached).To(BeTrue())

			}, Entry("omitted single-card fields", "omitted", []aliyunClient.NetworkCard(nil), []networkv1beta1.NetworkCard(nil)),
				Entry("zero cards", "zero", []aliyunClient.NetworkCard{}, []networkv1beta1.NetworkCard(nil)),
				Entry("explicit single card", "single", []aliyunClient.NetworkCard{{Index: 0}}, []networkv1beta1.NetworkCard{{Index: 0}}),
				Entry("dual cards", "dual", []aliyunClient.NetworkCard{{Index: 0}, {Index: 1}}, []networkv1beta1.NetworkCard{{Index: 0}, {Index: 1}}))
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

	Context("useECSLink", func() {
		const nodeName = "test-useECSLink-node"

		AfterEach(func() {
			cleanupNode(ctx, nodeName)
		})

		It("should return true when Primary ENI exists (new ENI link instances)", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-primary",
					Type:               aliyunClient.ENITypePrimary,
				},
				{
					NetworkInterfaceID: "eni-secondary",
					Type:               aliyunClient.ENITypeSecondary,
				},
			}, nil)

			reconciler := createReconciler(false, true)
			result, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("should return true when ENI has migration tags (leni_primary=true && acs:ecs:support_eni=true)", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-secondary-1",
					Type:               aliyunClient.ENITypeSecondary,
					Tags: []ecs.Tag{
						{TagKey: "leni_primary", TagValue: "true"},
						{TagKey: "acs:ecs:support_eni", TagValue: "true"},
					},
				},
				{
					NetworkInterfaceID: "eni-secondary-2",
					Type:               aliyunClient.ENITypeSecondary,
				},
			}, nil)

			reconciler := createReconciler(false, true)
			result, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("should return false when only leni_primary tag exists without acs:ecs:support_eni", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-secondary",
					Type:               aliyunClient.ENITypeSecondary,
					Tags: []ecs.Tag{
						{TagKey: "leni_primary", TagValue: "true"},
					},
				},
			}, nil)

			reconciler := createReconciler(false, true)
			result, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("should return false when only acs:ecs:support_eni tag exists without leni_primary", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-secondary",
					Type:               aliyunClient.ENITypeSecondary,
					Tags: []ecs.Tag{
						{TagKey: "acs:ecs:support_eni", TagValue: "true"},
					},
				},
			}, nil)

			reconciler := createReconciler(false, true)
			result, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("should return false when no Primary ENI and no migration tags (old ENO link instances)", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-secondary-1",
					Type:               aliyunClient.ENITypeSecondary,
				},
				{
					NetworkInterfaceID: "eni-secondary-2",
					Type:               aliyunClient.ENITypeSecondary,
				},
			}, nil)

			reconciler := createReconciler(false, true)
			result, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("should return false when no ENIs exist", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil)

			reconciler := createReconciler(false, true)
			result, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})

		It("should return error when API call fails", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil, errors.New("API error"))

			reconciler := createReconciler(false, true)
			_, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("API error"))
		})

		It("should return true when migration tags exist on second ENI", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-secondary-1",
					Type:               aliyunClient.ENITypeSecondary,
				},
				{
					NetworkInterfaceID: "eni-secondary-2",
					Type:               aliyunClient.ENITypeSecondary,
					Tags: []ecs.Tag{
						{TagKey: "leni_primary", TagValue: "true"},
						{TagKey: "acs:ecs:support_eni", TagValue: "true"},
						{TagKey: "other_tag", TagValue: "other_value"},
					},
				},
			}, nil)

			reconciler := createReconciler(false, true)
			result, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeTrue())
		})

		It("should return false when tag values are not 'true'", func() {
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-secondary",
					Type:               aliyunClient.ENITypeSecondary,
					Tags: []ecs.Tag{
						{TagKey: "leni_primary", TagValue: "false"},
						{TagKey: "acs:ecs:support_eni", TagValue: "true"},
					},
				},
			}, nil)

			reconciler := createReconciler(false, true)
			result, err := reconciler.useECSLink(ctx, "test-instance-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeFalse())
		})
	})

	Context("Delete Node", func() {
		const nodeName = "test-delete-node"

		AfterEach(func() {
			cleanupNode(ctx, nodeName)
		})

		It("should return nil when centralizedIPAM is true", func() {
			reconciler := createReconciler(true, false)
			reconciler.nodeStatusCache.LoadOrStore(nodeName, ctrlStatus.NewNodeStatus(0))
			_, err := reconciler.delete(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())
			_, cached := reconciler.nodeStatusCache.Get(nodeName)
			Expect(cached).To(BeFalse())
		})

		It("should handle node not found", func() {
			reconciler := createReconciler(false, false)
			_, err := reconciler.delete(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should remove finalizer when node exists", func() {
			crNode := testutil.NewNodeCRDBuilder(nodeName).Build()
			crNode.Finalizers = []string{finalizer}
			Expect(k8sClient.Create(ctx, crNode)).To(Succeed())

			reconciler := createReconciler(false, false)
			reconciler.nodeStatusCache.LoadOrStore(nodeName, &ctrlStatus.NodeStatus{})

			_, err := reconciler.delete(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			_, ok := reconciler.nodeStatusCache.Get(nodeName)
			Expect(ok).To(BeFalse())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, crNode)
			Expect(err).NotTo(HaveOccurred())
			Expect(crNode.Finalizers).NotTo(ContainElement(finalizer))
		})
	})
})
