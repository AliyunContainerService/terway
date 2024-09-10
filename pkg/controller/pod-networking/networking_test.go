package podnetworking

import (
	"context"
	"fmt"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
)

var _ = Describe("Networking controller", func() {
	Context("Create normal", func() {
		name := "normal-podnetworking"
		typeNamespacedName := types.NamespacedName{
			Name: name,
		}
		ctx := context.Background()

		It("Should create successfully", func() {
			created := &networkv1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: typeNamespacedName.Name,
				},
				Spec: networkv1beta1.PodNetworkingSpec{
					AllocationType: networkv1beta1.AllocationType{},
					Selector:       networkv1beta1.Selector{},
					VSwitchOptions: []string{"vsw-1"},
					ENIOptions: networkv1beta1.ENIOptions{
						ENIAttachType: networkv1beta1.ENIOptionTypeDefault,
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), created)).Should(Succeed())
		})

		switchPool, err := vswpool.NewSwitchPool(100, "10m")
		Expect(err).NotTo(HaveOccurred())

		openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
			AvailableIpAddressCount: 100,
			VSwitchId:               "vsw-1",
			ZoneId:                  "cn-hangzhou-k",
		}, nil)

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &ReconcilePodNetworking{
				client:       k8sClient,
				aliyunClient: openAPI,
				swPool:       switchPool,
				record:       record.NewFakeRecorder(100),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("Status Should Be Ready", func() {
			created := &networkv1beta1.PodNetworking{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), typeNamespacedName, created)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(created.Status.Status).Should(Equal(networkv1beta1.NetworkingStatusReady))
				g.Expect(len(created.Status.VSwitches)).Should(Equal(1))
			}, 5*time.Second, 500*time.Millisecond).Should(Succeed())
		})
	})

	Context("Create with not exist vSwitch", func() {
		name := "abnormal-podnetworking"
		typeNamespacedName := types.NamespacedName{
			Name: name,
		}
		ctx := context.Background()
		It("Should create successfully", func() {
			created := &networkv1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: typeNamespacedName.Name,
				},
				Spec: networkv1beta1.PodNetworkingSpec{
					AllocationType: networkv1beta1.AllocationType{},
					Selector:       networkv1beta1.Selector{},
					VSwitchOptions: []string{"vsw-not-exist"},
					ENIOptions: networkv1beta1.ENIOptions{
						ENIAttachType: networkv1beta1.ENIOptionTypeDefault,
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), created)).Should(Succeed())
		})

		switchPool, err := vswpool.NewSwitchPool(100, "10m")
		Expect(err).NotTo(HaveOccurred())

		openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-not-exist").Return(nil, fmt.Errorf("not found"))

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &ReconcilePodNetworking{
				client:       k8sClient,
				aliyunClient: openAPI,
				swPool:       switchPool,
				record:       record.NewFakeRecorder(100),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("Status Should Be Fail", func() {
			created := &networkv1beta1.PodNetworking{}
			Eventually(func(g Gomega) networkv1beta1.NetworkingStatus {
				err := k8sClient.Get(context.Background(), typeNamespacedName, created)
				g.Expect(err).NotTo(HaveOccurred())
				return created.Status.Status
			}, 5*time.Second, 500*time.Millisecond).Should(Equal(networkv1beta1.NetworkingStatusFail))
		})
	})

	Context("Modify config", func() {
		name := "modify"
		typeNamespacedName := types.NamespacedName{
			Name: name,
		}
		ctx := context.Background()

		It("Should create successfully", func() {
			created := &networkv1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: typeNamespacedName.Name,
				},
				Spec: networkv1beta1.PodNetworkingSpec{
					AllocationType: networkv1beta1.AllocationType{},
					Selector:       networkv1beta1.Selector{},
					VSwitchOptions: []string{"vsw-1", "vsw-2", "vsw-3"},
					ENIOptions: networkv1beta1.ENIOptions{
						ENIAttachType: networkv1beta1.ENIOptionTypeDefault,
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), created)).Should(Succeed())
		})

		switchPool, err := vswpool.NewSwitchPool(100, "10m")
		Expect(err).NotTo(HaveOccurred())

		openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
			AvailableIpAddressCount: 100,
			VSwitchId:               "vsw-1",
			ZoneId:                  "cn-hangzhou-k",
		}, nil)
		openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-2").Return(&vpc.VSwitch{
			AvailableIpAddressCount: 100,
			VSwitchId:               "vsw-2",
			ZoneId:                  "cn-hangzhou-k",
		}, nil)
		openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-3").Return(&vpc.VSwitch{
			AvailableIpAddressCount: 100,
			VSwitchId:               "vsw-3",
			ZoneId:                  "cn-hangzhou-k",
		}, nil)

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &ReconcilePodNetworking{
				client:       k8sClient,
				aliyunClient: openAPI,
				swPool:       switchPool,
				record:       record.NewFakeRecorder(100),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("Status Should Be Ready", func() {
			created := &networkv1beta1.PodNetworking{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), typeNamespacedName, created)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(created.Status.Status).Should(Equal(networkv1beta1.NetworkingStatusReady))
				g.Expect(len(created.Status.VSwitches)).Should(Equal(3))
			}, 5*time.Second, 500*time.Millisecond).Should(Succeed())
		})

		It("Modify exist pn", func() {
			pn := &networkv1beta1.PodNetworking{}
			Expect(k8sClient.Get(context.Background(), typeNamespacedName, pn)).Should(Succeed())
			pn.Spec.VSwitchOptions = []string{"vsw-1", "vsw-3"}
			Expect(k8sClient.Update(context.Background(), pn)).Should(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &ReconcilePodNetworking{
				client:       k8sClient,
				aliyunClient: openAPI,
				swPool:       switchPool,
				record:       record.NewFakeRecorder(100),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("Status Should Be Ready", func() {
			created := &networkv1beta1.PodNetworking{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), typeNamespacedName, created)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(created.Status.Status).Should(Equal(networkv1beta1.NetworkingStatusReady))
				g.Expect(len(created.Status.VSwitches)).Should(Equal(2))
			}, 5*time.Second, 500*time.Millisecond).Should(Succeed())
		})
	})
})
