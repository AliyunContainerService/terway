//go:build test_env

package pod

import (
	"context"
	"time"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Pod controller", func() {
	Context("create pod", func() {
		ctx := context.Background()
		name := "normal-pod"
		ns := "default"
		key := k8stypes.NamespacedName{Name: name, Namespace: ns}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Annotations: map[string]string{
					types.PodENI:        "true",
					types.PodNetworking: "elastic-ip-podnetworking",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-az-1",
				Containers: []corev1.Container{
					{
						Name:  "foo",
						Image: "busybox",
					},
				},
			},
		}
		podENI := &networkv1beta1.PodENI{}
		It("Create pod should succeed", func() {
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
		})
		It("PodENI status should be Ready", func() {
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, key, podENI)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(podENI.Spec.Allocations)).Should(Equal(1))
			}, 5*time.Second, 500*time.Millisecond).ShouldNot(HaveOccurred())
		})
		It("Pod UID should be stored", func() {
			Expect(podENI.Annotations[types.PodUID]).Should(BeEquivalentTo(pod.UID))
		})
	})

	Context("create fixed-ip pod", func() {
		ctx := context.Background()

		key := k8stypes.NamespacedName{Name: "fixed-ip-pod", Namespace: "default"}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Annotations: map[string]string{
					types.PodENI:        "true",
					types.PodNetworking: "fixed-ip-podnetworking",
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind:       "foo",
						Name:       "foo",
						UID:        "foo",
						APIVersion: "foo",
					},
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-az-1",
				Containers: []corev1.Container{
					{
						Name:  "foo",
						Image: "busybox",
					},
				},
			},
		}
		podENI := &networkv1beta1.PodENI{}
		It("Create pod should succeed", func() {
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
		})
		It("PodENI status should be Ready", func() {
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, key, podENI)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(podENI.Spec.Allocations)).Should(Equal(1))
			}, 5*time.Second, 500*time.Millisecond).ShouldNot(HaveOccurred())
		})
		It("Pod UID should be stored", func() {
			Expect(podENI.Annotations[types.PodUID]).Should(BeEquivalentTo(pod.UID))
		})
	})
})
