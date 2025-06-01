package podeni

import (
	"context"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/controller/status"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var ctx context.Context

var _ = Describe("Pod controller", func() {
	nodeName := "node"

	var (
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

		openAPI.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return([]ecs.InstanceType{
			{
				EniTotalQuantity:            5,
				EniQuantity:                 4,
				InstanceTypeId:              "instanceType",
				EniTrunkSupported:           false,
				EniPrivateIpAddressQuantity: 10,
			},
		}, nil).Maybe()

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					"topology.kubernetes.io/region":    "cn-hangzhou",
					"topology.kubernetes.io/zone":      "cn-hangzhou-a",
					"node.kubernetes.io/instance-type": "instanceType",
				},
			},
			Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-xxx"},
		}
		_ = k8sClient.Create(ctx, node)
	})

	AfterEach(func() {
		rollBackCtx := context.Background()
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		_ = k8sClient.Delete(rollBackCtx, node)
	})

	Context("reconcile create flow", func() {
		It("should create PodENI with Pod properly", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-eni-create",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "pause",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-eni-create",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-create",
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(true),
								},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: "eni-create",
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID: "eni-create",
					},
					PodENIRef: &corev1.ObjectReference{
						Namespace: "default",
						Name:      "test-pod-eni-create",
					},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			go func() {
				time.Sleep(1 * time.Second)
				for i := 0; i < 5; i++ {
					eni := &networkv1beta1.NetworkInterface{}
					err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-create"}, eni)
					if err != nil {
						continue
					}
					if eni.Status.Phase == networkv1beta1.ENIPhaseBinding {
						eni.Status.Phase = networkv1beta1.ENIPhaseBind
						_ = k8sClient.Status().Update(ctx, eni)
						return
					}

					time.Sleep(1 * time.Second)
				}
			}()

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-pod-eni-create", Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("reconcile delete flow", func() {
		It("should call podENIDelete if DeletionTimestamp is set", func() {
			now := metav1.Now()
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-delete",
					Namespace:         "default",
					DeletionTimestamp: &now,
					Finalizers:        []string{types.FinalizerPodENIV2},
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							AllocationType: networkv1beta1.AllocationType{
								Type: networkv1beta1.IPAllocTypeElastic,
							},
							ENI: networkv1beta1.ENI{
								ID: "eni-2",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			// call delete
			Expect(k8sClient.Delete(ctx, podENI))

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-delete",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "pause",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			r := &ReconcilePodENI{
				client:          k8sClient,
				scheme:          nil,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-delete", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "test-delete", Namespace: "default"}, updated)
			Expect(k8sErr.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("PodENI with different phases", func() {
		BeforeEach(func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-phase-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "pause",
						},
					},
					TerminationGracePeriodSeconds: ptr.To(int64(0)),
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
		})

		AfterEach(func() {
			Eventually(func() bool {
				pod := &corev1.Pod{}
				err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"}, pod)
				if err != nil {
					return k8sErr.IsNotFound(err)
				}
				_ = k8sClient.Delete(ctx, pod)
				return false
			}, time.Second*10, time.Millisecond*500).Should(BeTrue())

			Eventually(func() bool {
				podENI := &networkv1beta1.PodENI{}
				err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"}, podENI)
				if err != nil {
					return k8sErr.IsNotFound(err)
				}
				controllerutil.RemoveFinalizer(podENI, types.FinalizerPodENIV2)
				_ = k8sClient.Update(ctx, podENI)
				_ = k8sClient.Delete(ctx, podENI)
				return false
			}, time.Second*10, time.Millisecond*500).Should(BeTrue())
		})

		It("should handle ENIPhaseBind correctly", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-phase-pod",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-binding",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseBind,
				InstanceID: "i-instance1",
				TrunkENIID: "trunk-eni-1",
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-binding": {
						ID:     "eni-binding",
						Status: networkv1beta1.ENIStatusBind,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, podENI)).Should(Succeed())

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       true,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// 检查状态没有改变
			updated := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})

		It("should handle ENIPhaseUnbind correctly", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-phase-pod",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-unbind",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseUnbind,
			}
			Expect(k8sClient.Status().Update(ctx, podENI))

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       true,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// 检查状态没有改变
			updated := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseUnbind)))
		})

		It("should handle ENIPhaseDetaching correctly", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-phase-pod",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-detaching",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseDetaching,
				InstanceID: "i-instance1",
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-detaching": {
						ID:     "eni-detaching",
						Status: networkv1beta1.ENIStatusBind,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, podENI))

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: "eni-detaching",
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:                "eni-detaching",
						VPCID:             "",
						MAC:               "",
						Zone:              "",
						VSwitchID:         "",
						ResourceGroupID:   "",
						SecurityGroupIDs:  nil,
						AttachmentOptions: networkv1beta1.AttachmentOptions{},
					},
					IPv4:         "",
					IPv6:         "",
					IPv4CIDR:     "",
					IPv6CIDR:     "",
					ExtraConfig:  nil,
					ManagePolicy: networkv1beta1.ManagePolicy{},
					PodENIRef:    nil,
				},
			}
			Expect(k8sClient.Create(ctx, eni))
			eni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase: networkv1beta1.ENIPhaseBind,
			}
			Expect(k8sClient.Status().Update(ctx, eni))

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			go func() {
				time.Sleep(1 * time.Second)
				for i := 0; i < 5; i++ {
					eni := &networkv1beta1.NetworkInterface{}
					err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-detaching"}, eni)
					if err != nil {
						continue
					}
					if eni.Status.Phase == networkv1beta1.ENIPhaseDetaching {
						eni.Status.Phase = networkv1beta1.ENIPhaseUnbind
						_ = k8sClient.Status().Update(ctx, eni)
						return
					}

					time.Sleep(1 * time.Second)
				}
			}()

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle ENIPhaseDeleting correctly", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-phase-pod",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-deleting",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseDeleting,
			}
			Expect(k8sClient.Status().Update(ctx, podENI))

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// 应该已经被删除
			updated := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"}, updated)
			Expect(k8sErr.IsNotFound(err)).To(BeTrue())
		})

		It("should handle fixed IP PodENI correctly", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-phase-pod",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							AllocationType: networkv1beta1.AllocationType{
								Type:            networkv1beta1.IPAllocTypeFixed,
								ReleaseStrategy: networkv1beta1.ReleaseStrategyTTL,
								ReleaseAfter:    "24h",
							},
							ENI: networkv1beta1.ENI{
								ID: "eni-fixed",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase:       networkv1beta1.ENIPhaseBind,
				PodLastSeen: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
			}
			Expect(k8sClient.Status().Update(ctx, podENI))

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         true,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-phase-pod", Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle gcCRPodENIs correctly for orphaned PodENIs", func() {
			// 创建一个没有对应 Pod 的 PodENI
			orphanedPodENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphaned-podeni",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							AllocationType: networkv1beta1.AllocationType{
								Type:            networkv1beta1.IPAllocTypeFixed,
								ReleaseStrategy: networkv1beta1.ReleaseStrategyTTL,
								ReleaseAfter:    "1m", // 设置一个很短的 TTL
							},
							ENI: networkv1beta1.ENI{
								ID: "eni-orphaned",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, orphanedPodENI)).Should(Succeed())
			orphanedPodENI.Status = networkv1beta1.PodENIStatus{
				Phase:       networkv1beta1.ENIPhaseBind,
				PodLastSeen: metav1.NewTime(time.Now().Add(-10 * time.Minute)), // PodLastSeen 已过期
			}
			Expect(k8sClient.Status().Update(ctx, orphanedPodENI))

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         true,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			// 执行 GC 过程
			go r.gcCRPodENIs(ctx)

			// 等待一段时间让 GC 执行
			time.Sleep(1 * time.Second)

			// 检查 PodENI 是否被标记为删除状态
			updated := &networkv1beta1.PodENI{}
			err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "orphaned-podeni", Namespace: "default"}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseDeleting)))
		})
	})

	Context("migrate function tests", func() {

		It("should migrate PodENI with ENIPhaseInitial", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migrate-initial",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-migrate-initial",
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(true),
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseInitial,
			}
			Expect(k8sClient.Status().Update(ctx, podENI))

			// 执行 migrate 操作
			err := migrate(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred())

			// 检查是否创建了对应的 NetworkInterface
			eni := &networkv1beta1.NetworkInterface{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-migrate-initial"}, eni)
			Expect(err).NotTo(HaveOccurred())
			Expect(eni.Spec.ENI.ID).To(Equal("eni-migrate-initial"))
			Expect(eni.Spec.PodENIRef).NotTo(BeNil())
			Expect(eni.Spec.PodENIRef.Name).To(Equal("test-migrate-initial"))
			Expect(eni.Spec.PodENIRef.Namespace).To(Equal("default"))

			// 验证 reconcile 处理后的状态
			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			// 创建对应的 Pod
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migrate-initial",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "pause",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			// 模拟 NetworkInterface 状态变化
			go func() {
				time.Sleep(1 * time.Second)
				for i := 0; i < 5; i++ {
					eni := &networkv1beta1.NetworkInterface{}
					err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-migrate-initial"}, eni)
					if err != nil {
						continue
					}
					if eni.Status.Phase == networkv1beta1.ENIPhaseBinding {
						eni.Status.Phase = networkv1beta1.ENIPhaseBind
						_ = k8sClient.Status().Update(ctx, eni)
						return
					}
					time.Sleep(1 * time.Second)
				}
			}()

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-migrate-initial", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// 检查最终状态
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "test-migrate-initial", Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})

		It("should migrate PodENI with ENIPhaseBinding", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migrate-binding",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-migrate-binding",
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(false),
								},
							},
							AllocationType: networkv1beta1.AllocationType{Type: networkv1beta1.IPAllocTypeFixed},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseBinding,
				InstanceID: "i-instance1",
				TrunkENIID: "",
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-migrate-binding": {
						ID: "eni-migrate-binding",
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, podENI))

			// 执行 migrate 操作
			err := migrate(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred())

			// 检查是否创建了对应的 NetworkInterface
			eni := &networkv1beta1.NetworkInterface{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-migrate-binding"}, eni)
			Expect(err).NotTo(HaveOccurred())
			Expect(eni.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBinding)))

			// 创建对应的 Pod
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migrate-binding",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "pause",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			go func() {
				time.Sleep(1 * time.Second)
				eni := &networkv1beta1.NetworkInterface{}
				err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-migrate-binding"}, eni)
				if err == nil {
					eni.Status.Phase = networkv1beta1.ENIPhaseBind
					_ = k8sClient.Status().Update(ctx, eni)
				}
			}()

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-migrate-binding", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should migrate PodENI with ENIPhaseBind", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migrate-bind",
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-migrate-bind",
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(true),
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseBind,
				InstanceID: "i-instance1",
				TrunkENIID: "trunk-eni-1",
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-migrate-bind": {
						ID:     "eni-migrate-bind",
						Status: networkv1beta1.ENIStatusBind,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, podENI))

			// 执行 migrate 操作
			err := migrate(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred())

			// 检查是否创建了对应的 NetworkInterface
			eni := &networkv1beta1.NetworkInterface{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-migrate-bind"}, eni)
			Expect(err).NotTo(HaveOccurred())
			Expect(eni.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))

			// 创建对应的 Pod
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migrate-bind",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "pause",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       true,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-migrate-bind", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// 检查状态保持不变
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "test-migrate-bind", Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})

		It("should migrate PodENI with ENIPhaseDetaching", func() {
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-migrate-detaching",
					Namespace:  "default",
					Finalizers: []string{types.FinalizerPodENIV2},
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: "eni-migrate-detaching",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			podENI.Status = networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseDetaching,
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-migrate-detaching": {
						ID:     "eni-migrate-detaching",
						Status: networkv1beta1.ENIPhaseBind,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, podENI))

			// 执行 migrate 操作
			err := migrate(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred())

			// 检查是否创建了对应的 NetworkInterface
			eni := &networkv1beta1.NetworkInterface{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-migrate-detaching"}, eni)
			Expect(err).NotTo(HaveOccurred())
			Expect(eni.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseDetaching)))

			// 创建对应的 Pod
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migrate-detaching",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{
						{
							Name:  "pause",
							Image: "pause",
						},
					},
					TerminationGracePeriodSeconds: ptr.To(int64(0)),
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			r := &ReconcilePodENI{
				client:          k8sClient,
				aliyun:          openAPI,
				record:          record.NewFakeRecorder(100),
				trunkMode:       false,
				crdMode:         false,
				nodeStatusCache: status.NewCache[status.NodeStatus](),
			}

			// 模拟 detach 完成
			go func() {
				time.Sleep(1 * time.Second)
				eni := &networkv1beta1.NetworkInterface{}
				err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: "eni-migrate-detaching"}, eni)
				if err == nil {
					eni.Status.Phase = networkv1beta1.ENIPhaseUnbind
					_ = k8sClient.Status().Update(ctx, eni)
				}
			}()

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: "test-migrate-detaching", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
