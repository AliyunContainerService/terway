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

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
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
					Name: nodeName + "-dual",
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
				nodeName: nodeName + "-dual",
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-dual"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-dual",
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
				NamespacedName: types.NamespacedName{Name: nodeName + "-dual"},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-dual"}, node)
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
					Name: nodeName + "-erdma",
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
				nodeName: nodeName + "-erdma",
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-erdma"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-erdma",
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
				NamespacedName: types.NamespacedName{Name: nodeName + "-erdma"},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-erdma"}, node)
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
					Name: nodeName + "-trunk",
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
				nodeName: nodeName + "-trunk",
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-trunk"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-trunk",
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
				NamespacedName: types.NamespacedName{Name: nodeName + "-trunk"},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-trunk"}, node)
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
					Name: nodeName + "-both",
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
				nodeName: nodeName + "-both",
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-both"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-both",
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
				NamespacedName: types.NamespacedName{Name: nodeName + "-both"},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-both"}, node)
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
					Name: nodeName + "-ipv6",
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
				nodeName: nodeName + "-ipv6",
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-ipv6"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-ipv6",
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
				NamespacedName: types.NamespacedName{Name: nodeName + "-ipv6"},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-ipv6"}, node)
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
					Name: nodeName + "-unsupported-dual",
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
				nodeName: nodeName + "-unsupported-dual",
				record:   record.NewFakeRecorder(100),
			}

			// Patch runERDMADevicePlugin method to avoid starting real device plugin
			devicePluginPatches := patchRunERDMADevicePlugin(controllerReconciler)
			defer devicePluginPatches.Reset()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName + "-unsupported-dual"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Add networkv1beta1.node")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName + "-unsupported-dual",
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
				NamespacedName: types.NamespacedName{Name: nodeName + "-unsupported-dual"},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-unsupported-dual"}, node)
			Expect(err).NotTo(HaveOccurred())

			Expect(node.Spec.ENISpec).NotTo(BeNil())
			Expect(node.Spec.ENISpec.EnableIPv4).To(BeTrue())
			Expect(node.Spec.ENISpec.EnableIPv6).To(BeFalse()) // Should be false due to unsupported dual stack
		})
	})
})
