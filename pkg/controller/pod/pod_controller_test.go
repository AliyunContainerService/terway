package pod

import (
	"context"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aliyun "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/controller/mocks"
	"github.com/AliyunContainerService/terway/pkg/generated/clientset/versioned/scheme"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

var _ = Describe("Pod controller", func() {
	nodeName := "node"

	AfterEach(func() {
		ctx := context.Background()
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		_ = k8sClient.Delete(ctx, node)
	})

	Context("create normal pod use pod-networks anno", func() {
		ctx := context.Background()
		name := "normal-pod-pod-networks"
		ns := "default"
		eniID := "eni-0"
		key := k8stypes.NamespacedName{Name: name, Namespace: ns}
		request := reconcile.Request{
			NamespacedName: key,
		}

		vsw, _ := vswitch.NewSwitchPool(100, "10m")

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Annotations: map[string]string{
					types.PodENI:      "true",
					types.PodNetworks: "{\"podNetworks\":[{\"vSwitchOptions\":[\"vsw-0\",\"vsw-1\",\"vsw-2\"],\"securityGroupIDs\":[\"sg-1\"],\"interface\":\"eth0\",\"eniOptions\":{\"eniType\":\"Default\"},\"vSwitchSelectOptions\":{\"vSwitchSelectionPolicy\":\"ordered\"},\"resourceGroupID\":\"\",\"networkInterfaceTrafficMode\":\"\",\"defaultRoute\":false,\"allocationType\":{\"type\":\"Elastic\",\"releaseStrategy\":\"\",\"releaseAfter\":\"\"}}]}",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName,
				Containers: []corev1.Container{
					{
						Name:  "foo",
						Image: "busybox",
					},
				},
			},
		}

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					"topology.kubernetes.io/region":    "cn-hangzhou",
					"topology.kubernetes.io/zone":      "cn-hangzhou-a",
					"node.kubernetes.io/instance-type": "instanceType",
				},
			},
			Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-xxx"},
		}

		podENI := &networkv1beta1.PodENI{}

		It("Create podENI should succeed", func() {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
			openAPI = mocks.NewInterface(GinkgoT())
			openAPI.On("CreateNetworkInterfaceV2", mock.Anything, mock.Anything).Return(&aliyun.NetworkInterface{
				Status:             "Available",
				MacAddress:         "mac",
				NetworkInterfaceID: eniID,
				VPCID:              "vpc-0",
				VSwitchID:          "vsw-0",
				PrivateIPAddress:   "127.0.0.1",
				PrivateIPSets:      nil,
				ZoneID:             "cn-hangzhou-a",
				SecurityGroupIDs: []string{
					"sg-0",
				},
				ResourceGroupID:             "rg-0",
				IPv6Set:                     nil,
				Tags:                        nil,
				Type:                        "Secondary",
				InstanceID:                  "",
				TrunkNetworkInterfaceID:     "",
				NetworkInterfaceTrafficMode: "Standard",
				DeviceIndex:                 0,
				CreationTime:                "",
			}, nil).Once()

			openAPI.On("DescribeVSwitchByID", mock.Anything, mock.Anything).Return(&vpc.VSwitch{
				VpcId:                   "vpc-0",
				Status:                  "",
				AvailableIpAddressCount: 10,
				VSwitchId:               "vsw-0",
				CidrBlock:               "127.0.0.0/24",
				ZoneId:                  "cn-hangzhou-a",
				Ipv6CidrBlock:           "fd00::0/120",
				EnabledIpv6:             true,
			}, nil).Once()

			controlplane.SetConfig(&controlplane.Config{})

			r := &ReconcilePod{
				client:    k8sClient,
				scheme:    scheme.Scheme,
				aliyun:    openAPI,
				swPool:    vsw,
				record:    record.NewFakeRecorder(1000),
				trunkMode: false,
				crdMode:   false,
			}

			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, key, podENI)
			Expect(err).NotTo(HaveOccurred())

		})

		It("networkinterface should be created", func() {
			eni := &networkv1beta1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name:      eniID,
					Namespace: podENI.Namespace,
				},
			}
			Expect(k8sClient.Get(ctx, k8stypes.NamespacedName{Name: eni.Name, Namespace: eni.Namespace}, eni)).Should(Succeed())

			Expect(eni.Spec.PodENIRef.Name).To(Equal(pod.Name))
			Expect(eni.Spec.PodENIRef.Namespace).To(Equal(pod.Namespace))
		})

		It("PodENI should be created", func() {
			Expect(podENI.Annotations[types.PodUID]).Should(BeEquivalentTo(pod.UID))

			Expect(len(podENI.Spec.Allocations)).To(Equal(1))

			Expect(podENI.Spec.Allocations[0].AllocationType).To(Equal(networkv1beta1.AllocationType{
				Type:            networkv1beta1.IPAllocTypeElastic,
				ReleaseStrategy: "",
				ReleaseAfter:    "",
			}))

			Expect(podENI.Spec.Allocations[0].ENI).To(Equal(networkv1beta1.ENI{
				ID:               eniID,
				MAC:              "mac",
				Zone:             "cn-hangzhou-a",
				VPCID:            "vpc-0",
				VSwitchID:        "vsw-0",
				ResourceGroupID:  "rg-0",
				SecurityGroupIDs: []string{"sg-0"},
				AttachmentOptions: networkv1beta1.AttachmentOptions{
					Trunk: nil,
				},
			}))

			Expect(podENI.Spec.Allocations[0].IPv4).To(Equal("127.0.0.1"))
		})
	})

	Context("create fixed ip pod use pod-networks anno", func() {
		ctx := context.Background()
		name := "fixed-ip-pod-pod-networks"
		ns := "default"
		key := k8stypes.NamespacedName{Name: name, Namespace: ns}
		eniID := "eni-1"

		request := reconcile.Request{
			NamespacedName: key,
		}

		vsw, _ := vswitch.NewSwitchPool(100, "10m")

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Annotations: map[string]string{
					types.PodENI:      "true",
					types.PodNetworks: "{\"podNetworks\":[{\"vSwitchOptions\":[\"vsw-0\",\"vsw-1\",\"vsw-2\"],\"securityGroupIDs\":[\"sg-1\"],\"interface\":\"eth0\",\"eniOptions\":{\"eniType\":\"Default\"},\"vSwitchSelectOptions\":{\"vSwitchSelectionPolicy\":\"ordered\"},\"resourceGroupID\":\"\",\"networkInterfaceTrafficMode\":\"\",\"defaultRoute\":false,\"allocationType\":{\"type\":\"Fixed\",\"releaseStrategy\":\"TTL\",\"releaseAfter\":\"20m\"}}]}",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName,
				Containers: []corev1.Container{
					{
						Name:  "foo",
						Image: "busybox",
					},
				},
			},
		}

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					"topology.kubernetes.io/region":    "cn-hangzhou",
					"topology.kubernetes.io/zone":      "cn-hangzhou-a",
					"node.kubernetes.io/instance-type": "instanceType",
				},
			},
			Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-xxx"},
		}

		podENI := &networkv1beta1.PodENI{}

		It("Create podENI should succeed", func() {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
			openAPI = mocks.NewInterface(GinkgoT())
			openAPI.On("CreateNetworkInterfaceV2", mock.Anything, mock.Anything).Return(&aliyun.NetworkInterface{
				Status:             "Available",
				MacAddress:         "mac",
				NetworkInterfaceID: eniID,
				VPCID:              "vpc-0",
				VSwitchID:          "vsw-0",
				PrivateIPAddress:   "127.0.0.1",
				PrivateIPSets:      nil,
				ZoneID:             "cn-hangzhou-a",
				SecurityGroupIDs: []string{
					"sg-0",
				},
				ResourceGroupID:             "rg-0",
				IPv6Set:                     nil,
				Tags:                        nil,
				Type:                        "Secondary",
				InstanceID:                  "",
				TrunkNetworkInterfaceID:     "",
				NetworkInterfaceTrafficMode: "Standard",
				DeviceIndex:                 0,
				CreationTime:                "",
			}, nil).Once()

			openAPI.On("DescribeVSwitchByID", mock.Anything, mock.Anything).Return(&vpc.VSwitch{
				VpcId:                   "vpc-0",
				Status:                  "",
				AvailableIpAddressCount: 10,
				VSwitchId:               "vsw-0",
				CidrBlock:               "127.0.0.0/24",
				ZoneId:                  "cn-hangzhou-a",
				Ipv6CidrBlock:           "fd00::0/120",
				EnabledIpv6:             true,
			}, nil).Once()

			controlplane.SetConfig(&controlplane.Config{})

			r := &ReconcilePod{
				client:    k8sClient,
				scheme:    scheme.Scheme,
				aliyun:    openAPI,
				swPool:    vsw,
				record:    record.NewFakeRecorder(1000),
				trunkMode: false,
				crdMode:   false,
			}

			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, key, podENI)
			Expect(err).NotTo(HaveOccurred())

		})

		It("PodENI should be created", func() {
			Expect(podENI.Annotations[types.PodUID]).Should(BeEquivalentTo(pod.UID))

			Expect(len(podENI.Spec.Allocations)).To(Equal(1))

			Expect(podENI.Spec.Allocations[0].AllocationType).To(Equal(networkv1beta1.AllocationType{
				Type:            networkv1beta1.IPAllocTypeFixed,
				ReleaseStrategy: "TTL",
				ReleaseAfter:    "20m",
			}))

			Expect(podENI.Spec.Allocations[0].ENI).To(Equal(networkv1beta1.ENI{
				ID:               eniID,
				VPCID:            "vpc-0",
				MAC:              "mac",
				Zone:             "cn-hangzhou-a",
				VSwitchID:        "vsw-0",
				ResourceGroupID:  "rg-0",
				SecurityGroupIDs: []string{"sg-0"},
				AttachmentOptions: networkv1beta1.AttachmentOptions{
					Trunk: nil,
				},
			}))

			Expect(podENI.Spec.Allocations[0].IPv4).To(Equal("127.0.0.1"))
		})
	})

	Context("create fixed ip pod use legacy pn anno", func() {
		ctx := context.Background()
		name := "fixed-ip-pod-legacy-pn"
		ns := "default"
		eniID := "eni-2"
		key := k8stypes.NamespacedName{Name: name, Namespace: ns}
		request := reconcile.Request{
			NamespacedName: key,
		}

		vsw, _ := vswitch.NewSwitchPool(100, "10m")

		pn := &networkv1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: "foo",
			},
			Spec: networkv1beta1.PodNetworkingSpec{
				ENIOptions: networkv1beta1.ENIOptions{
					ENIAttachType: networkv1beta1.ENIOptionTypeDefault,
				},
				AllocationType: networkv1beta1.AllocationType{
					Type:            networkv1beta1.IPAllocTypeFixed,
					ReleaseStrategy: networkv1beta1.ReleaseStrategyNever,
				},
				Selector:         networkv1beta1.Selector{},
				SecurityGroupIDs: []string{"sg-0"},
				VSwitchOptions: []string{
					"vsw-0",
				},
				VSwitchSelectOptions: networkv1beta1.VSwitchSelectOptions{},
			},
			Status: networkv1beta1.PodNetworkingStatus{},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Annotations: map[string]string{
					types.PodENI:        "true",
					types.PodNetworking: "foo",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName,
				Containers: []corev1.Container{
					{
						Name:  "foo",
						Image: "busybox",
					},
				},
			},
		}

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					"topology.kubernetes.io/region":    "cn-hangzhou",
					"topology.kubernetes.io/zone":      "cn-hangzhou-a",
					"node.kubernetes.io/instance-type": "instanceType",
				},
			},
			Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-xxx"},
		}

		podENI := &networkv1beta1.PodENI{}

		It("Create podENI should succeed", func() {
			Expect(k8sClient.Create(ctx, pn)).Should(Succeed())
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
			openAPI = mocks.NewInterface(GinkgoT())
			openAPI.On("CreateNetworkInterfaceV2", mock.Anything, mock.Anything).Return(&aliyun.NetworkInterface{
				Status:             "Available",
				MacAddress:         "mac",
				NetworkInterfaceID: eniID,
				VPCID:              "vpc-0",
				VSwitchID:          "vsw-0",
				PrivateIPAddress:   "127.0.0.1",
				PrivateIPSets:      nil,
				ZoneID:             "cn-hangzhou-a",
				SecurityGroupIDs: []string{
					"sg-0",
				},
				ResourceGroupID:             "rg-0",
				IPv6Set:                     nil,
				Tags:                        nil,
				Type:                        "Secondary",
				InstanceID:                  "",
				TrunkNetworkInterfaceID:     "",
				NetworkInterfaceTrafficMode: "Standard",
				DeviceIndex:                 0,
				CreationTime:                "",
			}, nil).Once()

			openAPI.On("DescribeVSwitchByID", mock.Anything, mock.Anything).Return(&vpc.VSwitch{
				VpcId:                   "vpc-0",
				Status:                  "",
				AvailableIpAddressCount: 10,
				VSwitchId:               "vsw-0",
				CidrBlock:               "127.0.0.0/24",
				ZoneId:                  "cn-hangzhou-a",
				Ipv6CidrBlock:           "fd00::0/120",
				EnabledIpv6:             true,
			}, nil).Once()

			controlplane.SetConfig(&controlplane.Config{})

			r := &ReconcilePod{
				client:    k8sClient,
				scheme:    scheme.Scheme,
				aliyun:    openAPI,
				swPool:    vsw,
				record:    record.NewFakeRecorder(1000),
				trunkMode: false,
				crdMode:   false,
			}

			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, key, podENI)
			Expect(err).NotTo(HaveOccurred())

		})

		It("PodENI should be created", func() {
			Expect(podENI.Annotations[types.PodUID]).Should(BeEquivalentTo(pod.UID))

			Expect(len(podENI.Spec.Allocations)).To(Equal(1))

			Expect(podENI.Spec.Allocations[0].AllocationType).To(Equal(networkv1beta1.AllocationType{
				Type:            networkv1beta1.IPAllocTypeFixed,
				ReleaseStrategy: "Never",
				ReleaseAfter:    "",
			}))

			Expect(podENI.Spec.Allocations[0].ENI).To(Equal(networkv1beta1.ENI{
				ID:               eniID,
				VPCID:            "vpc-0",
				MAC:              "mac",
				Zone:             "cn-hangzhou-a",
				VSwitchID:        "vsw-0",
				ResourceGroupID:  "rg-0",
				SecurityGroupIDs: []string{"sg-0"},
				AttachmentOptions: networkv1beta1.AttachmentOptions{
					Trunk: nil,
				},
			}))

			Expect(podENI.Spec.Allocations[0].IPv4).To(Equal("127.0.0.1"))
		})
	})

	Context("create fixed ip pod (exclusive eni node)", func() {
		ctx := context.Background()
		name := "fixed-ip-pod-exclusive-eni"
		ns := "default"
		eniID := "eni-3"
		key := k8stypes.NamespacedName{Name: name, Namespace: ns}
		request := reconcile.Request{
			NamespacedName: key,
		}

		vsw, _ := vswitch.NewSwitchPool(100, "10m")

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        key.Name,
				Namespace:   key.Namespace,
				Annotations: map[string]string{},
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName,
				Containers: []corev1.Container{
					{
						Name:  "foo",
						Image: "busybox",
					},
				},
			},
		}

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					"topology.kubernetes.io/region":    "cn-hangzhou",
					"topology.kubernetes.io/zone":      "cn-hangzhou-a",
					"node.kubernetes.io/instance-type": "instanceType",
					types.ExclusiveENIModeLabel:        string(types.ExclusiveENIOnly),
				},
			},
			Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-xxx"},
		}

		podENI := &networkv1beta1.PodENI{}

		eniConfig := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "enable_eni_trunking": true,
      "ipam_type": "crd",
      "vswitches": {"cn-hangzhou-a":["vsw-0"],"cn-hangzhou-b":["vsw-2"]},
      "eni_tags": {"ack.aliyun.com":"c"},
      "service_cidr": "192.168.0.0/16,fc00::/112",
      "security_group": "sg-0",
      "ip_stack": "dual",
      "resource_group_id": "rg-0",
      "vswitch_selection_policy": "ordered"
    }`
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "eni-config",
				Namespace: "kube-system",
			},
			Data: map[string]string{
				"eni_conf": eniConfig,
			},
		}

		It("Create podENI should succeed", func() {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			Expect(k8sClient.Create(ctx, cm)).Should(Succeed())
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			openAPI = mocks.NewInterface(GinkgoT())
			openAPI.On("CreateNetworkInterfaceV2", mock.Anything, mock.Anything).Return(&aliyun.NetworkInterface{
				Status:             "Available",
				MacAddress:         "mac",
				NetworkInterfaceID: eniID,
				VPCID:              "vpc-0",
				VSwitchID:          "vsw-0",
				PrivateIPAddress:   "127.0.0.1",
				PrivateIPSets:      nil,
				ZoneID:             "cn-hangzhou-a",
				SecurityGroupIDs: []string{
					"sg-0",
				},
				ResourceGroupID:             "rg-0",
				IPv6Set:                     nil,
				Tags:                        nil,
				Type:                        "Secondary",
				InstanceID:                  "",
				TrunkNetworkInterfaceID:     "",
				NetworkInterfaceTrafficMode: "Standard",
				DeviceIndex:                 0,
				CreationTime:                "",
			}, nil).Once()

			openAPI.On("DescribeVSwitchByID", mock.Anything, mock.Anything).Return(&vpc.VSwitch{
				VpcId:                   "vpc-0",
				Status:                  "",
				AvailableIpAddressCount: 10,
				VSwitchId:               "vsw-0",
				CidrBlock:               "127.0.0.0/24",
				ZoneId:                  "cn-hangzhou-a",
				Ipv6CidrBlock:           "fd00::0/120",
				EnabledIpv6:             true,
			}, nil).Once()

			controlplane.SetConfig(&controlplane.Config{})

			r := &ReconcilePod{
				client:    k8sClient,
				scheme:    scheme.Scheme,
				aliyun:    openAPI,
				swPool:    vsw,
				record:    record.NewFakeRecorder(1000),
				trunkMode: false,
				crdMode:   false,
			}

			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, key, podENI)
			Expect(err).NotTo(HaveOccurred())

		})

		It("PodENI should be created", func() {
			Expect(podENI.Annotations[types.PodUID]).Should(BeEquivalentTo(pod.UID))

			Expect(len(podENI.Spec.Allocations)).To(Equal(1))

			Expect(podENI.Spec.Allocations[0].AllocationType).To(Equal(networkv1beta1.AllocationType{
				Type:            networkv1beta1.IPAllocTypeElastic,
				ReleaseStrategy: "",
				ReleaseAfter:    "",
			}))

			Expect(podENI.Spec.Allocations[0].ENI).To(Equal(networkv1beta1.ENI{
				ID:               eniID,
				VPCID:            "vpc-0",
				MAC:              "mac",
				Zone:             "cn-hangzhou-a",
				VSwitchID:        "vsw-0",
				ResourceGroupID:  "rg-0",
				SecurityGroupIDs: []string{"sg-0"},
				AttachmentOptions: networkv1beta1.AttachmentOptions{
					Trunk: nil,
				},
			}))

			Expect(podENI.Spec.Allocations[0].IPv4).To(Equal("127.0.0.1"))
		})
	})

	Context("create fixed ip pod (has prev eni)", func() {
		ctx := context.Background()
		name := "fixed-ip-pod-prev-eni"
		ns := "default"
		eniID := "eni-4"
		key := k8stypes.NamespacedName{Name: name, Namespace: ns}
		request := reconcile.Request{
			NamespacedName: key,
		}

		vsw, _ := vswitch.NewSwitchPool(100, "10m")

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Annotations: map[string]string{
					types.PodENI:      "true",
					types.PodNetworks: "{\"podNetworks\":[{\"vSwitchOptions\":[\"vsw-0\",\"vsw-1\",\"vsw-2\"],\"securityGroupIDs\":[\"sg-1\"],\"interface\":\"eth0\",\"eniOptions\":{\"eniType\":\"Default\"},\"vSwitchSelectOptions\":{\"vSwitchSelectionPolicy\":\"ordered\"},\"resourceGroupID\":\"\",\"networkInterfaceTrafficMode\":\"\",\"defaultRoute\":false,\"allocationType\":{\"type\":\"Fixed\",\"releaseStrategy\":\"TTL\",\"releaseAfter\":\"20m\"}}]}",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName,
				Containers: []corev1.Container{
					{
						Name:  "foo",
						Image: "busybox",
					},
				},
			},
		}

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					"topology.kubernetes.io/region":    "cn-hangzhou",
					"topology.kubernetes.io/zone":      "cn-hangzhou-a",
					"node.kubernetes.io/instance-type": "instanceType",
				},
			},
			Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-xxx"},
		}

		podENI := &networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Labels: map[string]string{
					types.ENIRelatedNodeName: "pev-node",
				},
			},
			Spec: networkv1beta1.PodENISpec{
				Allocations: []networkv1beta1.Allocation{
					{
						AllocationType: networkv1beta1.AllocationType{
							Type:            networkv1beta1.IPAllocTypeFixed,
							ReleaseStrategy: networkv1beta1.ReleaseStrategyNever,
						},
						ENI: networkv1beta1.ENI{
							ID:               eniID,
							MAC:              "mac",
							Zone:             "zone",
							VSwitchID:        "vsw-0",
							ResourceGroupID:  "rg-0",
							SecurityGroupIDs: []string{"sg-0"},
							AttachmentOptions: networkv1beta1.AttachmentOptions{
								Trunk: nil,
							},
						},
						IPv4:         "",
						IPv6:         "",
						IPv4CIDR:     "",
						IPv6CIDR:     "",
						Interface:    "eth0",
						DefaultRoute: true,
						ExtraRoutes:  nil,
						ExtraConfig:  nil,
					},
				},
			},
			Status: networkv1beta1.PodENIStatus{
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					eniID: {
						ID:               eniID,
						Type:             "",
						Vid:              0,
						Status:           networkv1beta1.ENIStatusUnBind,
						NetworkCardIndex: nil,
					},
				},
				Phase: networkv1beta1.ENIPhaseUnbind,
			},
		}

		It("Create podENI should succeed", func() {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			podENICopy := podENI.DeepCopy()
			Expect(k8sClient.Create(ctx, podENI)).Should(Succeed())

			_, err := controllerutil.CreateOrPatch(ctx, k8sClient, podENI, func() error {
				podENI.Status = podENICopy.Status
				return nil
			})
			Expect(err).Should(Not(HaveOccurred()))

			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			openAPI = mocks.NewInterface(GinkgoT())

			controlplane.SetConfig(&controlplane.Config{})

			r := &ReconcilePod{
				client:    k8sClient,
				scheme:    scheme.Scheme,
				aliyun:    openAPI,
				swPool:    vsw,
				record:    record.NewFakeRecorder(1000),
				trunkMode: false,
				crdMode:   false,
			}

			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, key, podENI)
			Expect(err).NotTo(HaveOccurred())

			Expect(podENI.Labels[types.ENIRelatedNodeName]).Should(BeEquivalentTo(nodeName))
		})
	})

	Context("create failed should rollback", func() {
		ctx := context.Background()
		name := "create-eni-failed"
		ns := "default"
		eniID := "eni-5" // exist cr`
		key := k8stypes.NamespacedName{Name: name, Namespace: ns}
		request := reconcile.Request{
			NamespacedName: key,
		}

		vsw, _ := vswitch.NewSwitchPool(100, "10m")

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Annotations: map[string]string{
					types.PodENI:      "true",
					types.PodNetworks: "{\"podNetworks\":[{\"vSwitchOptions\":[\"vsw-0\",\"vsw-1\",\"vsw-2\"],\"securityGroupIDs\":[\"sg-1\"],\"interface\":\"eth0\",\"eniOptions\":{\"eniType\":\"Default\"},\"vSwitchSelectOptions\":{\"vSwitchSelectionPolicy\":\"ordered\"},\"resourceGroupID\":\"\",\"networkInterfaceTrafficMode\":\"\",\"defaultRoute\":false,\"allocationType\":{\"type\":\"Elastic\",\"releaseStrategy\":\"\",\"releaseAfter\":\"\"}}]}",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName,
				Containers: []corev1.Container{
					{
						Name:  "foo",
						Image: "busybox",
					},
				},
			},
		}

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					"topology.kubernetes.io/region":    "cn-hangzhou",
					"topology.kubernetes.io/zone":      "cn-hangzhou-a",
					"node.kubernetes.io/instance-type": "instanceType",
				},
			},
			Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-xxx"},
		}

		podENI := &networkv1beta1.PodENI{}

		eni := &networkv1beta1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name: eniID,
			},
		}

		It("Create podENI should succeed", func() {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
			Expect(k8sClient.Create(ctx, eni)).Should(Succeed())

			openAPI = mocks.NewInterface(GinkgoT())
			openAPI.On("CreateNetworkInterfaceV2", mock.Anything, mock.Anything).Return(&aliyun.NetworkInterface{
				Status:             "Available",
				MacAddress:         "mac",
				NetworkInterfaceID: eniID,
				VPCID:              "vpc-0",
				VSwitchID:          "vsw-0",
				PrivateIPAddress:   "127.0.0.1",
				PrivateIPSets:      nil,
				ZoneID:             "cn-hangzhou-a",
				SecurityGroupIDs: []string{
					"sg-0",
				},
				ResourceGroupID:             "rg-0",
				IPv6Set:                     nil,
				Tags:                        nil,
				Type:                        "Secondary",
				InstanceID:                  "",
				TrunkNetworkInterfaceID:     "",
				NetworkInterfaceTrafficMode: "Standard",
				DeviceIndex:                 0,
				CreationTime:                "",
			}, nil).Once()

			openAPI.On("DescribeVSwitchByID", mock.Anything, mock.Anything).Return(&vpc.VSwitch{
				VpcId:                   "vpc-0",
				Status:                  "",
				AvailableIpAddressCount: 10,
				VSwitchId:               "vsw-0",
				CidrBlock:               "127.0.0.0/24",
				ZoneId:                  "cn-hangzhou-a",
				Ipv6CidrBlock:           "fd00::0/120",
				EnabledIpv6:             true,
			}, nil).Once()

			openAPI.On("DeleteNetworkInterfaceV2", mock.Anything, eniID).Return(nil).Once()

			controlplane.SetConfig(&controlplane.Config{})

			r := &ReconcilePod{
				client:    k8sClient,
				scheme:    scheme.Scheme,
				aliyun:    openAPI,
				swPool:    vsw,
				record:    record.NewFakeRecorder(1000),
				trunkMode: false,
				crdMode:   false,
			}

			_, err := r.Reconcile(ctx, request)
			Expect(err).To(HaveOccurred())

			err = k8sClient.Get(ctx, key, podENI)
			Expect(err).To(HaveOccurred())
		})
	})
})
