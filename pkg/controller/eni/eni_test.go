package eni

import (
	"context"
	"fmt"
	"testing"
	"time"

	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/internal/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var _ = Describe("Eni controller", func() {
	Context("Test init", func() {
		It("register should succeed", func() {
			openAPI := mocks.NewOpenAPI(GinkgoT())

			v, ok := register.Controllers[ControllerName]
			Expect(ok).To(BeTrue())

			mgr, ctx := testutil.NewManager(cfg, openAPI, k8sClient)
			err := v.Creator(mgr, ctx)

			Expect(err).To(Not(HaveOccurred()))
		})
	})

	Context("Reconcile ENI not found", func() {
		It("should return nil when ENI is not found", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())
			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}
			ctx := context.Background()

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "eni-nonexistent"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})
	})
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
			aliyun := mocks.NewOpenAPI(GinkgoT())

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
			aliyun := mocks.NewOpenAPI(GinkgoT())

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			ctx := context.Background()

			aliyun.On("DetachNetworkInterfaceV2", mock.Anything, &aliyunClient.DetachNetworkInterfaceOptions{
				NetworkInterfaceID: &eniID,
				InstanceID:         &instacneID,
				TrunkID:            &trunkID,
			}).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
				RawStatus:           ptr.To(true),
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
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("DetachNetworkInterfaceV2", mock.Anything, &aliyunClient.DetachNetworkInterfaceOptions{
				NetworkInterfaceID: &eniID,
				InstanceID:         &instacneID,
				TrunkID:            &trunkID,
			}).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
				RawStatus:           ptr.To(true),
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
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("AttachNetworkInterfaceV2", mock.Anything, &aliyunClient.AttachNetworkInterfaceOptions{
				NetworkInterfaceID:     ptr.To(eniID),
				InstanceID:             ptr.To(instanceID),
				TrunkNetworkInstanceID: ptr.To(trunkID),
			}).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
				RawStatus:           ptr.To(true),
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{
						Status:                      "InUse",
						MacAddress:                  "mac",
						NetworkInterfaceID:          eniID,
						VPCID:                       "vpcID",
						VSwitchID:                   "vswID",
						PrivateIPAddress:            "privateIPAddress",
						PrivateIPSets:               nil,
						ZoneID:                      "zoneID",
						SecurityGroupIDs:            []string{"sg-0"},
						ResourceGroupID:             "rg-0",
						IPv6Set:                     nil,
						Tags:                        nil,
						Type:                        "Secondary",
						InstanceID:                  "i-xx",
						TrunkNetworkInterfaceID:     "i-xx",
						NetworkInterfaceTrafficMode: "Standard",
						DeviceIndex:                 1,
						CreationTime:                "",
						NetworkCardIndex:            0,
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

			Expect(eni.Spec.ENI).To(Equal(networkv1beta1.ENI{
				ID:               eniID,
				MAC:              "mac",
				VPCID:            "vpcID",
				Zone:             "zoneID",
				VSwitchID:        "vswID",
				ResourceGroupID:  "rg-0",
				SecurityGroupIDs: []string{"sg-0"},
			}), "expect update spec succeed")

			Expect(eni.Status.ENIInfo).To(Equal(networkv1beta1.ENIInfo{
				ID:               eniID,
				Type:             "Secondary",
				Vid:              1,
				Status:           networkv1beta1.ENIStatusBind,
				NetworkCardIndex: ptr.To(0),
			}))
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
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("AttachNetworkInterfaceV2", mock.Anything, &aliyunClient.AttachNetworkInterfaceOptions{
				NetworkInterfaceID:     ptr.To(eniID),
				InstanceID:             ptr.To(instanceID),
				TrunkNetworkInstanceID: ptr.To(trunkID),
			}).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
				RawStatus:           ptr.To(true),
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
			aliyun := mocks.NewOpenAPI(GinkgoT())

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
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("DetachNetworkInterfaceV2", mock.Anything, &aliyunClient.DetachNetworkInterfaceOptions{
				NetworkInterfaceID: &eniID,
				InstanceID:         &instanceID,
				TrunkID:            &trunkID,
			}).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
				RawStatus:           ptr.To(true),
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

	Context("delete hdeni test", func() {
		hdeniID := "hdeni-delete"
		instanceID := "i-hdeni"
		trunkID := "eni-trunk-hdeni"

		typeNamespacedName := types.NamespacedName{Name: hdeniID}
		var hdeni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()
			hdeni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: hdeniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{ID: hdeniID},
				},
			}
			Expect(k8sClient.Create(ctx, hdeni)).To(Succeed())
			hdeni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseDeleting,
				InstanceID: instanceID,
				TrunkENIID: trunkID,
			}
			Expect(k8sClient.Status().Update(ctx, hdeni)).To(Succeed())
		})

		It("set DeletionTimestamp then delete hdeni via EFLOHDENI backend", func() {
			ctx := context.Background()
			Expect(k8sClient.Get(ctx, typeNamespacedName, hdeni)).To(Succeed())
			Expect(k8sClient.Delete(ctx, hdeni)).To(Succeed())

			aliyun := mocks.NewOpenAPI(GinkgoT())
			describeOpts := mock.MatchedBy(func(opts *aliyunClient.DescribeNetworkInterfaceOptions) bool {
				return opts != nil && opts.NetworkInterfaceIDs != nil && len(*opts.NetworkInterfaceIDs) > 0 && (*opts.NetworkInterfaceIDs)[0] == hdeniID
			})
			// First Reconcile: detach path for hdeni (Describe returns Unattached, then we clear status to Unbind)
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, describeOpts).
				Return([]*aliyunClient.NetworkInterface{{NetworkInterfaceID: hdeniID, Status: aliyunClient.LENIStatusUnattached}}, nil).Once()
			// Second Reconcile: delete() calls DeleteNetworkInterfaceV2 with hdeni backend
			aliyun.On("DeleteNetworkInterfaceV2", mock.Anything, hdeniID).Return(nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, &networkv1beta1.NetworkInterface{})
				return k8sErr.IsNotFound(err)
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())
		})
	})

	Context("delete eni when DeleteNetworkInterfaceV2 returns error", func() {
		eniID := "eni-delete-api-error"
		instanceID := "i-del-err"
		trunkID := "eni-trunk-err"

		typeNamespacedName := types.NamespacedName{Name: eniID}

		It("prepare and trigger delete", func() {
			ctx := context.Background()
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name:       eniID,
					Finalizers: []string{"network.alibabacloud.com/eni"},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{ENI: networkv1beta1.ENI{ID: eniID}},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())
			eni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseDeleting,
				InstanceID: instanceID,
				TrunkENIID: trunkID,
			}
			Expect(k8sClient.Status().Update(ctx, eni)).To(Succeed())
			Expect(k8sClient.Delete(ctx, eni)).To(Succeed())
		})

		It("should return error when DeleteNetworkInterfaceV2 fails", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())
			aliyun.On("DetachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Once()
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil, nil).Once()
			aliyun.On("DeleteNetworkInterfaceV2", mock.Anything, eniID).Return(fmt.Errorf("api delete failed")).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}
			ctx := context.Background()

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("api delete failed"))
		})
	})

	Context("delete leni test", func() {
		leniID := "leni-delete"
		instanceID := "i-leni-del"
		trunkID := "eni-trunk-leni-del"
		typeNamespacedName := types.NamespacedName{Name: leniID}
		var leni *networkv1beta1.NetworkInterface

		It("prepare and delete leni via EFLO backend", func() {
			ctx := context.Background()
			leni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name:       leniID,
					Finalizers: []string{"network.alibabacloud.com/eni"},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{ENI: networkv1beta1.ENI{ID: leniID}},
			}
			Expect(k8sClient.Create(ctx, leni)).To(Succeed())
			leni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseDeleting,
				InstanceID: instanceID,
				TrunkENIID: trunkID,
			}
			Expect(k8sClient.Status().Update(ctx, leni)).To(Succeed())
			Expect(k8sClient.Delete(ctx, leni)).To(Succeed())
		})

		It("detach then delete leni", func() {
			ctx := context.Background()
			aliyun := mocks.NewOpenAPI(GinkgoT())
			describeOpts := mock.MatchedBy(func(opts *aliyunClient.DescribeNetworkInterfaceOptions) bool {
				return opts != nil && opts.NetworkInterfaceIDs != nil && len(*opts.NetworkInterfaceIDs) > 0 && (*opts.NetworkInterfaceIDs)[0] == leniID
			})
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, describeOpts).
				Return([]*aliyunClient.NetworkInterface{{NetworkInterfaceID: leniID, Status: aliyunClient.LENIStatusUnattached}}, nil).Once()
			aliyun.On("DeleteNetworkInterfaceV2", mock.Anything, leniID).Return(nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, &networkv1beta1.NetworkInterface{})
				return k8sErr.IsNotFound(err)
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())
		})
	})

	Context("rollBackPodENI", func() {
		It("should do nothing when PodENIRef is nil", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())
			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "leni-noref"},
				Spec:       networkv1beta1.NetworkInterfaceSpec{},
			}
			err := r.rollBackPodENI(context.Background(), eni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should skip delete when PodENI already has DeletionTimestamp", func() {
			ctx := context.Background()
			podENIName := "pod-eni-deleting"
			podENINs := "default"
			leniID := "leni-rollback-deleting"

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{Name: podENIName, Namespace: podENINs},
			}
			Expect(k8sClient.Create(ctx, podENI)).To(Succeed())
			Expect(k8sClient.Delete(ctx, podENI)).To(Succeed())

			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: leniID},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					PodENIRef: &corev1.ObjectReference{Name: podENIName, Namespace: podENINs},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			aliyun := mocks.NewOpenAPI(GinkgoT())
			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			err := r.rollBackPodENI(ctx, eni)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// ==============================================================================
	// LENI (Lightweight ENI) TESTS
	// ==============================================================================

	Context("attach leni test", func() {
		leniID := "leni-attach"
		instanceID := "i-leni-attach"
		trunkID := "eni-trunk-leni"

		typeNamespacedName := types.NamespacedName{
			Name: leniID,
		}
		var leni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()

			leni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: leniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:               leniID,
						MAC:              "mac-leni",
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

			err := k8sClient.Create(ctx, leni)
			Expect(err).NotTo(HaveOccurred())

			leni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseBinding,
				ENIInfo:    networkv1beta1.ENIInfo{},
				InstanceID: instanceID,
				TrunkENIID: trunkID,
				NodeName:   "node-leni",
			}
			err = k8sClient.Status().Update(ctx, leni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should attach leni successfully when status is Available", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{leniID},
				RawStatus:           ptr.To(true),
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{
						Status:                      aliyunClient.LENIStatusAvailable,
						MacAddress:                  "mac-leni",
						NetworkInterfaceID:          leniID,
						VPCID:                       "vpcID-leni",
						VSwitchID:                   "vswID-leni",
						PrivateIPAddress:            "privateIPAddress-leni",
						PrivateIPSets:               nil,
						ZoneID:                      "zoneID-leni",
						SecurityGroupIDs:            []string{"sg-leni"},
						ResourceGroupID:             "rg-leni",
						IPv6Set:                     nil,
						Tags:                        nil,
						Type:                        "Secondary",
						InstanceID:                  instanceID,
						TrunkNetworkInterfaceID:     trunkID,
						NetworkInterfaceTrafficMode: "Standard",
						DeviceIndex:                 1,
						CreationTime:                "",
						NetworkCardIndex:            0,
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
				err := k8sClient.Get(ctx, typeNamespacedName, leni)
				Expect(err).NotTo(HaveOccurred())

				return leni.Status.Phase == networkv1beta1.ENIPhaseBind &&
					leni.Status.ENIInfo.Status == networkv1beta1.ENIStatusBind &&
					leni.Labels["k8s.aliyun.com/node"] == "node-leni"
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())

			Expect(leni.Spec.ENI).To(Equal(networkv1beta1.ENI{
				ID:               leniID,
				MAC:              "mac-leni",
				VPCID:            "vpcID-leni",
				Zone:             "zoneID-leni",
				VSwitchID:        "vswID-leni",
				ResourceGroupID:  "rg-leni",
				SecurityGroupIDs: []string{"sg-leni"},
			}), "expect update spec succeed")

			Expect(leni.Status.ENIInfo).To(Equal(networkv1beta1.ENIInfo{
				ID:               leniID,
				Type:             "Secondary",
				Vid:              1,
				Status:           networkv1beta1.ENIStatusBind,
				NetworkCardIndex: ptr.To(0),
			}))
		})

		It("should attach leni when status is Unattached", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())

			// Re-get leni to ensure we have the latest status
			ctx := context.Background()
			err := k8sClient.Get(ctx, typeNamespacedName, leni)
			Expect(err).NotTo(HaveOccurred())

			// Ensure Phase is Binding for attach to be called
			if leni.Status.Phase != networkv1beta1.ENIPhaseBinding {
				leni.Status.Phase = networkv1beta1.ENIPhaseBinding
				leni.Status.InstanceID = instanceID
				leni.Status.TrunkENIID = trunkID
				err = k8sClient.Status().Update(ctx, leni)
				Expect(err).NotTo(HaveOccurred())
			}

			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.DescribeNetworkInterfaceOptions) bool {
				return opts != nil && opts.NetworkInterfaceIDs != nil && len(*opts.NetworkInterfaceIDs) > 0 && (*opts.NetworkInterfaceIDs)[0] == leniID
			})).Return(
				[]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: leniID,
						Status:             aliyunClient.LENIStatusUnattached,
					}}, nil).Once()
			aliyun.On("AttachNetworkInterfaceV2", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.AttachNetworkInterfaceOptions) bool {
				return opts != nil && opts.NetworkInterfaceID != nil && *opts.NetworkInterfaceID == leniID &&
					opts.InstanceID != nil && *opts.InstanceID == instanceID &&
					opts.TrunkNetworkInstanceID != nil && *opts.TrunkNetworkInstanceID == trunkID
			})).Return(nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeFalse(), "should requeue when attaching")
		})

		It("should wait when leni status is Attaching", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())

			// Re-get leni to ensure we have the latest status
			ctx := context.Background()
			err := k8sClient.Get(ctx, typeNamespacedName, leni)
			Expect(err).NotTo(HaveOccurred())

			// Ensure Phase is Binding for attach to be called
			if leni.Status.Phase != networkv1beta1.ENIPhaseBinding {
				leni.Status.Phase = networkv1beta1.ENIPhaseBinding
				leni.Status.InstanceID = instanceID
				leni.Status.TrunkENIID = trunkID
				err = k8sClient.Status().Update(ctx, leni)
				Expect(err).NotTo(HaveOccurred())
			}

			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.DescribeNetworkInterfaceOptions) bool {
				return opts != nil && opts.NetworkInterfaceIDs != nil && len(*opts.NetworkInterfaceIDs) > 0 && (*opts.NetworkInterfaceIDs)[0] == leniID
			})).Return(
				[]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: leniID,
						Status:             aliyunClient.LENIStatusAttaching,
					}}, nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeFalse(), "should requeue when attaching")
		})
	})

	Context("detach leni test", func() {
		leniID := "leni-detach"
		instanceID := "i-leni-detach"
		trunkID := "eni-trunk-leni-detach"

		typeNamespacedName := types.NamespacedName{
			Name: leniID,
		}
		var leni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()

			leni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: leniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
					Labels: map[string]string{
						"k8s.aliyun.com/node": "node-leni",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:               leniID,
						MAC:              "mac-leni",
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

			err := k8sClient.Create(ctx, leni)
			Expect(err).NotTo(HaveOccurred())

			leni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseDetaching,
				ENIInfo:    networkv1beta1.ENIInfo{},
				InstanceID: instanceID,
				TrunkENIID: trunkID,
				NodeName:   "node-leni",
			}
			err = k8sClient.Status().Update(ctx, leni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should detach leni successfully when status is Available", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{leniID},
				RawStatus:           ptr.To(true),
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: leniID,
						Status:             aliyunClient.LENIStatusAvailable,
					}}, nil).Once()
			aliyun.On("DetachNetworkInterfaceV2", mock.Anything, &aliyunClient.DetachNetworkInterfaceOptions{
				NetworkInterfaceID: ptr.To(leniID),
				InstanceID:         ptr.To(instanceID),
			}).Return(nil).Once()

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
			Expect(result.IsZero()).To(BeFalse(), "should requeue when detaching")
		})

		It("should wait when leni status is Detaching", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{leniID},
				RawStatus:           ptr.To(true),
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: leniID,
						Status:             aliyunClient.LENIStatusDetaching,
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
			Expect(result.IsZero()).To(BeFalse(), "should requeue when detaching")
		})

		It("should handle leni status Unattached during detach", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{leniID},
				RawStatus:           ptr.To(true),
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: leniID,
						Status:             aliyunClient.LENIStatusUnattached,
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
				err = k8sClient.Get(ctx, typeNamespacedName, leni)
				Expect(err).NotTo(HaveOccurred())

				return leni.Status.Phase == networkv1beta1.ENIPhaseUnbind &&
					leni.Labels["k8s.aliyun.com/node"] == ""
			}, 5*time.Second, 500*time.Millisecond).Should(BeTrue())
		})
	})

	Context("leni attach failed test", func() {
		leniID := "leni-attach-failed"
		instanceID := "i-leni-attach-failed"
		trunkID := "eni-trunk-leni-failed"

		typeNamespacedName := types.NamespacedName{
			Name: leniID,
		}
		var leni *networkv1beta1.NetworkInterface

		It("prepare resources", func() {
			ctx := context.Background()

			leni = &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: leniID,
					Finalizers: []string{
						"network.alibabacloud.com/eni",
					},
				},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ENI: networkv1beta1.ENI{
						ID:               leniID,
						MAC:              "mac-leni",
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

			err := k8sClient.Create(ctx, leni)
			Expect(err).NotTo(HaveOccurred())

			leni.Status = networkv1beta1.NetworkInterfaceStatus{
				Phase:      networkv1beta1.ENIPhaseBinding,
				ENIInfo:    networkv1beta1.ENIInfo{},
				InstanceID: instanceID,
				TrunkENIID: trunkID,
				NodeName:   "node-leni",
			}
			err = k8sClient.Status().Update(ctx, leni)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error when DescribeNetworkInterfaceV2 fails for leni", func() {
			ctx := context.Background()
			Expect(k8sClient.Get(ctx, typeNamespacedName, leni)).To(Succeed())
			if leni.Status.Phase != networkv1beta1.ENIPhaseBinding {
				leni.Status.Phase = networkv1beta1.ENIPhaseBinding
				leni.Status.InstanceID = instanceID
				leni.Status.TrunkENIID = trunkID
				Expect(k8sClient.Status().Update(ctx, leni)).To(Succeed())
			}

			aliyun := mocks.NewOpenAPI(GinkgoT())
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.DescribeNetworkInterfaceOptions) bool {
				return opts != nil && opts.NetworkInterfaceIDs != nil && len(*opts.NetworkInterfaceIDs) > 0 && (*opts.NetworkInterfaceIDs)[0] == leniID
			})).Return(nil, fmt.Errorf("api describe error")).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("api describe error"))
		})

		It("should return error when leni not found from Describe", func() {
			ctx := context.Background()
			Expect(k8sClient.Get(ctx, typeNamespacedName, leni)).To(Succeed())
			if leni.Status.Phase != networkv1beta1.ENIPhaseBinding {
				leni.Status.Phase = networkv1beta1.ENIPhaseBinding
				leni.Status.InstanceID = instanceID
				leni.Status.TrunkENIID = trunkID
				Expect(k8sClient.Status().Update(ctx, leni)).To(Succeed())
			}

			aliyun := mocks.NewOpenAPI(GinkgoT())
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.DescribeNetworkInterfaceOptions) bool {
				return opts != nil && opts.NetworkInterfaceIDs != nil && len(*opts.NetworkInterfaceIDs) > 0 && (*opts.NetworkInterfaceIDs)[0] == leniID
			})).Return([]*aliyunClient.NetworkInterface{}, nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should return error for leni unsupported status on attach", func() {
			ctx := context.Background()
			Expect(k8sClient.Get(ctx, typeNamespacedName, leni)).To(Succeed())
			if leni.Status.Phase != networkv1beta1.ENIPhaseBinding {
				leni.Status.Phase = networkv1beta1.ENIPhaseBinding
				leni.Status.InstanceID = instanceID
				leni.Status.TrunkENIID = trunkID
				Expect(k8sClient.Status().Update(ctx, leni)).To(Succeed())
			}

			aliyun := mocks.NewOpenAPI(GinkgoT())
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.DescribeNetworkInterfaceOptions) bool {
				return opts != nil && opts.NetworkInterfaceIDs != nil && len(*opts.NetworkInterfaceIDs) > 0 && (*opts.NetworkInterfaceIDs)[0] == leniID
			})).Return([]*aliyunClient.NetworkInterface{
				{NetworkInterfaceID: leniID, Status: aliyunClient.LENIStatusDetachFailed},
			}, nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported status"))
		})

		It("should handle leni CreateFailed status", func() {
			aliyun := mocks.NewOpenAPI(GinkgoT())

			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{leniID},
				RawStatus:           ptr.To(true),
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{
						NetworkInterfaceID: leniID,
						Status:             aliyunClient.LENIStatusCreateFailed,
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
		})

		It("should delete PodENI when leni CreateFailed and PodENIRef is set", func() {
			ctx := context.Background()
			podENINs := "default"
			podENIName := "pod-eni-rollback"

			podENI := &networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podENIName,
					Namespace: podENINs,
				},
			}
			Expect(k8sClient.Create(ctx, podENI)).To(Succeed())

			// Update leni to reference the PodENI
			Expect(k8sClient.Get(ctx, typeNamespacedName, leni)).To(Succeed())
			leni.Spec.PodENIRef = &corev1.ObjectReference{
				Name:      podENIName,
				Namespace: podENINs,
			}
			Expect(k8sClient.Update(ctx, leni)).To(Succeed())

			aliyun := mocks.NewOpenAPI(GinkgoT())
			aliyun.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{leniID},
				RawStatus:           ptr.To(true),
			}).Return(
				[]*aliyunClient.NetworkInterface{
					{NetworkInterfaceID: leniID, Status: aliyunClient.LENIStatusCreateFailed},
				}, nil).Once()

			r := &ReconcileNetworkInterface{
				client:          k8sClient,
				scheme:          scheme.Scheme,
				aliyun:          aliyun,
				record:          record.NewFakeRecorder(1000),
				resourceBackoff: NewBackoffManager(),
			}

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			// rollBackPodENI should have deleted the PodENI
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: podENIName, Namespace: podENINs}, &networkv1beta1.PodENI{})
				return k8sErr.IsNotFound(err)
			}, 5*time.Second, 200*time.Millisecond).Should(BeTrue())
		})
	})

})

func TestToPtr(t *testing.T) {
	if toPtr("") != nil {
		t.Error("toPtr(\"\") should return nil")
	}
	s := "x"
	if p := toPtr("x"); p == nil || *p != s {
		t.Errorf("toPtr(\"x\") should return &\"x\", got %v", p)
	}
}
