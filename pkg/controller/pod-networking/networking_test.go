//go:build test_env

package podnetworking

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var _ = Describe("Networking controller", func() {
	Context("Create normal", func() {
		name := "normal-podnetworking"
		key := types.NamespacedName{Name: name}

		It("Should create successfully", func() {
			created := &networkv1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: key.Name,
				},
				Spec: networkv1beta1.PodNetworkingSpec{
					AllocationType: networkv1beta1.AllocationType{},
					Selector:       networkv1beta1.Selector{},
					VSwitchOptions: []string{"vsw-1"},
				},
			}
			Expect(k8sClient.Create(context.Background(), created)).Should(Succeed())
		})
		It("Status Should Be Ready", func() {
			created := &networkv1beta1.PodNetworking{}
			Eventually(func(g Gomega) networkv1beta1.NetworkingStatus {
				err := k8sClient.Get(context.Background(), key, created)
				g.Expect(err).NotTo(HaveOccurred())
				return created.Status.Status
			}, 5*time.Second, 500*time.Millisecond).Should(Equal(networkv1beta1.NetworkingStatusReady))
		})
	})

	Context("Create with not exist vSwitch", func() {
		name := "abnormal-podnetworking"
		key := types.NamespacedName{Name: name}
		It("Should create successfully", func() {
			created := &networkv1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: key.Name,
				},
				Spec: networkv1beta1.PodNetworkingSpec{
					AllocationType: networkv1beta1.AllocationType{},
					Selector:       networkv1beta1.Selector{},
					VSwitchOptions: []string{"vsw-not-exist"},
				},
			}
			Expect(k8sClient.Create(context.Background(), created)).Should(Succeed())
		})
		It("Status Should Be Fail", func() {
			created := &networkv1beta1.PodNetworking{}
			Eventually(func(g Gomega) networkv1beta1.NetworkingStatus {
				err := k8sClient.Get(context.Background(), key, created)
				g.Expect(err).NotTo(HaveOccurred())
				return created.Status.Status
			}, 5*time.Second, 500*time.Millisecond).Should(Equal(networkv1beta1.NetworkingStatusFail))
		})
	})
})
