package common

import (
	"context"
	"time"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
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
				BackOff: wait.Backoff{
					Duration: 100 * time.Millisecond,
					Factor:   1.0,
					Steps:    5,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status.Phase).To(Equal(networkv1beta1.Phase(networkv1beta1.ENIPhaseBind)))
		})

		It("should return error if ENI is not found and IgnoreNotExist is false", func() {
			_, err := WaitStatus(ctx, k8sClient, &DescribeOption{
				NetworkInterfaceID: "nonexistent-eni",
				IgnoreNotExist:     false,
				BackOff: wait.Backoff{
					Duration: 100 * time.Millisecond,
					Factor:   1.0,
					Steps:    1,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(k8sErr.IsNotFound(err)).To(BeTrue())
		})

		It("should ignore NotFound error if IgnoreNotExist is true", func() {
			result, err := WaitStatus(ctx, k8sClient, &DescribeOption{
				NetworkInterfaceID: "nonexistent-eni",
				IgnoreNotExist:     true,
				BackOff: wait.Backoff{
					Duration: 100 * time.Millisecond,
					Factor:   1.0,
					Steps:    1,
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
				BackOff: wait.Backoff{
					Duration: 100 * time.Millisecond,
					Factor:   1.0,
					Steps:    1,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("eni cr phase"))
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
				BackOff: wait.Backoff{
					Duration: 100 * time.Millisecond,
					Factor:   1.0,
					Steps:    2,
				},
			})
			Expect(err).To(HaveOccurred())
		})
	})
})
