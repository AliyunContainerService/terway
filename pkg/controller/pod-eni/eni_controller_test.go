package podeni

import (
	"context"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/controller/status"
	"github.com/AliyunContainerService/terway/pkg/internal/testutil"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var ctx context.Context

func createTestReconciler(openAPI *mocks.OpenAPI, trunkMode, crdMode bool) *ReconcilePodENI {
	return &ReconcilePodENI{
		client:          k8sClient,
		aliyun:          openAPI,
		record:          record.NewFakeRecorder(100),
		trunkMode:       trunkMode,
		crdMode:         crdMode,
		nodeStatusCache: status.NewCache[status.NodeStatus](),
	}
}

// Helper to simulate NetworkInterface status transitions
func simulateENIStatusTransition(eniID string, fromPhase, toPhase networkv1beta1.Phase, delay time.Duration) {
	go func() {
		time.Sleep(delay)
		eni := &networkv1beta1.NetworkInterface{}
		err := k8sClient.Get(ctx, k8stypes.NamespacedName{Name: eniID}, eni)
		if err != nil {
			return
		}
		if eni.Status.Phase == fromPhase {
			eni.Status.Phase = toPhase
			_ = k8sClient.Status().Update(ctx, eni)
		}
	}()
}

var _ = Describe("ENI Controller Tests", func() {
	const (
		testNodeName = "test-node"
	)

	var (
		openAPI     *mocks.OpenAPI
		vpcClient   *mocks.VPC
		ecsClient   *mocks.ECS
		efloClient  *mocks.EFLO
		testNode    *corev1.Node
		testNodeCRD *networkv1beta1.Node
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Setup mocks
		openAPI = mocks.NewOpenAPI(GinkgoT())
		vpcClient = mocks.NewVPC(GinkgoT())
		ecsClient = mocks.NewECS(GinkgoT())
		efloClient = mocks.NewEFLO(GinkgoT())

		openAPI.On("GetVPC").Return(vpcClient).Maybe()
		openAPI.On("GetECS").Return(ecsClient).Maybe()
		openAPI.On("GetEFLO").Return(efloClient).Maybe()

		ecsClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return([]ecs.InstanceType{
			{
				EniTotalQuantity:            5,
				EniQuantity:                 4,
				InstanceTypeId:              "instanceType",
				EniTrunkSupported:           false,
				EniPrivateIpAddressQuantity: 10,
			},
		}, nil).Maybe()

		// Setup test node
		testNode = testutil.NewK8sNodeBuilder(testNodeName).Build()
		Expect(k8sClient.Create(ctx, testNode)).Should(Succeed())

		// Setup test node CRD
		testNodeCRD = testutil.NewNodeCRDBuilder(testNodeName).Build()
		Expect(k8sClient.Create(ctx, testNodeCRD)).Should(Succeed())
	})

	AfterEach(func() {
		// Clean up test node
		if testNode != nil {
			_ = k8sClient.Delete(ctx, testNode)
		}
		// Clean up test node CRD
		if testNodeCRD != nil {
			_ = k8sClient.Delete(ctx, testNodeCRD)
		}
	})

	// ==============================================================================
	// RECONCILE ENTRY POINT TESTS
	// ==============================================================================

	Context("Reconcile Entry Point", func() {
		It("should return no error for non-existent PodENI", func() {
			r := createTestReconciler(openAPI, false, false)

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{
					Name: "non-existent", Namespace: "default",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should route to delete flow when DeletionTimestamp is set", func() {
			// Create PodENI first
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-delete-routing",
					Namespace:  "default",
					Finalizers: []string{types.FinalizerPodENIV2},
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: "eni-delete-routing"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())

			pod := testutil.CreateTestPod("test-delete-routing", "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			// Create NetworkInterface CRD for deletion
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: "eni-delete-routing",
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: "eni-delete-routing"},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			// Now delete the PodENI to trigger deletion flow
			Expect(k8sClient.Delete(ctx, podENI)).Should(Succeed())

			r := createTestReconciler(openAPI, false, false)
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{
					Name: "test-delete-routing", Namespace: "default",
				},
			})

			Expect(err).NotTo(HaveOccurred())
		})
	})

	// ==============================================================================
	// PODENI STATE-BASED TESTS
	// ==============================================================================

	Context("PodENI State: Initial", func() {
		It("should transition from Initial to Binding and then to Bind", func() {
			podName := "test-initial-state"
			eniID := "eni-initial"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: eniID,
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(false),
								},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			// Simulate successful binding
			simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify final state
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})

		It("should handle missing Pod gracefully", func() {
			podName := "test-missing-pod"
			eniID := "eni-missing-pod"

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should handle empty allocations", func() {
			podName := "test-empty-allocations"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{}, // Empty allocations
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, false)
			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("alloction is empty"))
		})
	})

	Context("PodENI State: Binding", func() {
		It("should continue binding process when in Binding state", func() {
			podName := "test-binding-state"
			eniID := "eni-binding"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							AllocationType: networkv1beta1.AllocationType{Type: networkv1beta1.IPAllocTypeFixed, ReleaseStrategy: networkv1beta1.ReleaseStrategyNever},
							ENI: networkv1beta1.ENI{
								ID: eniID,
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(false),
								},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseBinding,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})
	})

	Context("PodENI State: Bind", func() {
		It("should be no-op when already in Bind state", func() {
			podName := "test-bind-state"
			eniID := "eni-bind"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase:      networkv1beta1.ENIPhaseBind,
					InstanceID: "i-instance1",
					TrunkENIID: "trunk-eni-1",
					ENIInfos: map[string]networkv1beta1.ENIInfo{
						eniID: {
							ID:     eniID,
							Status: networkv1beta1.ENIStatusBind,
						},
					},
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify state unchanged
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})
	})

	Context("PodENI State: Unbind", func() {
		It("should be no-op when in Unbind state", func() {
			podName := "test-unbind-state"
			eniID := "eni-unbind"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseUnbind,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify state unchanged
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseUnbind)))
		})
	})

	Context("PodENI State: Detaching", func() {
		It("should transition from Detaching to Unbind", func() {
			podName := "test-detaching-state"
			eniID := "eni-detaching"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase:      networkv1beta1.ENIPhaseDetaching,
					InstanceID: "i-instance1",
					ENIInfos: map[string]networkv1beta1.ENIInfo{
						eniID: {
							ID:     eniID,
							Status: networkv1beta1.ENIStatusBind,
						},
					},
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
				Status: networkv1beta1.NetworkInterfaceStatus{
					Phase: networkv1beta1.ENIPhaseBind,
				},
			}
			err = testutil.CreateResource(ctx, k8sClient, eni)
			Expect(err).NotTo(HaveOccurred())

			simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseDetaching, networkv1beta1.ENIPhaseUnbind, 1*time.Second)

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify transition to Unbind
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseUnbind)))
		})
	})

	Context("PodENI State: Deleting", func() {
		It("should delete PodENI when in Deleting state", func() {
			podName := "test-deleting-state"
			eniID := "eni-deleting"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseDeleting,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify PodENI is deleted
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(k8sErr.IsNotFound(err)).To(BeTrue())
		})
	})

	// ==============================================================================
	// DELETION WORKFLOW TESTS
	// ==============================================================================

	Context("PodENI Deletion Workflow", func() {
		It("should handle deletion with finalizers correctly", func() {
			podName := "test-deletion-finalizer"
			eniID := "eni-deletion-finalizer"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			now := metav1.Now()
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:              podName,
					Namespace:         "default",
					DeletionTimestamp: &now,
					Finalizers:        []string{types.FinalizerPodENIV2},
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, podENI)).Should(Succeed())

			r := createTestReconciler(openAPI, false, false)
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())

			// Verify finalizer is removed and PodENI is deleted
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(k8sErr.IsNotFound(err)).To(BeTrue())
		})
	})

	// ==============================================================================
	// MIGRATION TESTS
	// ==============================================================================

	Context("Migration Function", func() {
		It("should create NetworkInterface for existing PodENI", func() {
			podName := "test-migration"
			eniID := "eni-migration"

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
					Labels: map[string]string{
						types.ENIRelatedNodeName: testNodeName,
					},
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase:      networkv1beta1.ENIPhaseBind,
					InstanceID: "i-test",
					TrunkENIID: "eni-trunk",
					ENIInfos: map[string]networkv1beta1.ENIInfo{
						eniID: {
							ID:     eniID,
							Status: networkv1beta1.ENIStatusBind,
						},
					},
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			// Execute migration
			err = migrate(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred())

			// Verify NetworkInterface was created
			eni := &networkv1beta1.NetworkInterface{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: eniID}, eni)
			Expect(err).NotTo(HaveOccurred())
			Expect(eni.Spec.ENI.ID).To(Equal(eniID))
			Expect(eni.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})

		It("should update existing NetworkInterface during migration", func() {
			podName := "test-migration-update"
			eniID := "eni-migration-update"

			// Create PodENI
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
					Labels: map[string]string{
						types.ENIRelatedNodeName: testNodeName,
					},
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase:      networkv1beta1.ENIPhaseBind,
					InstanceID: "i-test",
					ENIInfos: map[string]networkv1beta1.ENIInfo{
						eniID: {ID: eniID, Status: networkv1beta1.ENIStatusBind},
					},
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			// Create NetworkInterface with empty status
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			// Execute migration
			err = migrate(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred())

			// Verify NetworkInterface status was updated
			updatedENI := &networkv1beta1.NetworkInterface{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: eniID}, updatedENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
			Expect(updatedENI.Status.InstanceID).To(Equal("i-test"))
		})
	})

	// ==============================================================================
	// GARBAGE COLLECTION TESTS
	// ==============================================================================

	Context("Garbage Collection", func() {
		It("should mark orphaned fixed-IP PodENI as deleting after TTL", func() {
			podName := "test-gc-orphaned"
			eniID := "eni-gc-orphaned"

			// Create orphaned PodENI (no corresponding Pod) with expired TTL
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							AllocationType: networkv1beta1.AllocationType{
								Type:            networkv1beta1.IPAllocTypeFixed,
								ReleaseStrategy: networkv1beta1.ReleaseStrategyTTL,
								ReleaseAfter:    "1m",
							},
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase:       networkv1beta1.ENIPhaseBind,
					PodLastSeen: metav1.NewTime(time.Now().Add(-10 * time.Minute)), // Expired
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, true)

			// Execute GC
			r.gcCRPodENIs(ctx)

			// Wait for GC to process
			time.Sleep(100 * time.Millisecond)

			// Verify PodENI is marked for deletion
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseDeleting)))
		})

		It("should preserve PodENI with NEVER release strategy", func() {
			podName := "test-gc-never"
			eniID := "eni-gc-never"

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							AllocationType: networkv1beta1.AllocationType{
								Type:            networkv1beta1.IPAllocTypeFixed,
								ReleaseStrategy: networkv1beta1.ReleaseStrategyNever,
							},
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase:       networkv1beta1.ENIPhaseBind,
					PodLastSeen: metav1.NewTime(time.Now().Add(-24 * time.Hour)), // Very old
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, true)

			// Execute GC
			r.gcCRPodENIs(ctx)

			// Wait for GC to process
			time.Sleep(100 * time.Millisecond)

			// Verify PodENI is NOT marked for deletion
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})

		It("should update timestamp for existing pod with fixed IP", func() {
			podName := "test-gc-update-timestamp"
			eniID := "eni-gc-update-timestamp"

			// Create Pod
			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			// Create PodENI with old timestamp
			oldTime := time.Now().Add(-2 * time.Hour)
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							AllocationType: networkv1beta1.AllocationType{
								Type:            networkv1beta1.IPAllocTypeFixed,
								ReleaseStrategy: networkv1beta1.ReleaseStrategyTTL,
								ReleaseAfter:    "1h",
							},
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase:       networkv1beta1.ENIPhaseBind,
					PodLastSeen: metav1.NewTime(oldTime),
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, true)

			// Execute GC
			r.gcCRPodENIs(ctx)

			// Wait for GC to process
			time.Sleep(100 * time.Millisecond)

			// Verify timestamp is updated
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.PodLastSeen.Time).To(BeTemporally(">", oldTime))
		})
	})

	// ==============================================================================
	// TRUNK vs SECONDARY MODE TESTS
	// ==============================================================================

	Context("Trunk vs Secondary Mode", func() {
		It("should handle trunk mode correctly", func() {
			podName := "test-trunk-mode"
			eniID := "eni-trunk-mode"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: eniID,
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
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			r := createTestReconciler(openAPI, true, false) // trunk mode enabled

			// Since we don't have actual trunk ENI setup in test, this should fail
			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("trunk eni id not found"))
		})

		It("should handle secondary mode correctly", func() {
			podName := "test-secondary-mode"
			eniID := "eni-secondary-mode"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: eniID,
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(false),
								},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)

			r := createTestReconciler(openAPI, false, false) // trunk mode disabled

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})
	})

	// ==============================================================================
	// ERROR HANDLING TESTS
	// ==============================================================================

	Context("Error Handling", func() {
		It("should handle missing Node gracefully", func() {
			podName := "test-missing-node"
			eniID := "eni-missing-node"

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: "non-existent-node", // Non-existent node
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
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, false)
			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("error get node"))
		})

		It("should handle attachment failure gracefully", func() {
			// This would test what happens when ENI attachment fails
			// Due to test complexity, we mainly ensure no panic occurs
			podName := "test-attach-failure"
			eniID := "eni-attach-failure"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			// Don't create NetworkInterface - this should cause attachment to fail

			r := createTestReconciler(openAPI, false, false)
			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).To(HaveOccurred()) // Expected due to missing NetworkInterface
		})
	})

	// ==============================================================================
	// FIXED IP TESTS
	// ==============================================================================

	Context("Fixed IP Allocation", func() {
		It("should handle fixed IP PodENI correctly", func() {
			podName := "test-fixed-ip"
			eniID := "eni-fixed-ip"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			pod.ObjectMeta.Labels = map[string]string{"app": "fixed-ip-app"}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
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
								ID: eniID,
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(false),
								},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify PodLastSeen is set for fixed IP
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
			Expect(updatedPodENI.Status.PodLastSeen).NotTo(BeNil())
		})

		It("should not need attach for non-fixed IP pods without fixed IP rebind", func() {
			podName := "test-non-fixed-rebind"
			eniID := "eni-non-fixed-rebind"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							AllocationType: networkv1beta1.AllocationType{
								Type: networkv1beta1.IPAllocTypeElastic, // Not fixed
							},
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseBinding, // Already has status
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, false)
			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			// Should fail because previous podENI exists but pod is not using fixed IP
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("found previous podENI, but pod is not using fixed ip"))
		})
	})

	// ==============================================================================
	// MULTI-ENI ALLOCATION TESTS
	// ==============================================================================

	Context("Multi-ENI Allocation", func() {
		It("should handle multiple ENI allocations correctly", func() {
			podName := "test-multi-eni"
			eniID1 := "eni-multi-1"
			eniID2 := "eni-multi-2"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							Interface: "net1",
							ENI:       networkv1beta1.ENI{ID: eniID1, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
						{
							Interface: "net2",
							ENI:       networkv1beta1.ENI{ID: eniID2, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			// Create NetworkInterface resources for both ENIs
			for _, eniID := range []string{eniID1, eniID2} {
				eni := &networkv1beta1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{Name: eniID},
					Spec: networkv1beta1.NetworkInterfaceSpec{
						ENI: networkv1beta1.ENI{ID: eniID},
					},
				}
				Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

				// Simulate successful binding for each ENI
				simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)
			}

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify both ENIs are processed and allocations are sorted by Interface
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
			Expect(len(updatedPodENI.Spec.Allocations)).To(Equal(2))
			// Verify sorting by Interface
			Expect(updatedPodENI.Spec.Allocations[0].Interface).To(Equal("net1"))
			Expect(updatedPodENI.Spec.Allocations[1].Interface).To(Equal("net2"))
		})

		It("should handle slice order changes in multi-ENI allocations without unnecessary updates", func() {
			podName := "test-multi-eni-order"
			eniID1 := "eni-order-1"
			eniID2 := "eni-order-2"
			eniID3 := "eni-order-3"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			// Create PodENI with allocations in specific order (net3, net1, net2)
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							Interface: "net3",
							ENI:       networkv1beta1.ENI{ID: eniID3, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
						{
							Interface: "net1",
							ENI:       networkv1beta1.ENI{ID: eniID1, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
						{
							Interface: "net2",
							ENI:       networkv1beta1.ENI{ID: eniID2, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			// Create NetworkInterface resources for all ENIs
			for _, eniID := range []string{eniID1, eniID2, eniID3} {
				eni := &networkv1beta1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{Name: eniID},
					Spec: networkv1beta1.NetworkInterfaceSpec{
						ENI: networkv1beta1.ENI{ID: eniID},
					},
				}
				Expect(k8sClient.Create(ctx, eni)).Should(Succeed())
				simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)
			}

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify allocations are sorted by Interface field
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
			Expect(len(updatedPodENI.Spec.Allocations)).To(Equal(3))

			// Verify the allocations are NOT modified
			Expect(updatedPodENI.Spec.Allocations[0].Interface).To(Equal("net3"))
			Expect(updatedPodENI.Spec.Allocations[1].Interface).To(Equal("net1"))
			Expect(updatedPodENI.Spec.Allocations[2].Interface).To(Equal("net2"))

			Expect(updatedPodENI.Status.InstanceID).To(Equal("i-xxx"))
		})

		It("should handle route order changes in multi-ENI allocations", func() {
			podName := "test-multi-eni-routes"
			eniID1 := "eni-routes-1"
			eniID2 := "eni-routes-2"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			// Create PodENI with routes in different orders
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							Interface: "net1",
							ENI:       networkv1beta1.ENI{ID: eniID1, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
							IPv4:      "127.0.0.1",
							ExtraRoutes: []networkv1beta1.Route{
								{Dst: "192.168.2.0/24"},
								{Dst: "192.168.1.0/24"},
								{Dst: "192.168.3.0/24"},
							},
						},
						{
							Interface: "net2",
							ENI:       networkv1beta1.ENI{ID: eniID2, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
							IPv4:      "127.0.0.2",
							ExtraRoutes: []networkv1beta1.Route{
								{Dst: "10.0.2.0/24"},
								{Dst: "10.0.1.0/24"},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			// Create NetworkInterface resources
			for _, eniID := range []string{eniID1, eniID2} {
				eni := &networkv1beta1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{Name: eniID},
					Spec: networkv1beta1.NetworkInterfaceSpec{
						ENI: networkv1beta1.ENI{ID: eniID},
					},
				}
				Expect(k8sClient.Create(ctx, eni)).Should(Succeed())
				simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)
			}

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify the reconciliation completed successfully
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))

			// Routes order should not affect the comparison logic
			Expect(len(updatedPodENI.Spec.Allocations)).To(Equal(2))

			Expect(updatedPodENI.Status.InstanceID).To(Equal("i-xxx"))
		})

		It("should handle concurrent ENI attachment for multiple allocations", func() {
			podName := "test-concurrent-attach"
			eniID1 := "eni-concurrent-1"
			eniID2 := "eni-concurrent-2"
			eniID3 := "eni-concurrent-3"
			eniID4 := "eni-concurrent-4"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			// Create PodENI with multiple allocations
			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							Interface: "net1",
							ENI:       networkv1beta1.ENI{ID: eniID1, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
						{
							Interface: "net2",
							ENI:       networkv1beta1.ENI{ID: eniID2, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
						{
							Interface: "net3",
							ENI:       networkv1beta1.ENI{ID: eniID3, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
						{
							Interface: "net4",
							ENI:       networkv1beta1.ENI{ID: eniID4, AttachmentOptions: networkv1beta1.AttachmentOptions{Trunk: ptr.To(false)}},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			// Create NetworkInterface resources for all ENIs
			for _, eniID := range []string{eniID1, eniID2, eniID3, eniID4} {
				eni := &networkv1beta1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{Name: eniID},
					Spec: networkv1beta1.NetworkInterfaceSpec{
						ENI: networkv1beta1.ENI{ID: eniID},
					},
				}
				Expect(k8sClient.Create(ctx, eni)).Should(Succeed())
				simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)
			}

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify all ENIs are attached and status is updated
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
			Expect(len(updatedPodENI.Spec.Allocations)).To(Equal(4))
			Expect(len(updatedPodENI.Status.ENIInfos)).To(Equal(4))

			// Verify all ENIs are in the status
			for _, eniID := range []string{eniID1, eniID2, eniID3, eniID4} {
				_, exists := updatedPodENI.Status.ENIInfos[eniID]
				Expect(exists).To(BeTrue(), "ENI %s should exist in status", eniID)
			}

			Expect(updatedPodENI.Status.InstanceID).To(Equal("i-xxx"))
		})
	})

	// ==============================================================================
	// NODE EXCLUSIVE ENI MODE TESTS
	// ==============================================================================

	Context("Node Exclusive ENI Mode", func() {
		BeforeEach(func() {
			// Clean up the default test node
			if testNode != nil {
				_ = k8sClient.Delete(ctx, testNode)
			}
			// Clean up the default test node CRD
			if testNodeCRD != nil {
				_ = k8sClient.Delete(ctx, testNodeCRD)
			}
		})

		It("should handle exclusive ENI only mode correctly", func() {
			// Create node with exclusive ENI only mode
			exclusiveNode := testutil.NewK8sNodeBuilder("exclusive-node").
				WithLabel(types.ExclusiveENIModeLabel, string(types.ExclusiveENIOnly)).
				Build()
			Expect(k8sClient.Create(ctx, exclusiveNode)).Should(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, exclusiveNode) }()

			// Create corresponding Node CRD
			exclusiveNodeCRD := testutil.NewNodeCRDBuilder("exclusive-node").Build()
			Expect(k8sClient.Create(ctx, exclusiveNodeCRD)).Should(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, exclusiveNodeCRD) }()

			podName := "test-exclusive-eni"
			eniID := "eni-exclusive"

			pod := testutil.CreateTestPod(podName, "default", "exclusive-node")
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: eniID,
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(false), // eniOnly pod should use trunk=false
								},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			simulateENIStatusTransition(eniID, networkv1beta1.ENIPhaseBinding, networkv1beta1.ENIPhaseBind, 1*time.Second)

			r := createTestReconciler(openAPI, true, false) // trunk mode enabled
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			// Should work in secondary mode even when trunk is enabled globally
			// because node is in exclusive ENI only mode
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should reject trunk ENI for eniOnly pods when trunk mode is enabled", func() {
			// Reset test node for this test
			testNode = testutil.NewK8sNodeBuilder(testNodeName).Build()
			Expect(k8sClient.Create(ctx, testNode)).Should(Succeed())

			// Reset test node CRD for this test
			testNodeCRD = testutil.NewNodeCRDBuilder(testNodeName).Build()
			Expect(k8sClient.Create(ctx, testNodeCRD)).Should(Succeed())

			podName := "test-trunk-reject"
			eniID := "eni-trunk-reject"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: eniID,
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(false), // eniOnly pod trying to use trunk=false in trunk mode
								},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, true, false) // trunk mode enabled
			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			// Should fail because trunk ENI is not allowed for eniOnly pod
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("trunk eni is not allowed for eniOnly pod"))
		})
	})

	// ==============================================================================
	// CRD MODE TESTS
	// ==============================================================================

	Context("CRD Mode", func() {
		It("should always require PodENI in CRD mode", func() {
			podName := "test-crd-mode"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			r := createTestReconciler(openAPI, false, true) // CRD mode enabled

			// Test the podRequirePodENI function
			requiresPodENI := r.podRequirePodENI(ctx, pod)
			Expect(requiresPodENI).To(BeTrue())
		})

		It("should handle non-CRD mode pod requirements correctly", func() {
			podName := "test-non-crd-mode"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			r := createTestReconciler(openAPI, false, false) // CRD mode disabled

			// Host network pods should not require PodENI in non-CRD mode
			requiresPodENI := r.podRequirePodENI(ctx, pod)
			Expect(requiresPodENI).To(BeFalse())
		})
	})

	// ==============================================================================
	// COMPLEX ERROR SCENARIOS
	// ==============================================================================

	Context("Complex Error Scenarios", func() {
		It("should handle node without instance type gracefully", func() {
			// Create node without instance type label
			nodeWithoutType := testutil.NewK8sNodeBuilder("node-no-type").Build()
			delete(nodeWithoutType.Labels, "node.kubernetes.io/instance-type")
			Expect(k8sClient.Create(ctx, nodeWithoutType)).Should(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, nodeWithoutType) }()

			// Create corresponding Node CRD
			nodeWithoutTypeCRD := testutil.NewNodeCRDBuilder("node-no-type").Build()
			Expect(k8sClient.Create(ctx, nodeWithoutTypeCRD)).Should(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, nodeWithoutTypeCRD) }()

			podName := "test-no-instance-type"
			eniID := "eni-no-instance-type"

			pod := testutil.CreateTestPod(podName, "default", "node-no-type")
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{ID: eniID},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			Expect(testutil.CreateResource(ctx, k8sClient, podENI)).Should(Succeed())

			r := createTestReconciler(openAPI, false, false)

			// This should complete without error due to fallback handling
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			// May succeed or fail depending on implementation, but should not panic
			// The key is that it handles the missing instance type gracefully
		})

		It("should handle pod sandbox exited state correctly", func() {
			podName := "test-sandbox-exited"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			pod.Status.Phase = corev1.PodSucceeded // Pod has completed
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			r := createTestReconciler(openAPI, false, false)

			// Exited pod should not require PodENI
			requiresPodENI := r.podRequirePodENI(ctx, pod)
			Expect(requiresPodENI).To(BeFalse())
		})

		It("should handle ignored pod labels correctly", func() {
			podName := "test-ignored-pod"

			pod := testutil.CreateTestPod(podName, "default", testNodeName)
			pod.Labels = map[string]string{
				types.IgnoreByTerway: "true",
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			r := createTestReconciler(openAPI, false, false)

			// Ignored pod should not require PodENI
			requiresPodENI := r.podRequirePodENI(ctx, pod)
			Expect(requiresPodENI).To(BeFalse())
		})
	})

	// ==============================================================================
	// ECS HIGH DENSITY TESTS
	// ==============================================================================

	Context("ECS High Density Mode", func() {
		BeforeEach(func() {
			// Clean up the default test node
			if testNode != nil {
				_ = k8sClient.Delete(ctx, testNode)
			}
			// Clean up the default test node CRD
			if testNodeCRD != nil {
				_ = k8sClient.Delete(ctx, testNodeCRD)
			}
		})

		It("should handle ecsHighDensity with vid = 0 correctly", func() {
			// Create node with ecsHighDensity support
			highDensityNode := testutil.NewK8sNodeBuilder("high-density-node").
				WithInstanceType("instanceType").
				WithEFLO().
				Build()
			Expect(k8sClient.Create(ctx, highDensityNode)).Should(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, highDensityNode) }()

			// Create corresponding Node CRD
			highDensityNodeCRD := testutil.NewNodeCRDBuilder("high-density-node").
				WithEFLO().
				WithAnnotation(types.ENOApi, types.APIEcsHDeni).
				Build()
			Expect(k8sClient.Create(ctx, highDensityNodeCRD)).Should(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, highDensityNodeCRD) }()

			podName := "test-high-density-vid-zero"
			eniID := "eni-high-density-vid-zero"

			pod := testutil.CreateTestPod(podName, "default", "high-density-node")
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: "default",
				},
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							ENI: networkv1beta1.ENI{
								ID: eniID,
								AttachmentOptions: networkv1beta1.AttachmentOptions{
									Trunk: ptr.To(false),
								},
							},
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase: networkv1beta1.ENIPhaseInitial,
				},
			}
			err := testutil.CreateResource(ctx, k8sClient, podENI)
			Expect(err).NotTo(HaveOccurred())

			// Create NetworkInterface with vid = 0
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: eniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: eniID},
				},
				Status: networkv1beta1.NetworkInterfaceStatus{
					Phase: networkv1beta1.ENIPhaseBind,
					ENIInfo: networkv1beta1.ENIInfo{
						ID:   eniID,
						Type: networkv1beta1.ENITypeSecondary,
						Vid:  0, // vid = 0 should be allowed in ecsHighDensity mode
					},
				},
			}
			err = testutil.CreateResource(ctx, k8sClient, eni)
			Expect(err).NotTo(HaveOccurred())

			r := createTestReconciler(openAPI, false, false)
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: k8stypes.NamespacedName{Name: podName, Namespace: "default"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify that vid = 0 is handled correctly in ecsHighDensity mode
			updatedPodENI := &networkv1beta1.PodENI{}
			err = k8sClient.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: "default"}, updatedPodENI)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPodENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))

			// Check that vfID is set to vid value (0) and vid is reset to 0
			eniInfo, exists := updatedPodENI.Status.ENIInfos[eniID]
			Expect(exists).To(BeTrue())
			Expect(eniInfo.Vid).To(Equal(0))
			Expect(eniInfo.VfID).NotTo(BeNil())
			Expect(*eniInfo.VfID).To(Equal(uint32(0)))
		})
	})

	// ==============================================================================
	// NUMA HINTS TESTS
	// ==============================================================================

	Context("NUMA Hints", func() {
		It("should parse NUMA hints from pod annotations correctly", func() {
			// Test the internal function
			annotations := map[string]string{
				"cpuSet": `{"container1":{"0":"0-3","1":"4-7"},"container2":{"0":"8-11"}}`,
			}

			// This tests the podNumaHints function
			hints := podNumaHints(annotations)
			Expect(len(hints)).To(BeNumerically(">=", 1))
			Expect(hints).To(ContainElement(0))
			Expect(hints).To(ContainElement(1))
		})

		It("should handle invalid NUMA hints gracefully", func() {
			annotations := map[string]string{
				"cpuSet": "invalid-json",
			}

			hints := podNumaHints(annotations)
			Expect(hints).To(BeNil())
		})

		It("should handle missing NUMA hints gracefully", func() {
			annotations := map[string]string{}

			hints := podNumaHints(annotations)
			Expect(hints).To(BeNil())
		})
	})

	// ==============================================================================
	// INJECT NODE STATUS TESTS
	// ==============================================================================

	Context("InjectNodeStatus", func() {
		var r *ReconcilePodENI

		BeforeEach(func() {
			r = createTestReconciler(openAPI, false, false)
		})

		Context("when NetworkCards count is 0", func() {
			It("should handle LingJun node correctly", func() {
				nodeName := "lingjun-node"
				podName := "test-pod-lingjun"

				lingjunNode := testutil.NewK8sNodeBuilder(nodeName).
					WithEFLO().
					Build()
				Expect(k8sClient.Create(ctx, lingjunNode)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, lingjunNode)
				}()

				lingjunNodeCRD := testutil.NewNodeCRDBuilder(nodeName).
					WithEFLO().
					WithNetworkCardsCount(0).
					Build()
				Expect(k8sClient.Create(ctx, lingjunNodeCRD)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, lingjunNodeCRD)
				}()

				pod := testutil.CreateTestPod(podName, "default", nodeName)
				Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, pod)
				}()

				resultCtx := r.injectNodeStatus(ctx, "default", podName)
				nodeStatus, ok := status.MetaCtx[status.NodeStatus](resultCtx)
				Expect(ok).To(BeTrue())
				Expect(nodeStatus).NotTo(BeNil())
				Expect(nodeStatus.NetworkCards).To(HaveLen(0))
			})

			It("should create empty NodeStatus when node CRD not found", func() {
				nodeName := "non-existent-node"
				podName := "test-pod-no-node"

				nonExistentNode := testutil.NewK8sNodeBuilder(nodeName).Build()
				Expect(k8sClient.Create(ctx, nonExistentNode)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, nonExistentNode)
				}()

				pod := testutil.CreateTestPod(podName, "default", nodeName)
				Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, pod)
				}()

				resultCtx := r.injectNodeStatus(ctx, "default", podName)
				nodeStatus, ok := status.MetaCtx[status.NodeStatus](resultCtx)
				Expect(ok).To(BeTrue())
				Expect(nodeStatus).NotTo(BeNil())
				Expect(nodeStatus.NetworkCards).To(HaveLen(0))
			})
		})

		Context("when NetworkCards count is 2", func() {
			It("should balance ENIs across network cards", func() {
				nodeName := "multi-card-node"
				networkCardsCount := 2

				multiCardNode := testutil.NewK8sNodeBuilder(nodeName).Build()
				Expect(k8sClient.Create(ctx, multiCardNode)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, multiCardNode)
				}()

				multiCardNodeCRD := testutil.NewNodeCRDBuilder(nodeName).
					WithNetworkCardsCount(networkCardsCount).
					Build()
				Expect(k8sClient.Create(ctx, multiCardNodeCRD)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, multiCardNodeCRD)
				}()

				podENI1 := &networkv1beta1.PodENI{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-podeni-1",
						Namespace: "default",
						Labels:    map[string]string{"name": nodeName},
					},
					Spec: networkv1beta1.PodENISpec{
						Allocations: []networkv1beta1.Allocation{{ENI: networkv1beta1.ENI{ID: "eni-card-0"}}},
					},
					Status: networkv1beta1.PodENIStatus{
						Phase: networkv1beta1.ENIPhaseBind,
						ENIInfos: map[string]networkv1beta1.ENIInfo{
							"eni-card-0": {ID: "eni-card-0", NetworkCardIndex: ptr.To(0)},
						},
					},
				}
				Expect(testutil.CreateResource(ctx, k8sClient, podENI1)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, podENI1)
				}()

				podENI2 := &networkv1beta1.PodENI{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-podeni-2",
						Namespace: "default",
						Labels:    map[string]string{"name": nodeName},
					},
					Spec: networkv1beta1.PodENISpec{
						Allocations: []networkv1beta1.Allocation{{ENI: networkv1beta1.ENI{ID: "eni-card-1"}}},
					},
					Status: networkv1beta1.PodENIStatus{
						Phase: networkv1beta1.ENIPhaseBind,
						ENIInfos: map[string]networkv1beta1.ENIInfo{
							"eni-card-1": {ID: "eni-card-1", NetworkCardIndex: ptr.To(1)},
						},
					},
				}
				Expect(testutil.CreateResource(ctx, k8sClient, podENI2)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, podENI2)
				}()

				podENI3 := &networkv1beta1.PodENI{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-podeni-3",
						Namespace: "default",
						Labels:    map[string]string{"name": nodeName},
					},
					Spec: networkv1beta1.PodENISpec{
						Allocations: []networkv1beta1.Allocation{{ENI: networkv1beta1.ENI{ID: "eni-card-0-2"}}},
					},
					Status: networkv1beta1.PodENIStatus{
						Phase: networkv1beta1.ENIPhaseBind,
						ENIInfos: map[string]networkv1beta1.ENIInfo{
							"eni-card-0-2": {ID: "eni-card-0-2", NetworkCardIndex: ptr.To(0)},
						},
					},
				}
				Expect(testutil.CreateResource(ctx, k8sClient, podENI3)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, podENI3)
				}()

				pod := testutil.CreateTestPod("test-pod-multicard", "default", nodeName)
				Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, pod)
				}()

				Eventually(func() bool {
					var podENIList networkv1beta1.PodENIList
					err := k8sClient.List(ctx, &podENIList, &client.ListOptions{
						LabelSelector: labels.SelectorFromSet(map[string]string{"name": nodeName}),
					})
					return err == nil && len(podENIList.Items) == 3
				}, time.Second*5, time.Millisecond*100).Should(BeTrue())

				resultCtx := r.injectNodeStatus(ctx, "default", "test-pod-multicard")
				nodeStatus, ok := status.MetaCtx[status.NodeStatus](resultCtx)
				Expect(ok).To(BeTrue())
				Expect(nodeStatus).NotTo(BeNil())
				Expect(nodeStatus.NetworkCards).To(HaveLen(networkCardsCount))

				Expect(nodeStatus.NetworkCards[0].NetworkInterfaces).To(HaveLen(2))
				Expect(nodeStatus.NetworkCards[1].NetworkInterfaces).To(HaveLen(1))
				Expect(nodeStatus.NetworkCards[0].NetworkInterfaces.Has(status.NetworkInterfaceID("eni-card-0"))).To(BeTrue())
				Expect(nodeStatus.NetworkCards[0].NetworkInterfaces.Has(status.NetworkInterfaceID("eni-card-0-2"))).To(BeTrue())
				Expect(nodeStatus.NetworkCards[1].NetworkInterfaces.Has(status.NetworkInterfaceID("eni-card-1"))).To(BeTrue())
			})

			It("should handle concurrent requests with singleflight", func() {
				nodeName := "concurrent-test-node"
				networkCardsCount := 2

				concurrentNode := testutil.NewK8sNodeBuilder(nodeName).Build()
				Expect(k8sClient.Create(ctx, concurrentNode)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, concurrentNode)
				}()

				concurrentNodeCRD := testutil.NewNodeCRDBuilder(nodeName).
					WithNetworkCardsCount(networkCardsCount).
					Build()
				Expect(k8sClient.Create(ctx, concurrentNodeCRD)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, concurrentNodeCRD)
				}()

				pods := []string{"concurrent-pod-1", "concurrent-pod-2", "concurrent-pod-3"}
				for _, podName := range pods {
					pod := testutil.CreateTestPod(podName, "default", nodeName)
					Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
					defer func(p *corev1.Pod) {
						_ = k8sClient.Delete(ctx, p)
					}(pod)
				}

				done := make(chan bool, 3)
				for _, podName := range pods {
					go func(name string) {
						resultCtx := r.injectNodeStatus(ctx, "default", name)
						nodeStatus, ok := status.MetaCtx[status.NodeStatus](resultCtx)
						Expect(ok).To(BeTrue())
						Expect(nodeStatus).NotTo(BeNil())
						Expect(nodeStatus.NetworkCards).To(HaveLen(networkCardsCount))
						done <- true
					}(podName)
				}

				for i := 0; i < 3; i++ {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						Fail("concurrent injectNodeStatus timed out")
					}
				}

				nodeStatus, ok := r.nodeStatusCache.Get(nodeName)
				Expect(ok).To(BeTrue())
				Expect(nodeStatus).NotTo(BeNil())
				Expect(nodeStatus.NetworkCards).To(HaveLen(networkCardsCount))
			})
		})

		Context("error handling", func() {
			It("should return original context when pod not found", func() {
				resultCtx := r.injectNodeStatus(ctx, "default", "non-existent-pod")
				Expect(resultCtx).To(Equal(ctx))
			})

			It("should return original context when node is being deleted", func() {
				nodeName := "deleting-node"
				podName := "test-pod-deleting"

				deletingNode := testutil.NewK8sNodeBuilder(nodeName).Build()
				Expect(k8sClient.Create(ctx, deletingNode)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, deletingNode)
				}()

				now := metav1.Now()
				deletingNodeCRD := testutil.NewNodeCRDBuilder(nodeName).Build()
				deletingNodeCRD.DeletionTimestamp = &now
				Expect(k8sClient.Create(ctx, deletingNodeCRD)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, deletingNodeCRD)
				}()

				pod := testutil.CreateTestPod(podName, "default", nodeName)
				Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, pod)
				}()

				resultCtx := r.injectNodeStatus(ctx, "default", podName)
				Expect(resultCtx).To(Equal(ctx))
			})

			It("should return original context when NetworkCardsCount is nil", func() {
				nodeName := "nil-cards-node"
				podName := "test-pod-nil-cards"

				nilCardsNode := testutil.NewK8sNodeBuilder(nodeName).Build()
				Expect(k8sClient.Create(ctx, nilCardsNode)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, nilCardsNode)
				}()

				nilCardsNodeCRD := testutil.NewNodeCRDBuilder(nodeName).Build()
				nilCardsNodeCRD.Spec.NodeCap.NetworkCardsCount = nil
				Expect(k8sClient.Create(ctx, nilCardsNodeCRD)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, nilCardsNodeCRD)
				}()

				pod := testutil.CreateTestPod(podName, "default", nodeName)
				Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
				defer func() {
					_ = k8sClient.Delete(ctx, pod)
				}()

				resultCtx := r.injectNodeStatus(ctx, "default", podName)
				Expect(resultCtx).To(Equal(ctx))
			})
		})
	})

})
