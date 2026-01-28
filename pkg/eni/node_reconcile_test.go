package eni

import (
	"context"
	"time"

	"github.com/agiledragon/gomonkey/v2"
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
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

var _ = Describe("Node controller", func() {
	// Helper function to patch runERDMADevicePlugin method
	patchRunERDMADevicePlugin := func(controllerReconciler *nodeReconcile) *gomonkey.Patches {
		patches := gomonkey.ApplyPrivateMethod(controllerReconciler, "runERDMADevicePlugin", func(r *nodeReconcile, count int) {
		})
		return patches
	}

	Context("Create Node", func() {
		nodeName := "foo"

		AfterEach(func() {
			_ = k8sClient.Delete(context.Background(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "eni-config", Namespace: "kube-system"}})

			// wait eni config deleted
			Eventually(func() error {
				cm := &corev1.ConfigMap{}
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: "eni-config", Namespace: "kube-system"}, cm)
			}, 5*time.Second, 500*time.Millisecond).Should(HaveOccurred())

			_ = k8sClient.Delete(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}})
			Eventually(func() error {
				node := &corev1.Node{}
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, node)
			}, 5*time.Second, 500*time.Millisecond).Should(HaveOccurred())

			_ = k8sClient.Delete(context.Background(), &networkv1beta1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}})
			Eventually(func() error {
				node := &networkv1beta1.Node{}
				return k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, node)
			}, 5*time.Second, 500*time.Millisecond).Should(HaveOccurred())
		})

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

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

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
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           2,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10,
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

		It("New regular node with IPv4 configuration", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-regular123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-regular1", "vsw-regular2"]},
						"security_group": "sg-regular",
						"vswitch_selection_policy": "random",
						"ip_stack": "ipv4",
						"max_pool_size": 10,
						"min_pool_size": 5,
						"ip_pool_sync_period": "30s"
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-regular123",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           3,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10,
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeFalse())
			Expect(node.Spec.ENISpec.VSwitchOptions).To(Equal([]string{"vsw-regular1", "vsw-regular2"}))
			Expect(node.Spec.ENISpec.SecurityGroupIDs).To(Equal([]string{"sg-regular"}))
			Expect(node.Spec.ENISpec.VSwitchSelectPolicy).To(Equal(networkv1beta1.VSwitchSelectionPolicyRandom))

			Expect(node.Spec.Pool).NotTo(BeNil())
			Expect(node.Spec.Pool.MaxPoolSize).To(Equal(10))
			Expect(node.Spec.Pool.MinPoolSize).To(Equal(5))
			Expect(node.Spec.Pool.PoolSyncPeriod).To(Equal("30s"))

			Expect(node.Spec.Flavor).To(HaveLen(1))
			Expect(node.Spec.Flavor[0].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))
			Expect(node.Spec.Flavor[0].Count).To(Equal(2)) // 3 adapters - 1 = 2
		})

		It("New regular node with dual stack configuration", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-dual",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-dual123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-dual1"]},
						"security_group": "sg-dual",
						"vswitch_selection_policy": "ordered",
						"ip_stack": "dual",
						"max_pool_size": 20,
						"min_pool_size": 10
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-dual",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-dual123",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           3,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10, // Same as IPv4 for dual stack
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeTrue())
			Expect(node.Spec.ENISpec.VSwitchOptions).To(Equal([]string{"vsw-dual1"}))
			Expect(node.Spec.ENISpec.SecurityGroupIDs).To(Equal([]string{"sg-dual"}))
			Expect(node.Spec.ENISpec.VSwitchSelectPolicy).To(Equal(networkv1beta1.VSwitchSelectionPolicyMost))
		})

		It("New regular node with ERDMA enabled", func() {
			ctx := context.Background()

			// Patch nodecap.GetNodeCapabilities to return ERDMA support
			patches := gomonkey.ApplyFunc(nodecap.GetNodeCapabilities, func(cap string) string {
				if cap == nodecap.NodeCapabilityERDMA {
					return "erdma"
				}
				return ""
			})
			defer patches.Reset()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-erdma",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-erdma123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-erdma1"]},
						"security_group": "sg-erdma",
						"vswitch_selection_policy": "random",
						"ip_stack": "ipv4",
						"enable_erdma": true,
						"max_pool_size": 15,
						"min_pool_size": 8
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-erdma",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-erdma123",
						InstanceType: "ecs.ebmg6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           4,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10,
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableERDMA).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeFalse())

			Expect(node.Spec.Flavor).To(HaveLen(2))
			// ERDMA flavor should be first
			Expect(node.Spec.Flavor[0].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))
			Expect(node.Spec.Flavor[0].NetworkInterfaceTrafficMode).To(Equal(networkv1beta1.NetworkInterfaceTrafficModeHighPerformance))
			Expect(node.Spec.Flavor[0].Count).To(Equal(1))
			// Regular secondary ENI
			Expect(node.Spec.Flavor[1].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))
			Expect(node.Spec.Flavor[1].NetworkInterfaceTrafficMode).To(Equal(networkv1beta1.NetworkInterfaceTrafficModeStandard))
			Expect(node.Spec.Flavor[1].Count).To(Equal(2)) // 4 adapters - 1 (ERDMA) - 1 = 2
		})

		It("New regular node with ENI trunking enabled", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-trunk",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-trunk123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-trunk1"]},
						"security_group": "sg-trunk",
						"vswitch_selection_policy": "random",
						"ip_stack": "ipv4",
						"enable_eni_trunking": true,
						"max_pool_size": 12,
						"min_pool_size": 6
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-trunk",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-trunk123",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           4,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10,
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableTrunk).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeFalse())

			Expect(node.Spec.Flavor).To(HaveLen(2))
			// Trunk flavor should be first
			Expect(node.Spec.Flavor[0].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeTrunk))
			Expect(node.Spec.Flavor[0].NetworkInterfaceTrafficMode).To(Equal(networkv1beta1.NetworkInterfaceTrafficModeStandard))
			Expect(node.Spec.Flavor[0].Count).To(Equal(1))
			// Regular secondary ENI
			Expect(node.Spec.Flavor[1].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))
			Expect(node.Spec.Flavor[1].NetworkInterfaceTrafficMode).To(Equal(networkv1beta1.NetworkInterfaceTrafficModeStandard))
			Expect(node.Spec.Flavor[1].Count).To(Equal(2)) // 4 adapters - 1 (trunk) - 1 = 2
		})

		It("New regular node with both ERDMA and trunking enabled", func() {
			ctx := context.Background()

			// Patch nodecap.GetNodeCapabilities to return ERDMA support
			patches := gomonkey.ApplyFunc(nodecap.GetNodeCapabilities, func(cap string) string {
				if cap == nodecap.NodeCapabilityERDMA {
					return "erdma"
				}
				return ""
			})
			defer patches.Reset()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-both",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-both123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-both1"]},
						"security_group": "sg-both",
						"vswitch_selection_policy": "ordered",
						"ip_stack": "ipv4",
						"enable_erdma": true,
						"enable_eni_trunking": true,
						"max_pool_size": 25,
						"min_pool_size": 12
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-both",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-both123",
						InstanceType: "ecs.ebmg6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           5,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10,
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableTrunk).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableERDMA).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeFalse())

			Expect(node.Spec.Flavor).To(HaveLen(3))
			// Trunk flavor should be first
			Expect(node.Spec.Flavor[0].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeTrunk))
			Expect(node.Spec.Flavor[0].Count).To(Equal(1))
			// ERDMA flavor should be second
			Expect(node.Spec.Flavor[1].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))
			Expect(node.Spec.Flavor[1].NetworkInterfaceTrafficMode).To(Equal(networkv1beta1.NetworkInterfaceTrafficModeHighPerformance))
			Expect(node.Spec.Flavor[1].Count).To(Equal(1))
			// Regular secondary ENI
			Expect(node.Spec.Flavor[2].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))
			Expect(node.Spec.Flavor[2].NetworkInterfaceTrafficMode).To(Equal(networkv1beta1.NetworkInterfaceTrafficModeStandard))
			Expect(node.Spec.Flavor[2].Count).To(Equal(2)) // 5 adapters - 1 (trunk) - 1 (ERDMA) - 1 = 2
		})

		It("New regular node with IPv6-only configuration", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-ipv6",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-ipv6123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-ipv61"]},
						"security_group": "sg-ipv6",
						"vswitch_selection_policy": "random",
						"ip_stack": "ipv6",
						"max_pool_size": 8,
						"min_pool_size": 4
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-ipv6",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-ipv6123",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           3,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10,
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeFalse())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeTrue())
			Expect(node.Spec.ENISpec.VSwitchOptions).To(Equal([]string{"vsw-ipv61"}))
			Expect(node.Spec.ENISpec.SecurityGroupIDs).To(Equal([]string{"sg-ipv6"}))
			Expect(node.Spec.ENISpec.VSwitchSelectPolicy).To(Equal(networkv1beta1.VSwitchSelectionPolicyRandom))
		})

		It("New regular node with unsupported dual stack configuration", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-unsupported-dual",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-unsupported-dual123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-unsupported-dual1"]},
						"security_group": "sg-unsupported-dual",
						"vswitch_selection_policy": "random",
						"ip_stack": "dual",
						"max_pool_size": 15,
						"min_pool_size": 8
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-unsupported-dual",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-unsupported-dual123",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           3,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     5, // Different from IPv4, should fallback to IPv4 only
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeFalse()) // Should be false due to unsupported dual stack
		})

		It("New regular node with idle IP reclaim configuration", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-reclaim",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-reclaim123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-reclaim1"]},
						"security_group": "sg-reclaim",
						"vswitch_selection_policy": "random",
						"ip_stack": "ipv4",
						"max_pool_size": 20,
						"min_pool_size": 5,
						"ip_pool_sync_period": "30s",
						"idle_ip_reclaim_after": "5m",
						"idle_ip_reclaim_interval": "1m",
						"idle_ip_reclaim_batch_size": 3,
						"idle_ip_reclaim_jitter_factor": "0.2"
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-reclaim",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-reclaim123",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           3,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10,
						EriQuantity:        2,
						MemberAdapterLimit: 2,
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

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeFalse())

			// Verify pool configuration
			Expect(node.Spec.Pool).NotTo(BeNil())
			Expect(node.Spec.Pool.MaxPoolSize).To(Equal(20))
			Expect(node.Spec.Pool.MinPoolSize).To(Equal(5))
			Expect(node.Spec.Pool.PoolSyncPeriod).To(Equal("30s"))

			// Verify idle IP reclaim configuration
			Expect(node.Spec.Pool.Reclaim).NotTo(BeNil())
			Expect(node.Spec.Pool.Reclaim.After).NotTo(BeNil())
			Expect(node.Spec.Pool.Reclaim.After.Duration).To(Equal(5 * time.Minute))
			Expect(node.Spec.Pool.Reclaim.Interval).NotTo(BeNil())
			Expect(node.Spec.Pool.Reclaim.Interval.Duration).To(Equal(1 * time.Minute))
			Expect(node.Spec.Pool.Reclaim.BatchSize).To(Equal(3))
			Expect(node.Spec.Pool.Reclaim.JitterFactor).To(Equal("0.2"))
		})

		It("New EFLO node with idle IP reclaim configuration", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-eflo-reclaim",
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
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-eflo"]},
						"security_group": "sg-eflo",
						"vswitch_selection_policy": "ordered",
						"max_pool_size": 15,
						"min_pool_size": 3,
						"ip_pool_sync_period": "60s",
						"idle_ip_reclaim_after": "10m",
						"idle_ip_reclaim_interval": "2m",
						"idle_ip_reclaim_batch_size": 5,
						"idle_ip_reclaim_jitter_factor": "0.15"
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName + "-eflo-reclaim",
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-eflo-reclaim"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-eflo-reclaim",
					Labels: map[string]string{
						"alibabacloud.com/lingjun-worker": "true",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-eflo-reclaim",
						InstanceType: "ecs",
						ZoneID:       "cn-hangzhou-i",
					},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			By("Reconciling the created resource")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-eflo-reclaim"},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-eflo-reclaim"}, node)
			Expect(err).NotTo(HaveOccurred())

			Expect(node.Spec.NodeMetadata.RegionID).To(Equal("cn-hangzhou"))
			Expect(node.Spec.NodeMetadata.InstanceType).To(Equal("ecs"))
			Expect(node.Spec.NodeMetadata.InstanceID).To(Equal("i-eflo-reclaim"))
			Expect(node.Spec.NodeMetadata.ZoneID).To(Equal("cn-hangzhou-i"))

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.VSwitchOptions).To(Equal([]string{"vsw-eflo"}))
			Expect(node.Spec.ENISpec.SecurityGroupIDs).To(Equal([]string{"sg-eflo"}))
			Expect(node.Spec.ENISpec.VSwitchSelectPolicy).To(Equal(networkv1beta1.VSwitchSelectionPolicyMost))

			// Verify pool configuration for EFLO node
			Expect(node.Spec.Pool).NotTo(BeNil())
			Expect(node.Spec.Pool.MaxPoolSize).To(Equal(15))
			Expect(node.Spec.Pool.MinPoolSize).To(Equal(3))
			Expect(node.Spec.Pool.PoolSyncPeriod).To(Equal("60s"))

			// Verify idle IP reclaim configuration for EFLO node
			Expect(node.Spec.Pool.Reclaim).NotTo(BeNil())
			Expect(node.Spec.Pool.Reclaim.After).NotTo(BeNil())
			Expect(node.Spec.Pool.Reclaim.After.Duration).To(Equal(10 * time.Minute))
			Expect(node.Spec.Pool.Reclaim.Interval).NotTo(BeNil())
			Expect(node.Spec.Pool.Reclaim.Interval.Duration).To(Equal(2 * time.Minute))
			Expect(node.Spec.Pool.Reclaim.BatchSize).To(Equal(5))
			Expect(node.Spec.Pool.Reclaim.JitterFactor).To(Equal("0.15"))
		})

		It("New regular node without idle IP reclaim configuration", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-no-reclaim",
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-no-reclaim",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-no-reclaim123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-no-reclaim"]},
						"security_group": "sg-no-reclaim",
						"vswitch_selection_policy": "random",
						"ip_stack": "ipv4",
						"max_pool_size": 10,
						"min_pool_size": 2,
						"ip_pool_sync_period": "30s"
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			By("Reconciling the created resource")
			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName + "-no-reclaim",
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-no-reclaim"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-no-reclaim",
					Labels: map[string]string{
						"kubernetes.io/hostname": "test-node-no-reclaim",
					},
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-no-reclaim123",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           3,
						IPv4PerAdapter:     10,
						IPv6PerAdapter:     10,
						EriQuantity:        2,
						MemberAdapterLimit: 2,
					},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			By("Reconciling the created resource")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-no-reclaim"},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-no-reclaim"}, node)
			Expect(err).NotTo(HaveOccurred())

			// Verify pool configuration exists
			Expect(node.Spec.Pool).NotTo(BeNil())
			Expect(node.Spec.Pool.MaxPoolSize).To(Equal(10))
			Expect(node.Spec.Pool.MinPoolSize).To(Equal(2))
			Expect(node.Spec.Pool.PoolSyncPeriod).To(Equal("30s"))

			// Verify idle IP reclaim configuration is not set
			Expect(node.Spec.Pool.Reclaim).To(BeNil())
		})

		It("GetVSwitchID from metadata when vswitchOptions is empty", func() {
			ctx := context.Background()

			mockMetadata := &mocks.Interface{}
			mockMetadata.On("GetVSwitchID").Return("vsw-metadata", nil)
			oldMetadata := instance.GetInstanceMeta()
			instance.Init(mockMetadata)
			defer instance.Init(oldMetadata)

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-metadata123",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {},
						"security_group": "sg-metadata"
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-metadata123",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters: 1,
					},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(node.Spec.ENISpec.VSwitchOptions).To(Equal([]string{"vsw-metadata"}))
		})

		It("ERDMA boundary conditions", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-erdma-boundary",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-1"]},
						"security_group": "sg-1",
						"enable_erdma": true
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			// Case 1: EriQuantity <= 0
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-erdma-boundary",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:    1,
						EriQuantity: 0,
					},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			patches := gomonkey.ApplyFunc(nodecap.GetNodeCapabilities, func(cap string) string {
				if cap == nodecap.NodeCapabilityERDMA {
					return "erdma"
				}
				return ""
			})
			defer patches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(node.Spec.ENISpec.EnableERDMA).To(BeFalse())

			// Case 2: nodecap.GetNodeCapabilities returns empty
			node.Spec.NodeCap.EriQuantity = 1
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())

			patches.Reset()
			patches = gomonkey.ApplyFunc(nodecap.GetNodeCapabilities, func(cap string) string {
				return ""
			})
			defer patches.Reset()

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(node.Spec.ENISpec.EnableERDMA).To(BeFalse())
		})

		It("Trunking boundary conditions", func() {
			ctx := context.Background()

			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: corev1.NodeSpec{
					ProviderID: "i-trunk-boundary",
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).Should(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "eni-config",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": `{
						"vswitches": {"cn-hangzhou-i":["vsw-1"]},
						"security_group": "sg-1",
						"enable_eni_trunking": true
					}`,
				}}
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())

			controllerReconciler := &nodeReconcile{
				client:   k8sClient,
				nodeName: nodeName,
				record:   record.NewFakeRecorder(100),
			}
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			// Case 1: MemberAdapterLimit <= 0
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: networkv1beta1.NodeSpec{
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-trunk-boundary",
						InstanceType: "ecs.c6.large",
						ZoneID:       "cn-hangzhou-i",
					},
					NodeCap: networkv1beta1.NodeCap{
						Adapters:           1,
						MemberAdapterLimit: 0,
					},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(node.Spec.ENISpec.EnableTrunk).To(BeFalse())

			// Case 2: ExclusiveENIOnly label
			node.Spec.NodeCap.MemberAdapterLimit = 10
			node.Labels = map[string]string{
				terwayTypes.ExclusiveENIModeLabel: string(terwayTypes.ExclusiveENIOnly),
			}
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(node.Spec.ENISpec.EnableTrunk).To(BeFalse())
		})
	})
})
