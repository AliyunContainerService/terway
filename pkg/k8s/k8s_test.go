package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func TestGetNodeReturnsNodeWhenExists(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
	}).Build()
	node, err := getNode(context.Background(), client, "node1")
	assert.NoError(t, err)
	assert.NotNil(t, node)
	assert.Equal(t, "node1", node.Name)
}

func TestGetPodReturnsPodWhenExists(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
	}).Build()
	pod, err := getPod(context.Background(), client, "default", "pod1", true)
	assert.NoError(t, err)
	assert.NotNil(t, pod)
	assert.Equal(t, "pod1", pod.Name)
}

func TestGetCMReturnsConfigMapWhenExists(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm1", Namespace: "default"},
	}).Build()
	cm, err := getCM(context.Background(), client, "default", "cm1")
	assert.NoError(t, err)
	assert.NotNil(t, cm)
	assert.Equal(t, "cm1", cm.Name)
}

func TestConvertPodReturnsPodInfoWithCorrectNetworkType(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
		Status: corev1.PodStatus{
			PodIP: "192.168.1.1",
			PodIPs: []corev1.PodIP{
				{IP: "192.168.1.1"},
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string](), pod)
	assert.Equal(t, daemon.PodNetworkTypeENIMultiIP, result.PodNetworkType)
}

func TestConvertPodSetsIngressAndEgressBandwidth(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			Annotations: map[string]string{
				podIngressBandwidth: "1M",
				podEgressBandwidth:  "2M",
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string](), pod)
	assert.Equal(t, uint64(1*MEGABYTE), result.TcIngress)
	assert.Equal(t, uint64(2*MEGABYTE), result.TcEgress)
}

func TestConvertPodHandlesInvalidBandwidthAnnotations(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			Annotations: map[string]string{
				podIngressBandwidth: "invalid",
				podEgressBandwidth:  "invalid",
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string](), pod)
	assert.Equal(t, uint64(0), result.TcIngress)
	assert.Equal(t, uint64(0), result.TcEgress)
}

func TestConvertPodSetsPodENI(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			Annotations: map[string]string{
				types.PodENI: "true",
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string](), pod)
	assert.True(t, result.PodENI)
}

func TestConvertPodHandlesInvalidPodENIAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			Annotations: map[string]string{
				types.PodENI: "invalid",
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string](), pod)
	assert.False(t, result.PodENI)
}

func TestConvertPodSetsNetworkPriority(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			Annotations: map[string]string{
				types.NetworkPriority: string(types.NetworkPrioGuaranteed),
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string](), pod)
	assert.Equal(t, string(types.NetworkPrioGuaranteed), result.NetworkPriority)
}

func TestConvertPodHandlesInvalidNetworkPriorityAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			Annotations: map[string]string{
				types.NetworkPriority: "invalid",
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string](), pod)
	assert.Empty(t, result.NetworkPriority)
}

func TestConvertPodSetsERdmaWhenEnabled(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							deviceplugin.ERDMAResName: resource.MustParse("1"),
						},
					},
				},
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, true, sets.New[string](), pod)
	assert.True(t, result.ERdma)
}

func TestConvertPodSetsIPStickTimeForStatefulWorkload(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			Annotations: map[string]string{
				types.PodIPReservation: "true",
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string](), pod)
	assert.Equal(t, defaultStickTimeForSts, result.IPStickTime)
}

func TestServiceCidrFromAPIServerReturnsErrorWhenConfigMapNotFound(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	_, err := serviceCidrFromAPIServer(client)
	assert.Error(t, err)
}

func TestServiceCidrFromAPIServerReturnsErrorWhenConfigMapDataMissing(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: k8sKubeadmConfigmap, Namespace: k8sSystemNamespace},
	}).Build()
	_, err := serviceCidrFromAPIServer(client)
	assert.Error(t, err)
}

func TestServiceCidrFromAPIServerReturnsErrorWhenUnmarshalFails(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: k8sKubeadmConfigmap, Namespace: k8sSystemNamespace},
		Data:       map[string]string{k8sKubeadmConfigmapNetworking: "invalid-yaml"},
	}).Build()
	_, err := serviceCidrFromAPIServer(client)
	assert.Error(t, err)
}

func TestServiceCidrFromAPIServerReturnsErrorWhenServiceSubnetNotFound(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: k8sKubeadmConfigmap, Namespace: k8sSystemNamespace},
		Data:       map[string]string{k8sKubeadmConfigmapNetworking: "networking: {}"},
	}).Build()
	_, err := serviceCidrFromAPIServer(client)
	assert.Error(t, err)
}

func TestServiceCidrFromAPIServerReturnsParsedCidr(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: k8sKubeadmConfigmap, Namespace: k8sSystemNamespace},
		Data:       map[string]string{k8sKubeadmConfigmapNetworking: "networking:\n  serviceSubnet: 10.96.0.0/12"},
	}).Build()
	cidr, err := serviceCidrFromAPIServer(client)
	assert.NoError(t, err)
	assert.NotNil(t, cidr)
	assert.Equal(t, "10.96.0.0/12", cidr.String())
}
