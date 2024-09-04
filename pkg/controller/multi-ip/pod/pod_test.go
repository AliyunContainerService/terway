package pod

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pod Controller", func() {
	ctx := context.Background()

	BeforeEach(func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
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
		err := k8sClient.Create(ctx, pod)
		Expect(err).NotTo(HaveOccurred())
	})
	AfterEach(func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		}
		err := k8sClient.Delete(ctx, pod)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("Reconcile", func() {
		It("Reconcile should succeed", func() {
			n := &ReconcilePod{
				client: k8sClient,
			}
			_, err := n.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      "pod",
				},
			})

			Expect(err).To(BeNil())
		})
	})
})

var _ = Describe("NeedProcess Function", func() {
	var pod *corev1.Pod

	BeforeEach(func() {
		pod = &corev1.Pod{
			Spec: corev1.PodSpec{
				NodeName: "node1",
			},
		}
	})

	Context("When object is not a Pod", func() {
		It("returns false", func() {
			obj := &corev1.Service{}
			result := needProcess(obj)
			Expect(result).To(BeFalse())
		})
	})

	Context("When Pod has no NodeName", func() {
		It("returns false", func() {
			pod.Spec.NodeName = ""
			result := needProcess(pod)
			Expect(result).To(BeFalse())
		})
	})

	Context("When Pod uses HostNetwork", func() {
		It("returns false", func() {
			pod.Spec.HostNetwork = true
			result := needProcess(pod)
			Expect(result).To(BeFalse())
		})
	})

	Context("When Pod is ignored by Terway", func() {
		It("returns false", func() {
			pod.ObjectMeta.Labels = map[string]string{"k8s.aliyun.com/ignore-by-terway": "true"}
			result := needProcess(pod)
			Expect(result).To(BeFalse())
		})
	})

	Context("When Pod uses ENI", func() {
		It("returns false", func() {
			pod.ObjectMeta.Annotations = map[string]string{"k8s.aliyun.com/pod-eni": "true"}
			result := needProcess(pod)
			Expect(result).To(BeFalse())
		})
	})

	Context("When Pod sandbox not exited and has PodIP", func() {
		It("returns false", func() {
			pod.Status.PodIP = "192.168.1.1"
			result := needProcess(pod)
			Expect(result).To(BeFalse())
		})
	})

	Context("When Pod is ready to process", func() {
		It("returns true", func() {
			result := needProcess(pod)
			Expect(result).To(BeTrue())
		})
	})
})
