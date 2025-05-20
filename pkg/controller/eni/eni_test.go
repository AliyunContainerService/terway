package eni

import (
	"context"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/controller/mocks"
)

var _ = Describe("Eni controller", func() {
	Context("delete eni test (eni not found)", func() {
		eniID := "eni-1"
		instacneID := "i-1"
		trunkID := "eni-trunk-1"

		typeNamespacedName := types.NamespacedName{
			Name: eniID,
		}
		var eni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()

			eni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: eniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:               eniID,
						MAC:              "mac",
						VPCID:            "",
						Zone:             "",
						VSwitchID:        "",
						ResourceGroupID:  "",
						SecurityGroupIDs: nil,
					},
					IPv4:        "",
					IPv6:        "",
					IPv4CIDR:    "",
					IPv6CIDR:    "",
					ExtraConfig: nil,
				},
			}

			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseDeleting,
				ENIInfo:    networkv1beta1.ENIInfo{},
				InstanceID: instacneID,
				TrunkENIID: trunkID,
			}
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("set eni DeletionTimestamp", func() {
			aliyun := mocks.NewInterface(GinkgoT())

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			ctx := context.Background()

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: eniID,
				},
			})

			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKeyFromObject(eni), eni)
			Expect(err).NotTo(HaveOccurred())

			Expect(eni.DeletionTimestamp.IsZero()).To(BeFalse())
		})

		It("eni is not found", func() {
			aliyun := mocks.NewInterface(GinkgoT())

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			ctx := context.Background()

			aliyun.On("DetachNetworkInterface", mock.Anything, eniID, instacneID, trunkID).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
			}).Return(
				nil, nil).Once()
			aliyun.On("DeleteNetworkInterfaceV2", mock.Anything, eniID).Return(nil).Once()

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				eni := &networkv1beta1.NetworkInterface{}
				err := k8sClient.Get(ctx, typeNamespacedName, eni)
				return k8sErr.IsNotFound(err)
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())
		})
	})

	Context("detach eni test", func() {
		eniID := "eni-2"
		instacneID := "i-2"
		trunkID := "eni-trunk-2"

		typeNamespacedName := types.NamespacedName{
			Name: eniID,
		}
		var eni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()

			eni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: eniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
					Labels: map[string]string{
						"k8s.aliyun.com/node": "node-1",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:               eniID,
						MAC:              "mac",
						VPCID:            "",
						Zone:             "",
						VSwitchID:        "",
						ResourceGroupID:  "",
						SecurityGroupIDs: nil,
					},
					IPv4:        "",
					IPv6:        "",
					IPv4CIDR:    "",
					IPv6CIDR:    "",
					ExtraConfig: nil,
				},
			}

			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseDetaching,
				ENIInfo:    networkv1beta1.ENIInfo{},
				InstanceID: instacneID,
				TrunkENIID: trunkID,
				NodeName:   "node-1",
			}
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should detach only", func() {
			aliyun := mocks.NewInterface(GinkgoT())

			aliyun.On("DetachNetworkInterface", mock.Anything, eniID, instacneID, trunkID).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
			}).Return(
				nil, nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			ctx := context.Background()

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: eniID,
				},
			})

			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				err = k8sClient.Get(ctx, typeNamespacedName, eni)
				Expect(err).NotTo(HaveOccurred())

				Expect(eni.DeletionTimestamp.IsZero()).To(BeTrue(), "delete timestamp should be zero")

				if eni.Status.Phase != networkv1beta1.ENIPhaseUnbind {
					return false
				}
				if eni.Status.ENIInfo.Status != networkv1beta1.ENIPhaseUnbind {
					return false
				}

				if eni.Labels["k8s.aliyun.com/node"] != "" {
					return false
				}
				return true

			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())

		})
	})

	Context("attach eni test", func() {
		eniID := "eni-attach"
		instanceID := "i-attach"
		trunkID := "eni-trunk-attach"

		typeNamespacedName := types.NamespacedName{
			Name: eniID,
		}
		var eni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()

			eni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: eniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:               eniID,
						MAC:              "mac",
						VPCID:            "",
						Zone:             "",
						VSwitchID:        "",
						ResourceGroupID:  "",
						SecurityGroupIDs: nil,
					},
					IPv4:        "",
					IPv6:        "",
					IPv4CIDR:    "",
					IPv6CIDR:    "",
					ExtraConfig: nil,
				},
			}

			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseBinding,
				ENIInfo:    networkv1beta1.ENIInfo{},
				InstanceID: instanceID,
				TrunkENIID: trunkID,
				NodeName:   "node-1",
			}
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should attach eni successfully", func() {
			aliyun := mocks.NewInterface(GinkgoT())

			aliyun.On("AttachNetworkInterface", mock.Anything, &aliyunClient.AttachNetworkInterfaceOptions{
				NetworkInterfaceID:     ptr.To(eniID),
				InstanceID:             ptr.To(instanceID),
				TrunkNetworkInstanceID: ptr.To(trunkID),
			}).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: eniID,
						Type:               "Secondary",
						DeviceIndex:        1,
						NetworkCardIndex:   0,
						Status:             "InUse",
					}}, nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			ctx := context.Background()

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, eni)
				Expect(err).NotTo(HaveOccurred())

				return eni.Status.Phase == networkv1beta1.ENIPhaseBind &&
					eni.Status.ENIInfo.Status == networkv1beta1.ENIStatusBind && eni.Labels["k8s.aliyun.com/node"] == "node-1"
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())
		})
	})

	Context("attach eni backoff test", func() {
		eniID := "eni-attach-backoff"
		instanceID := "i-attach"
		trunkID := "eni-trunk-attach"

		typeNamespacedName := types.NamespacedName{
			Name: eniID,
		}
		var eni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()

			eni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: eniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:               eniID,
						MAC:              "mac",
						VPCID:            "",
						Zone:             "",
						VSwitchID:        "",
						ResourceGroupID:  "",
						SecurityGroupIDs: nil,
					},
					IPv4:        "",
					IPv6:        "",
					IPv4CIDR:    "",
					IPv6CIDR:    "",
					ExtraConfig: nil,
				},
			}

			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseBinding,
				ENIInfo:    networkv1beta1.ENIInfo{},
				InstanceID: instanceID,
				TrunkENIID: trunkID,
				NodeName:   "node-1",
			}
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should attach eni successfully", func() {
			aliyun := mocks.NewInterface(GinkgoT())

			aliyun.On("AttachNetworkInterface", mock.Anything, &aliyunClient.AttachNetworkInterfaceOptions{
				NetworkInterfaceID:     ptr.To(eniID),
				InstanceID:             ptr.To(instanceID),
				TrunkNetworkInstanceID: ptr.To(trunkID),
			}).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: eniID,
						Type:               "Secondary",
						DeviceIndex:        1,
						NetworkCardIndex:   0,
						Status:             "Attaching",
					}}, nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			ctx := context.Background()

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(result.IsZero()).To(BeFalse())
		})
	})

	Context("delete eni test", func() {
		eniID := "eni-delete"
		instanceID := "i-delete"
		trunkID := "eni-trunk-delete"

		typeNamespacedName := types.NamespacedName{
			Name: eniID,
		}
		var eni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()

			eni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: eniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:               eniID,
						MAC:              "mac",
						VPCID:            "",
						Zone:             "",
						VSwitchID:        "",
						ResourceGroupID:  "",
						SecurityGroupIDs: nil,
					},
					IPv4:        "",
					IPv6:        "",
					IPv4CIDR:    "",
					IPv6CIDR:    "",
					ExtraConfig: nil,
				},
			}

			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseDeleting,
				ENIInfo:    networkv1beta1.ENIInfo{},
				InstanceID: instanceID,
				TrunkENIID: trunkID,
			}
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("trigger delete", func() {
			aliyun := mocks.NewInterface(GinkgoT())

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			ctx := context.Background()

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should delete eni successfully", func() {
			aliyun := mocks.NewInterface(GinkgoT())

			aliyun.On("DetachNetworkInterface", mock.Anything, eniID, instanceID, trunkID).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
			}).Return(
				nil, nil).Once()
			aliyun.On("DeleteNetworkInterfaceV2", mock.Anything, eniID).Return(nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			ctx := context.Background()

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, &networkv1beta1.NetworkInterface{})
				return k8sErr.IsNotFound(err)
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())
		})
	})

})
