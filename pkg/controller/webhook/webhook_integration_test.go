package webhook

import (
	"context"
	"crypto/tls"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

var _ = Describe("Webhook", func() {
	var c client.Client
	var obj *corev1.Pod

	var ctx context.Context
	var cancel context.CancelFunc
	var server webhook.Server

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = admissionv1.AddToScheme(scheme)
	BeforeEach(func() {
		Expect(cfg).NotTo(BeNil())
		var err error
		c, err = client.New(cfg, client.Options{
			Scheme: scheme,
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel = context.WithCancel(context.Background())
		m, err := manager.New(cfg, manager.Options{
			Scheme: scheme,
			WebhookServer: webhook.NewServer(webhook.Options{
				Port:    testenv.WebhookInstallOptions.LocalServingPort,
				Host:    testenv.WebhookInstallOptions.LocalServingHost,
				CertDir: testenv.WebhookInstallOptions.LocalServingCertDir,
				TLSOpts: []func(*tls.Config){func(config *tls.Config) {}},
			}),
		}) // we need manager here just to leverage manager.SetFields
		Expect(err).NotTo(HaveOccurred())
		server = m.GetWebhookServer()
	})

	AfterEach(func() {
		cancel()
	})

	Context("pod webhook", func() {
		It("test pod create trunk eni", func() {

			server.Register("/mutating", MutatingHook(c, &controlplane.Config{
				EnableWebhookInjectResource: ptr.To(true),
				EnableTrunk:                 ptr.To(true),
				IPAMType:                    "",
			}))
			server.Register("/validate", ValidateHook())

			go func() {
				err := server.Start(ctx)
				Expect(err).NotTo(HaveOccurred())
			}()

			By("create a hostNetwork Pod")
			Eventually(func() bool {
				obj = &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod-host-network",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						HostNetwork: true,
						Containers: []corev1.Container{
							{
								Name:  "test-container",
								Image: "busybox",
							},
						},
					},
				}

				err := c.Create(context.TODO(), obj)
				return err == nil
			}, 1*time.Second).Should(BeTrue())

			_ = c.Delete(context.TODO(), obj)

			By("create pod with conflict config")
			Eventually(func() bool {
				obj = &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Annotations: map[string]string{
							"k8s.aliyun.com/pod-networks":         "true",
							"k8s.aliyun.com/pod-networks-request": "true",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test-container",
								Image: "busybox",
							},
						},
					},
				}

				err := c.Create(context.TODO(), obj)
				return err != nil && strings.Contains(err.Error(), "exclusive")
			}, 1*time.Second).Should(BeTrue())

			By("use pod-networks annotation")
			podNetworks := `
{
  "podNetworks": [
    {
      "vSwitchOptions": [
        "vsw-1",
        "vsw-2",
        "vsw-3"
      ],
      "interface": "eth0",
      "securityGroupIDs": [
        "sg-1"
      ]
    },
    {
      "vSwitchOptions": [
        "vsw-1",
        "vsw-2",
        "vsw-3"
      ],
      "interface": "eth1",
      "securityGroupIDs": [
        "sg-2"
      ],
      "defaultRoute": true
    }
  ]
}`

			obj = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.aliyun.com/pod-networks": podNetworks,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "busybox",
						},
					},
				},
			}

			err := c.Create(context.TODO(), obj)
			Expect(err).NotTo(HaveOccurred())

			err = c.Get(context.TODO(), client.ObjectKeyFromObject(obj), obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Annotations["k8s.aliyun.com/pod-eni"]).To(Equal("true"))

			Expect(obj.Spec.Containers[0].Resources.Limits["aliyun/member-eni"].Equal(resource.MustParse(strconv.Itoa(2)))).To(BeTrue())

			_ = c.Delete(context.TODO(), obj)

			cancel()
		})

		It("test pod create use pod requests", func() {

			server.Register("/mutating", MutatingHook(c, &controlplane.Config{
				EnableWebhookInjectResource: ptr.To(true),
				EnableTrunk:                 ptr.To(true),
				IPAMType:                    "",
			}))
			server.Register("/validate", ValidateHook())

			go func() {
				err := server.Start(ctx)
				Expect(err).NotTo(HaveOccurred())
			}()

			By("create a hostNetwork Pod")
			Eventually(func() bool {
				obj = &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod-host-network",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						HostNetwork: true,
						Containers: []corev1.Container{
							{
								Name:  "test-container",
								Image: "busybox",
							},
						},
					},
				}

				err := c.Create(context.TODO(), obj)
				return err == nil
			}, 1*time.Second).Should(BeTrue())

			_ = c.Delete(context.TODO(), obj)

			By("use pod-networks annotation")

			defaultPn := &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
				},
				Spec: v1beta1.PodNetworkingSpec{
					ENIOptions: v1beta1.ENIOptions{
						ENIAttachType: v1beta1.ENIOptionTypeDefault,
					},
					AllocationType:   v1beta1.AllocationType{},
					Selector:         v1beta1.Selector{},
					SecurityGroupIDs: []string{"sg-1"},
					VSwitchOptions:   []string{"vsw-1", "vsw-2", "vsw-3"},
					VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
						VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
					},
				},
				Status: v1beta1.PodNetworkingStatus{
					Status: v1beta1.NetworkingStatusReady,
					VSwitches: []v1beta1.VSwitch{
						{
							ID:   "vsw-1",
							Zone: "zone-1",
						},
						{
							ID:   "vsw-2",
							Zone: "zone-2",
						},
						{
							ID:   "vsw-3",
							Zone: "zone-2",
						},
					},
					UpdateAt: metav1.Time{},
					Message:  "",
				},
			}
			update := defaultPn.DeepCopy()
			_, err := controllerutil.CreateOrPatch(ctx, c, update, func() error {
				update.Annotations = defaultPn.Annotations
				update.Labels = defaultPn.Labels
				update.Spec = defaultPn.Spec
				update.Status = defaultPn.Status
				return nil
			})

			Expect(err).NotTo(HaveOccurred())

			_, err = controllerutil.CreateOrPatch(ctx, c, update, func() error {
				update.Annotations = defaultPn.Annotations
				update.Labels = defaultPn.Labels
				update.Spec = defaultPn.Spec
				update.Status = defaultPn.Status
				return nil
			})

			Expect(err).NotTo(HaveOccurred())

			secondaryPn := &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: "secondary",
				},
				Spec: v1beta1.PodNetworkingSpec{
					ENIOptions: v1beta1.ENIOptions{
						ENIAttachType: v1beta1.ENIOptionTypeDefault,
					},
					AllocationType: v1beta1.AllocationType{
						Type:            v1beta1.IPAllocTypeFixed,
						ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
						ReleaseAfter:    "6m",
					},
					Selector:         v1beta1.Selector{},
					SecurityGroupIDs: []string{"sg-1"},
					VSwitchOptions:   []string{"vsw-1", "vsw-2", "vsw-3"},
					VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
						VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
					}},
				Status: v1beta1.PodNetworkingStatus{
					Status: v1beta1.NetworkingStatusReady,
					VSwitches: []v1beta1.VSwitch{
						{
							ID:   "vsw-1",
							Zone: "zone-1",
						},
						{
							ID:   "vsw-2",
							Zone: "zone-2",
						},
						{
							ID:   "vsw-3",
							Zone: "zone-2",
						},
					},
					UpdateAt: metav1.Time{},
					Message:  "",
				},
			}

			update = secondaryPn.DeepCopy()
			_, err = controllerutil.CreateOrPatch(ctx, c, update, func() error {
				update.Annotations = secondaryPn.Annotations
				update.Labels = secondaryPn.Labels
				update.Spec = secondaryPn.Spec
				update.Status = secondaryPn.Status

				return nil
			})

			Expect(err).NotTo(HaveOccurred())

			_, err = controllerutil.CreateOrPatch(ctx, c, update, func() error {
				update.Annotations = secondaryPn.Annotations
				update.Labels = secondaryPn.Labels
				update.Spec = secondaryPn.Spec
				update.Status = secondaryPn.Status

				return nil
			})

			Expect(err).NotTo(HaveOccurred())

			podNetworks := `
 [
        {"interfaceName":"eth0","network":"default","defaultRoute": false },
        {"interfaceName":"eth1","network":"secondary","defaultRoute": true }
      ]`

			obj = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.aliyun.com/pod-networks-request": podNetworks,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "busybox",
						},
					},
				},
			}

			err = c.Create(context.TODO(), obj)
			Expect(err).NotTo(HaveOccurred())

			err = c.Get(context.TODO(), client.ObjectKeyFromObject(obj), obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Annotations["k8s.aliyun.com/pod-eni"]).To(Equal("true"))

			Expect(obj.Spec.Containers[0].Resources.Limits["aliyun/member-eni"].Equal(resource.MustParse(strconv.Itoa(2)))).To(BeTrue())

			networks, err := controlplane.ParsePodNetworksFromAnnotation(obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(networks.PodNetworks[0]).To(Equal(controlplane.PodNetworks{
				VSwitchOptions:   []string{"vsw-1", "vsw-2", "vsw-3"},
				SecurityGroupIDs: []string{"sg-1"},
				Interface:        "eth0",
				ExtraRoutes:      nil,
				ENIOptions: v1beta1.ENIOptions{
					ENIAttachType: v1beta1.ENIOptionTypeDefault,
				},
				VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
					VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
				},
				ResourceGroupID:             "",
				NetworkInterfaceTrafficMode: "",
				DefaultRoute:                false,
				AllocationType: &v1beta1.AllocationType{
					Type:            v1beta1.IPAllocTypeElastic,
					ReleaseStrategy: "",
					ReleaseAfter:    "",
				},
			}))
			Expect(networks.PodNetworks[1]).To(Equal(controlplane.PodNetworks{
				VSwitchOptions:   []string{"vsw-1", "vsw-2", "vsw-3"},
				SecurityGroupIDs: []string{"sg-1"},
				Interface:        "eth1",
				ExtraRoutes:      nil,
				ENIOptions: v1beta1.ENIOptions{
					ENIAttachType: v1beta1.ENIOptionTypeDefault,
				},
				VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
					VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
				},
				ResourceGroupID:             "",
				NetworkInterfaceTrafficMode: "",
				DefaultRoute:                true,
				AllocationType: &v1beta1.AllocationType{
					Type:            v1beta1.IPAllocTypeFixed,
					ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
					ReleaseAfter:    "6m",
				},
			}))

			_ = c.Delete(context.TODO(), obj)

			cancel()
		})

		It("test pod create exclusive eni", func() {

			server.Register("/mutating", MutatingHook(c, &controlplane.Config{
				EnableWebhookInjectResource: ptr.To(true),
				EnableTrunk:                 ptr.To(false),
				IPAMType:                    "crd",
			}))
			server.Register("/validate", ValidateHook())

			go func() {
				err := server.Start(ctx)
				Expect(err).NotTo(HaveOccurred())
			}()

			By("use pod-networks annotation")
			podNetworks := `
{
  "podNetworks": [
    {
      "vSwitchOptions": [
        "vsw-1",
        "vsw-2",
        "vsw-3"
      ],
      "interface": "eth0",
      "securityGroupIDs": [
        "sg-1"
      ]
    },
    {
      "vSwitchOptions": [
        "vsw-1",
        "vsw-2",
        "vsw-3"
      ],
      "interface": "eth1",
      "securityGroupIDs": [
        "sg-2"
      ],
      "defaultRoute": true
    }
  ]
}`

			obj = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"k8s.aliyun.com/pod-networks": podNetworks,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "busybox",
						},
					},
				},
			}

			err := c.Create(context.TODO(), obj)
			Expect(err).NotTo(HaveOccurred())

			err = c.Get(context.TODO(), client.ObjectKeyFromObject(obj), obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Annotations["k8s.aliyun.com/pod-eni"]).To(Equal("true"))

			Expect(obj.Spec.Containers[0].Resources.Limits["aliyun/eni"].Equal(resource.MustParse(strconv.Itoa(2)))).To(BeTrue())

			_ = c.Delete(context.TODO(), obj)

			cancel()
		})

	})

	It("test podNetworking create", func() {
		server.Register("/mutating", MutatingHook(c, &controlplane.Config{
			EnableWebhookInjectResource: ptr.To(true),
			EnableTrunk:                 ptr.To(true),
		}))
		server.Register("/validate", ValidateHook())
		ctrl.SetLogger(GinkgoLogr)
		go func() {
			err := server.Start(ctx)
			Expect(err).NotTo(HaveOccurred())
		}()

		By("create a valid PodNetworking")
		podNetworking := &v1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-podnetworking",
				Namespace: "default",
			},
			Spec: v1beta1.PodNetworkingSpec{
				ENIOptions: v1beta1.ENIOptions{
					ENIAttachType: v1beta1.ENIOptionTypeENI,
				},
				Selector: v1beta1.Selector{
					PodSelector: &metav1.LabelSelector{},
				},
				AllocationType: v1beta1.AllocationType{
					ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
					ReleaseAfter:    "5m",
				},
				SecurityGroupIDs: []string{"sg-1"},
				VSwitchOptions:   []string{"vsw-1", "vsw-2"},
			},
		}

		err := c.Create(context.TODO(), podNetworking)
		Expect(err).NotTo(HaveOccurred())

		By("create a empty selector PodNetworking")
		emptySelectorPodNetworking := &v1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: "empty-selector-podnetworking",
			},
			Spec: v1beta1.PodNetworkingSpec{
				ENIOptions: v1beta1.ENIOptions{
					ENIAttachType: v1beta1.ENIOptionTypeDefault,
				},
				AllocationType: v1beta1.AllocationType{
					ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
					ReleaseAfter:    "5m",
				},
				SecurityGroupIDs: []string{"sg-1"},
				VSwitchOptions:   []string{"vsw-1", "vsw-2"},
			},
		}

		err = c.Create(context.TODO(), emptySelectorPodNetworking)
		Expect(err).NotTo(HaveOccurred())

		By("create an invalid PodNetworking with too many security groups")
		invalidPodNetworking := &v1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: "invalid-podnetworking",
			},
			Spec: v1beta1.PodNetworkingSpec{
				ENIOptions: v1beta1.ENIOptions{
					ENIAttachType: v1beta1.ENIOptionTypeDefault,
				},
				AllocationType: v1beta1.AllocationType{
					ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
					ReleaseAfter:    "5m",
				},
				SecurityGroupIDs: []string{"sg-1", "sg-2", "sg-3", "sg-4", "sg-5", "sg-6", "sg-7", "sg-8", "sg-9", "sg-10", "sg-11"},
				VSwitchOptions:   []string{"vsw-1", "vsw-2"},
			},
		}

		err = c.Create(context.TODO(), invalidPodNetworking)
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), "security group can not more than 10")).To(BeTrue())

		By("networking used as netplan")
		netplanPodNetworking := &v1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: "netplan-podnetworking",
			},
			Spec: v1beta1.PodNetworkingSpec{
				ENIOptions: v1beta1.ENIOptions{
					ENIAttachType: v1beta1.ENIOptionTypeENI,
				},
				AllocationType: v1beta1.AllocationType{
					Type:            v1beta1.IPAllocTypeFixed,
					ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
					ReleaseAfter:    "10m",
				},
				VSwitchOptions:   []string{"vsw-1", "vsw-2"},
				SecurityGroupIDs: []string{"sg-1"},
			},
		}
		err = c.Create(context.TODO(), netplanPodNetworking)
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), "attachType must be default")).To(BeTrue())

		cancel()
	})

})
