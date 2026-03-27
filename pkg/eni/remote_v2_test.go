package eni

import (
	"context"
	"time"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("RemoteV2", func() {
	Context("basic interface behaviour", func() {
		var rv2 *RemoteV2

		BeforeEach(func() {
			rv2 = &RemoteV2{
				podENINotifier: NewNotifier(),
			}
		})

		It("returns ResourceTypeMismatch for LocalIP requests", func() {
			ch, traces := rv2.Allocate(context.Background(), &daemon.CNI{}, &LocalIPRequest{})
			Expect(ch).To(BeNil())
			Expect(traces).To(HaveLen(1))
			Expect(traces[0].Condition).To(Equal(ResourceTypeMismatch))
		})

		It("Release always returns false, nil", func() {
			ok, err := rv2.Release(context.Background(), &daemon.CNI{}, &RemoteIPResource{})
			Expect(ok).To(BeFalse())
			Expect(err).NotTo(HaveOccurred())
		})

		It("Dispose always returns 0", func() {
			Expect(rv2.Dispose(10)).To(Equal(0))
		})

		It("Priority returns 100", func() {
			Expect(rv2.Priority()).To(Equal(100))
		})
	})

	Context("getTrunkENI without trunk enabled", func() {
		const nodeName = "rv2-no-trunk"

		It("returns nil when trunk is disabled", func() {
			netNode := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
				Spec: networkv1beta1.NodeSpec{
					ENISpec: &networkv1beta1.ENISpec{
						VSwitchOptions:      []string{"vsw-xx"},
						SecurityGroupIDs:    []string{"sg-x"},
						VSwitchSelectPolicy: networkv1beta1.VSwitchSelectionPolicyMost,
						EnableTrunk:         false,
						EnableIPv4:          true,
					},
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-no-trunk",
						InstanceType: "ecs",
						ZoneID:       "cn-hangzhou-i",
					},
				},
			}
			Expect(k8sClient.Create(ctx, netNode)).To(Succeed())

			rv2 := &RemoteV2{client: k8sClient, nodeName: nodeName}
			trunk, err := rv2.getTrunkENI(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(trunk).To(BeNil())
		})
	})

	Context("getTrunkENI with trunk enabled", func() {
		const nodeName = "rv2-with-trunk"

		It("resolves trunk ENI from Node CR and k8s Node annotation", func() {
			k8sNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Annotations: map[string]string{
						types.TrunkOn: "eni-trunk-rv2",
					},
				},
			}
			Expect(k8sClient.Create(ctx, k8sNode)).To(Succeed())

			netNode := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
				Spec: networkv1beta1.NodeSpec{
					ENISpec: &networkv1beta1.ENISpec{
						VSwitchOptions:      []string{"vsw-xx"},
						SecurityGroupIDs:    []string{"sg-x"},
						VSwitchSelectPolicy: networkv1beta1.VSwitchSelectionPolicyMost,
						EnableTrunk:         true,
						EnableIPv4:          true,
					},
					NodeMetadata: networkv1beta1.NodeMetadata{
						RegionID:     "cn-hangzhou",
						InstanceID:   "i-with-trunk",
						InstanceType: "ecs",
						ZoneID:       "cn-hangzhou-i",
					},
				},
			}
			Expect(k8sClient.Create(ctx, netNode)).To(Succeed())

			netNode.Status.NetworkInterfaces = map[string]*networkv1beta1.Nic{
				"eni-trunk-rv2": {
					ID:               "eni-trunk-rv2",
					MacAddress:       "00:16:3e:aa:bb:cc",
					VSwitchID:        "vsw-xx",
					SecurityGroupIDs: []string{"sg-x"},
					PrimaryIPAddress: "10.2.0.1",
					IPv4CIDR:         "10.2.0.0/24",
					Status:           "InUse",
				},
			}
			Expect(k8sClient.Status().Update(ctx, netNode)).To(Succeed())

			rv2 := &RemoteV2{client: k8sClient, nodeName: nodeName}
			trunk, err := rv2.getTrunkENI(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(trunk).NotTo(BeNil())
			Expect(trunk.ID).To(Equal("eni-trunk-rv2"))
			Expect(trunk.MAC).To(Equal("00:16:3e:aa:bb:cc"))
			Expect(trunk.Trunk).To(BeTrue())
			Expect(trunk.VSwitchID).To(Equal("vsw-xx"))
		})
	})

	Context("Allocate delegates to Remote for RemoteIP (no trunk)", func() {
		It("returns PodENI allocation when PodENI is in Bind phase", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-rv2-alloc",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							IPv4:         "10.1.0.5",
							IPv4CIDR:     "10.1.0.0/24",
							ENI:          networkv1beta1.ENI{ID: "eni-rv2-alloc", MAC: "00:16:3e:00:00:01"},
							Interface:    "eth0",
							DefaultRoute: true,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).To(Succeed())

			podENI.Status = networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseBind,
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-rv2-alloc": {ID: "eni-rv2-alloc"},
				},
			}
			Expect(k8sClient.Status().Update(ctx, podENI)).To(Succeed())

			notifier := NewNotifier()
			rv2 := &RemoteV2{
				client:         k8sClient,
				nodeName:       "any-node",
				podENINotifier: notifier,
			}

			allocCtx, allocCancel := context.WithTimeout(ctx, 5*time.Second)
			defer allocCancel()

			ch, traces := rv2.Allocate(allocCtx, &daemon.CNI{
				PodName:      "pod-rv2-alloc",
				PodNamespace: "default",
			}, &RemoteIPRequest{})
			Expect(ch).NotTo(BeNil())
			Expect(traces).To(BeNil())

			select {
			case resp := <-ch:
				Expect(resp).NotTo(BeNil())
				Expect(resp.Err).NotTo(HaveOccurred())
				Expect(resp.NetworkConfigs).To(HaveLen(1))
				rpcConf := resp.NetworkConfigs[0].ToRPC()
				Expect(rpcConf).To(HaveLen(1))
				Expect(rpcConf[0].BasicInfo.PodIP.IPv4).To(Equal("10.1.0.5"))
				Expect(rpcConf[0].ENIInfo.Trunk).To(BeFalse())
			case <-allocCtx.Done():
				Fail("timeout waiting for RemoteV2 allocation")
			}
		})
	})

	Context("Allocate with trunk ENI", func() {
		It("passes trunk ENI info to Remote and returns trunk-enriched response", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-rv2-trunk-alloc",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							IPv4:         "10.3.0.10",
							IPv4CIDR:     "10.3.0.0/24",
							ENI:          networkv1beta1.ENI{ID: "eni-member-rv2", MAC: "00:16:3e:dd:ee:ff"},
							Interface:    "eth0",
							DefaultRoute: true,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).To(Succeed())

			podENI.Status = networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseBind,
				TrunkENIID: "eni-trunk-for-alloc",
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-member-rv2": {ID: "eni-member-rv2", Vid: 100},
				},
			}
			Expect(k8sClient.Status().Update(ctx, podENI)).To(Succeed())

			notifier := NewNotifier()
			rv2 := &RemoteV2{
				client:         k8sClient,
				nodeName:       "any-node",
				podENINotifier: notifier,
				trunkENI: &daemon.ENI{
					ID:        "eni-trunk-for-alloc",
					MAC:       "00:16:3e:aa:bb:cc",
					Trunk:     true,
					GatewayIP: types.IPSet{},
				},
			}

			allocCtx, allocCancel := context.WithTimeout(ctx, 5*time.Second)
			defer allocCancel()

			ch, _ := rv2.Allocate(allocCtx, &daemon.CNI{
				PodName:      "pod-rv2-trunk-alloc",
				PodNamespace: "default",
			}, &RemoteIPRequest{})
			Expect(ch).NotTo(BeNil())

			select {
			case resp := <-ch:
				Expect(resp).NotTo(BeNil())
				Expect(resp.Err).NotTo(HaveOccurred())
				Expect(resp.NetworkConfigs).To(HaveLen(1))
				rpcConf := resp.NetworkConfigs[0].ToRPC()
				Expect(rpcConf).To(HaveLen(1))
				Expect(rpcConf[0].BasicInfo.PodIP.IPv4).To(Equal("10.3.0.10"))
				Expect(rpcConf[0].ENIInfo.Trunk).To(BeTrue())
				Expect(rpcConf[0].ENIInfo.MAC).To(Equal("00:16:3e:aa:bb:cc"))
				Expect(rpcConf[0].ENIInfo.Vid).To(Equal(uint32(100)))
			case <-allocCtx.Done():
				Fail("timeout waiting for RemoteV2 trunk allocation")
			}
		})
	})

	Context("Allocate waits for PodENI notification", func() {
		It("blocks until notifier fires and PodENI becomes ready", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-rv2-wait",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).To(Succeed())

			notifier := NewNotifier()
			rv2 := &RemoteV2{
				client:         k8sClient,
				nodeName:       "any-node",
				podENINotifier: notifier,
			}

			allocCtx, allocCancel := context.WithTimeout(ctx, 5*time.Second)
			defer allocCancel()

			ch, _ := rv2.Allocate(allocCtx, &daemon.CNI{
				PodName:      "pod-rv2-wait",
				PodNamespace: "default",
			}, &RemoteIPRequest{})
			Expect(ch).NotTo(BeNil())

			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)

				podENI.Spec.Allocations = []networkv1beta1.Allocation{
					{
						IPv4:         "10.4.0.20",
						IPv4CIDR:     "10.4.0.0/24",
						ENI:          networkv1beta1.ENI{ID: "eni-rv2-wait", MAC: "00:16:3e:11:22:33"},
						Interface:    "eth0",
						DefaultRoute: true,
					},
				}
				Expect(k8sClient.Update(allocCtx, podENI)).To(Succeed())

				podENI.Status = networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseBind,
					ENIInfos: map[string]networkv1beta1.ENIInfo{
						"eni-rv2-wait": {ID: "eni-rv2-wait"},
					},
				}
				Expect(k8sClient.Status().Update(allocCtx, podENI)).To(Succeed())
				notifier.Notify()
			}()

			select {
			case resp := <-ch:
				Expect(resp).NotTo(BeNil())
				Expect(resp.Err).NotTo(HaveOccurred())
				Expect(resp.NetworkConfigs).To(HaveLen(1))
				rpcConf := resp.NetworkConfigs[0].ToRPC()
				Expect(rpcConf).To(HaveLen(1))
				Expect(rpcConf[0].BasicInfo.PodIP.IPv4).To(Equal("10.4.0.20"))
			case <-allocCtx.Done():
				Fail("timeout waiting for delayed RemoteV2 allocation")
			}
		})
	})
})
