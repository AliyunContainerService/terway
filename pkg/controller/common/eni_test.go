package common

import (
	"context"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Common ENI Operations", func() {
	var ctx context.Context
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	BeforeEach(func() {
		// 使用 fake.NewClientBuilder 创建假客户端
		ctx = context.Background()
	})
	Context("Attach ENI", func() {
		It("should attach ENI successfully", func() {
			// 初始化一个 NetworkInterface 对象
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-attach"},
			}
			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status.Phase = networkv1beta1.ENIPhaseUnbind
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, client.ObjectKey{Name: "eni-attach"}, eni)
			Expect(err).NotTo(HaveOccurred())

			// 调用 Attach 方法
			err = Attach(ctx, k8sClient, &AttachOption{
				InstanceID:         "i-1",
				NetworkInterfaceID: "eni-attach",
				TrunkENIID:         "trunk-1",
				NodeName:           "node-1",
			})
			Expect(err).NotTo(HaveOccurred())

			// 验证状态是否正确更新
			updatedENI := &networkv1beta1.NetworkInterface{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "eni-attach"}, updatedENI)).To(Succeed())
			Expect(string(updatedENI.Status.Phase)).To(Equal(networkv1beta1.ENIPhaseBinding))
			Expect(updatedENI.Status.InstanceID).To(Equal("i-1"))
			Expect(updatedENI.Status.TrunkENIID).To(Equal("trunk-1"))
			Expect(updatedENI.Status.NodeName).To(Equal("node-1"))
		})

		It("should return error for invalid options", func() {
			err := Attach(ctx, k8sClient, &AttachOption{})
			Expect(err).To(HaveOccurred())
		})

		It("should return error when InstanceID is empty", func() {
			err := Attach(ctx, k8sClient, &AttachOption{
				InstanceID:         "",
				NetworkInterfaceID: "eni-1",
				NodeName:           "node-1",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("instance id is empty"))
		})

		It("should return error when NetworkInterfaceID is empty", func() {
			err := Attach(ctx, k8sClient, &AttachOption{
				InstanceID:         "i-1",
				NetworkInterfaceID: "",
				NodeName:           "node-1",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("network interface id is empty"))
		})

		It("should return error when NodeName is empty", func() {
			err := Attach(ctx, k8sClient, &AttachOption{
				InstanceID:         "i-1",
				NetworkInterfaceID: "eni-1",
				NodeName:           "",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("node name is empty"))
		})

		It("should return error if ENI is in detaching phase", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-detaching"},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())
			eni.Status.Phase = networkv1beta1.ENIPhaseDetaching
			err := k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			err = Attach(ctx, k8sClient, &AttachOption{
				InstanceID:         "i-1",
				NetworkInterfaceID: "eni-detaching",
				TrunkENIID:         "trunk-1",
				NodeName:           "node-1",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("eni cr phase"))
		})

		It("should return nil if ENI is already in Bind phase", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-already-bind"},
				Status:     networkv1beta1.NetworkInterfaceStatus{Phase: networkv1beta1.ENIPhaseBind},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Attach(ctx, k8sClient, &AttachOption{
				InstanceID:         "i-1",
				NetworkInterfaceID: "eni-already-bind",
				NodeName:           "node-1",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error when ENI is not found", func() {
			err := Attach(ctx, k8sClient, &AttachOption{
				InstanceID:         "i-1",
				NetworkInterfaceID: "eni-not-exist",
				NodeName:           "node-1",
			})
			Expect(err).To(HaveOccurred())
			Expect(k8sErr.IsNotFound(err)).To(BeTrue())
		})
	})
	Context("Detach ENI", func() {
		It("should detach ENI successfully", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-detach"},
			}
			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status.Phase = networkv1beta1.ENIPhaseBind
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			err = Detach(ctx, k8sClient, &DetachOption{
				NetworkInterfaceID: "eni-detach",
			})
			Expect(err).NotTo(HaveOccurred())

			updatedENI := &networkv1beta1.NetworkInterface{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "eni-detach"}, updatedENI)).To(Succeed())
			Expect(string(updatedENI.Status.Phase)).To(Equal(networkv1beta1.ENIPhaseDetaching))
		})

		It("should do nothing if ENI is already detached", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-unbind"},
				Status: networkv1beta1.NetworkInterfaceStatus{
					Phase: networkv1beta1.ENIPhaseUnbind,
				},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Detach(ctx, k8sClient, &DetachOption{
				NetworkInterfaceID: "eni-unbind",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should ignore cached ENI", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-cached-1"},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ManagePolicy: networkv1beta1.ManagePolicy{Cache: true},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Detach(ctx, k8sClient, &DetachOption{
				NetworkInterfaceID: "eni-cached-1",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error if ENI is in binding phase", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-binding"},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())
			eni.Status.Phase = networkv1beta1.ENIPhaseBinding
			Expect(k8sClient.Status().Update(ctx, eni)).To(Succeed())

			err := Detach(ctx, k8sClient, &DetachOption{
				NetworkInterfaceID: "eni-binding",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("eni cr phase"))
		})

		It("should do nothing if ENI is UnManaged", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-unmanaged"},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ManagePolicy: networkv1beta1.ManagePolicy{UnManaged: true},
				},
				Status: networkv1beta1.NetworkInterfaceStatus{Phase: networkv1beta1.ENIPhaseBind},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Detach(ctx, k8sClient, &DetachOption{
				NetworkInterfaceID: "eni-unmanaged",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return nil when ENI is not found", func() {
			err := Detach(ctx, k8sClient, &DetachOption{
				NetworkInterfaceID: "eni-not-exist-detach",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should detach ENI in Initial phase", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-initial-phase"},
				Spec:       networkv1beta1.NetworkInterfaceSpec{},
				Status:     networkv1beta1.NetworkInterfaceStatus{Phase: networkv1beta1.ENIPhaseInitial},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Detach(ctx, k8sClient, &DetachOption{
				NetworkInterfaceID: "eni-initial-phase",
			})
			Expect(err).NotTo(HaveOccurred())

			updatedENI := &networkv1beta1.NetworkInterface{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "eni-initial-phase"}, updatedENI)).To(Succeed())
			Expect(updatedENI.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseDetaching)))
		})
	})
	Context("Delete ENI", func() {
		It("should delete ENI successfully", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-delete"},
			}
			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status.Phase = networkv1beta1.ENIPhaseBind
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			err = Delete(ctx, k8sClient, &DeleteOption{
				NetworkInterfaceID: "eni-delete",
			})
			Expect(err).NotTo(HaveOccurred())

			updatedENI := &networkv1beta1.NetworkInterface{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "eni-delete"}, updatedENI)).To(Succeed())
			Expect(string(updatedENI.Status.Phase)).To(Equal(networkv1beta1.ENIPhaseDeleting))
		})

		It("should do nothing if ENI is already deleting", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-deleting"},
				Status: networkv1beta1.NetworkInterfaceStatus{
					Phase: networkv1beta1.ENIPhaseDeleting,
				},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Delete(ctx, k8sClient, &DeleteOption{
				NetworkInterfaceID: "eni-deleting",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should ignore cached ENI", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-cached-2"},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ManagePolicy: networkv1beta1.ManagePolicy{Cache: true},
				},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Delete(ctx, k8sClient, &DeleteOption{
				NetworkInterfaceID: "eni-cached-2",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should use provided Obj when set", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-delete-obj"},
				Status:     networkv1beta1.NetworkInterfaceStatus{Phase: networkv1beta1.ENIPhaseBind},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Delete(ctx, k8sClient, &DeleteOption{
				NetworkInterfaceID: "eni-delete-obj",
				Obj:                eni,
			})
			Expect(err).NotTo(HaveOccurred())

			updatedENI := &networkv1beta1.NetworkInterface{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "eni-delete-obj"}, updatedENI)).To(Succeed())
			Expect(string(updatedENI.Status.Phase)).To(Equal(networkv1beta1.ENIPhaseDeleting))
		})

		It("should do nothing if ENI is UnManaged", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-delete-unmanaged"},
				Spec: networkv1beta1.NetworkInterfaceSpec{
					ManagePolicy: networkv1beta1.ManagePolicy{UnManaged: true},
				},
				Status: networkv1beta1.NetworkInterfaceStatus{Phase: networkv1beta1.ENIPhaseBind},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())

			err := Delete(ctx, k8sClient, &DeleteOption{
				NetworkInterfaceID: "eni-delete-unmanaged",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return nil when ENI is not found", func() {
			err := Delete(ctx, k8sClient, &DeleteOption{
				NetworkInterfaceID: "eni-not-exist-delete",
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
	Context("WaitStatus", func() {
		It("should wait for ENI status to match expected phase", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-wait"},
			}
			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status.Phase = networkv1beta1.ENIPhaseUnbind
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			go func() {
				time.Sleep(50 * time.Millisecond)
				updatedENI := &networkv1beta1.NetworkInterface{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "eni-wait"}, updatedENI)).To(Succeed())
				updatedENI.Status.Phase = networkv1beta1.ENIPhaseBind
				Expect(k8sClient.Status().Update(ctx, updatedENI)).To(Succeed())
			}()

			result, err := WaitStatus(ctx, k8sClient, &DescribeOption{
				NetworkInterfaceID: "eni-wait",
				ExpectPhase:        ptr.To(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)),
				BackOff: backoff.ExtendedBackoff{
					Backoff: wait.Backoff{
						Duration: 100 * time.Millisecond,
						Factor:   1.0,
						Steps:    5,
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})

		It("should return error if ENI is not found and IgnoreNotExist is false", func() {
			_, err := WaitStatus(ctx, k8sClient, &DescribeOption{
				NetworkInterfaceID: "nonexistent-eni",
				IgnoreNotExist:     false,
				BackOff: backoff.ExtendedBackoff{
					Backoff: wait.Backoff{
						Duration: 100 * time.Millisecond,
						Factor:   1.0,
						Steps:    1,
					},
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(k8sErr.IsNotFound(err)).To(BeTrue())
		})

		It("should ignore NotFound error if IgnoreNotExist is true", func() {
			result, err := WaitStatus(ctx, k8sClient, &DescribeOption{
				NetworkInterfaceID: "nonexistent-eni",
				IgnoreNotExist:     true,
				BackOff: backoff.ExtendedBackoff{
					Backoff: wait.Backoff{
						Duration: 100 * time.Millisecond,
						Factor:   1.0,
						Steps:    1,
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("should return error if ENI phase does not match expected phase", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-mismatch"},
			}
			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status.Phase = networkv1beta1.ENIPhaseUnbind
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			_, err = WaitStatus(ctx, k8sClient, &DescribeOption{
				NetworkInterfaceID: "eni-mismatch",
				ExpectPhase:        ptr.To(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)),
				BackOff: backoff.ExtendedBackoff{
					Backoff: wait.Backoff{
						Duration: 100 * time.Millisecond,
						Factor:   1.0,
						Steps:    1,
					},
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("eni cr phase"))
		})

		It("should default BackOff.Steps to 1 when zero", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-steps-zero"},
			}
			Expect(k8sClient.Create(ctx, eni)).To(Succeed())
			eni.Status.Phase = networkv1beta1.ENIPhaseUnbind
			Expect(k8sClient.Status().Update(ctx, eni)).To(Succeed())

			opt := &DescribeOption{
				NetworkInterfaceID: "eni-steps-zero",
				ExpectPhase:        ptr.To(networkv1beta1.Phase(networkv1beta1.ENIPhaseUnbind)),
				BackOff: backoff.ExtendedBackoff{
					Backoff: wait.Backoff{
						Duration: 10 * time.Millisecond,
						Factor:   1.0,
						Steps:    0, // will be set to 1
					},
				},
			}
			result, err := WaitStatus(ctx, k8sClient, opt)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseUnbind)))
		})

		It("should return timeout error if ENI status does not update in time", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "eni-timeout"},
			}
			err := k8sClient.Create(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			eni.Status.Phase = networkv1beta1.ENIPhaseUnbind
			err = k8sClient.Status().Update(ctx, eni)
			Expect(err).NotTo(HaveOccurred())

			_, err = WaitStatus(ctx, k8sClient, &DescribeOption{
				NetworkInterfaceID: "eni-timeout",
				ExpectPhase:        ptr.To(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)),
				BackOff: backoff.ExtendedBackoff{
					Backoff: wait.Backoff{
						Duration: 100 * time.Millisecond,
						Factor:   1.0,
						Steps:    2,
					},
				},
			})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("ToNetworkInterfaceCR", func() {
		It("should convert aliyunClient.NetworkInterface to NetworkInterface CR", func() {
			aliyunENI := &aliyunClient.NetworkInterface{
				NetworkInterfaceID: "eni-123",
				MacAddress:         "00:11:22:33:44:55",
				VPCID:              "vpc-123",
				ZoneID:             "zone-1",
				VSwitchID:          "vsw-123",
				ResourceGroupID:    "rg-123",
				SecurityGroupIDs:   []string{"sg-1", "sg-2"},
				PrivateIPAddress:   "192.168.1.100",
				IPv6Set: []aliyunClient.IPSet{
					{IPAddress: "2001:db8::1"},
				},
			}

			result := ToNetworkInterfaceCR(aliyunENI)

			Expect(result.Name).To(Equal("eni-123"))
			Expect(result.Spec.ENI.ID).To(Equal("eni-123"))
			Expect(result.Spec.ENI.MAC).To(Equal("00:11:22:33:44:55"))
			Expect(result.Spec.IPv4).To(Equal("192.168.1.100"))
			Expect(result.Spec.IPv6).To(Equal("2001:db8::1"))
		})

		It("should set empty IPv6 when IPv6Set is empty", func() {
			aliyunENI := &aliyunClient.NetworkInterface{
				NetworkInterfaceID: "eni-456",
				MacAddress:         "00:11:22:33:44:66",
				VPCID:              "vpc-123",
				ZoneID:             "zone-1",
				VSwitchID:          "vsw-123",
				PrivateIPAddress:   "192.168.1.101",
				IPv6Set:            nil,
			}

			result := ToNetworkInterfaceCR(aliyunENI)

			Expect(result.Spec.IPv6).To(Equal(""))
		})
	})

	Context("WaitCreated", func() {
		It("should wait for object to be created successfully", func() {
			obj := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: "wait-created-test",
				},
			}
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())

			err := WaitCreated(ctx, k8sClient, obj, "", "wait-created-test")
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error when object is never created within timeout", func() {
			obj := &networkv1beta1.NetworkInterface{}
			err := WaitCreated(ctx, k8sClient, obj, "", "never-created-name")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("WaitDeleted", func() {
		It("should wait for object to be deleted successfully", func() {
			obj := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: "wait-deleted-test",
				},
			}
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
			Expect(k8sClient.Delete(ctx, obj)).To(Succeed())

			WaitDeleted(ctx, k8sClient, obj, "", "wait-deleted-test")

			err := k8sClient.Get(ctx, client.ObjectKey{Name: "wait-deleted-test"}, obj)
			Expect(k8sErr.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("WaitRVChanged", func() {
		It("should wait for resource version to change", func() {
			obj := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name: "wait-rv-test",
				},
			}
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())

			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wait-rv-test"}, obj)).To(Succeed())
			initialRV := obj.GetResourceVersion()

			update := obj.DeepCopy()

			go func() {
				time.Sleep(50 * time.Millisecond)
				update.Spec.IPv4 = "192.168.1.100"
				Expect(k8sClient.Update(ctx, update)).To(Succeed())
			}()

			err := WaitRVChanged(ctx, k8sClient, obj, "", "wait-rv-test", initialRV)
			Expect(err).NotTo(HaveOccurred())
			Expect(obj.GetResourceVersion()).NotTo(Equal(initialRV))
		})

		It("should return error when resource version does not change within timeout", func() {
			obj := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "wait-rv-timeout"},
			}
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wait-rv-timeout"}, obj)).To(Succeed())
			currentRV := obj.GetResourceVersion()

			err := WaitRVChanged(ctx, k8sClient, obj, "", "wait-rv-timeout", currentRV)
			Expect(err).To(HaveOccurred())
		})
	})
})
