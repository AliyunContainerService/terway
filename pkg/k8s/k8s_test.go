package k8s

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/terway/types/daemon"
)

func TestK8s_PodExist(t *testing.T) {
	// Setup
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	k8sObj := &k8s{
		client:   fakeClient,
		nodeName: "test-node",
	}

	// Test existing pod on node
	exist, err := k8sObj.PodExist("default", "test-pod")
	assert.NoError(t, err)
	assert.True(t, exist)

	// Test non-existent pod
	exist, err = k8sObj.PodExist("default", "non-existent")
	assert.NoError(t, err)
	assert.False(t, exist)

	// Test pod on different node
	podDifferentNode := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "other-node",
		},
	}

	fakeClient2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(podDifferentNode).Build()
	k8sObj2 := &k8s{
		client:   fakeClient2,
		nodeName: "test-node",
	}

	exist, err = k8sObj2.PodExist("default", "other-pod")
	assert.NoError(t, err)
	assert.False(t, exist)
}

func TestK8s_GetLocalPods_ENIOnlyMode(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-eni-only",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	k8sObj := &k8s{
		client:      fakeClient,
		nodeName:    "test-node",
		mode:        "ENIOnly",
		enableErdma: false,
	}

	pods, err := k8sObj.GetLocalPods()
	assert.NoError(t, err)
	assert.Len(t, pods, 1)
	assert.Equal(t, daemon.PodNetworkTypeVPCENI, pods[0].PodNetworkType)
}

// 新增测试: 测试带宽注解解析
func TestK8s_GetLocalPods_BandwidthAnnotations(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-with-bandwidth",
			Namespace: "default",
			Annotations: map[string]string{
				podIngressBandwidth: "10M",
				podEgressBandwidth:  "20M",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	k8sObj := &k8s{
		client:      fakeClient,
		nodeName:    "test-node",
		mode:        "ENIMultiIP",
		enableErdma: false,
	}

	pods, err := k8sObj.GetLocalPods()
	assert.NoError(t, err)
	assert.Len(t, pods, 1)
	assert.Equal(t, uint64(10*MEGABYTE), pods[0].TcIngress)
	assert.Equal(t, uint64(20*MEGABYTE), pods[0].TcEgress)
}

// 新增测试: 测试PodENI注解
func TestK8s_GetLocalPods_PodENIAnnotation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-with-pod-eni",
			Namespace: "default",
			Annotations: map[string]string{
				types.PodENI: "true",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	k8sObj := &k8s{
		client:      fakeClient,
		nodeName:    "test-node",
		mode:        "ENIMultiIP",
		enableErdma: false,
	}

	pods, err := k8sObj.GetLocalPods()
	assert.NoError(t, err)
	assert.Len(t, pods, 1)
	assert.True(t, pods[0].PodENI)
}

// 新增测试: 测试ERDMA功能
func TestK8s_GetLocalPods_ERDMA(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-with-erdma",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			Containers: []corev1.Container{
				{
					Name: "container",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							deviceplugin.ERDMAResName: resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	k8sObj := &k8s{
		client:      fakeClient,
		nodeName:    "test-node",
		mode:        "ENIMultiIP",
		enableErdma: true,
	}

	pods, err := k8sObj.GetLocalPods()
	assert.NoError(t, err)
	assert.Len(t, pods, 1)
	assert.True(t, pods[0].ERdma)
}

// 新增测试: 测试已完成的Pod状态
func TestK8s_GetLocalPods_SandboxExited(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	podSucceeded := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-succeeded",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
		},
	}

	podFailed := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-failed",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}

	podRunning := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-running",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(podSucceeded, podFailed, podRunning).Build()

	k8sObj := &k8s{
		client:      fakeClient,
		nodeName:    "test-node",
		mode:        "ENIMultiIP",
		enableErdma: false,
	}

	pods, err := k8sObj.GetLocalPods()
	assert.NoError(t, err)
	assert.Len(t, pods, 3)

	for _, pod := range pods {
		if pod.Name == "pod-succeeded" || pod.Name == "pod-failed" {
			assert.True(t, pod.SandboxExited)
		} else if pod.Name == "pod-running" {
			assert.False(t, pod.SandboxExited)
		}
	}
}

// 新增测试: 测试IP保留注解
func TestK8s_GetLocalPods_IPStickTime(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	podWithReservation := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-with-reservation",
			Namespace: "default",
			Annotations: map[string]string{
				types.PodIPReservation: "true",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}

	podWithStatefulSet := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-with-statefulset",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "StatefulSet",
					Name: "test-sts",
				},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}

	k8sObj := &k8s{
		client:                  fake.NewClientBuilder().WithScheme(scheme).WithObjects(podWithReservation, podWithStatefulSet).Build(),
		nodeName:                "test-node",
		mode:                    "ENIMultiIP",
		enableErdma:             false,
		statefulWorkloadKindSet: sets.New[string]("statefulset"),
		Locker:                  &sync.RWMutex{},
	}

	pods, err := k8sObj.GetLocalPods()
	assert.NoError(t, err)
	assert.Len(t, pods, 2)

	for _, pod := range pods {
		assert.Equal(t, defaultStickTimeForSts, pod.IPStickTime)
	}
}

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

func TestConvertPodSetsIPStickTimeForStatefulSetOwner(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "StatefulSet",
					Name: "test-sts",
				},
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string]("statefulset"), pod)
	assert.Equal(t, defaultStickTimeForSts, result.IPStickTime)
}

func TestConvertPodNoIPStickTimeForNonStatefulWorkload(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "Deployment",
					Name: "test-deploy",
				},
			},
		},
	}
	result := convertPod(daemon.ModeENIMultiIP, false, sets.New[string]("statefulset"), pod)
	assert.Equal(t, time.Duration(0), result.IPStickTime)
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

func TestParseBandwidth(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    uint64
		expectError bool
	}{
		{
			name:        "invalid bytes",
			input:       "100",
			expected:    100,
			expectError: true,
		},
		{
			name:        "valid kilobytes",
			input:       "1K",
			expected:    1 * KILOBYTE,
			expectError: false,
		},
		{
			name:        "valid megabytes",
			input:       "1M",
			expected:    1 * MEGABYTE,
			expectError: false,
		},
		{
			name:        "valid gigabytes",
			input:       "1G",
			expected:    1 * GIGABYTE,
			expectError: false,
		},
		{
			name:        "valid terabytes",
			input:       "1T",
			expected:    1 * TERABYTE,
			expectError: false,
		},
		{
			name:        "invalid empty string",
			input:       "",
			expected:    0,
			expectError: true,
		},
		{
			name:        "invalid format",
			input:       "invalid",
			expected:    0,
			expectError: true,
		},
		{
			name:        "negative value",
			input:       "-1M",
			expected:    0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseBandwidth(tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestIsERDMA(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name: "pod with erdma resource in container",
			pod: &corev1.Pod{
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
			},
			expected: true,
		},
		{
			name: "pod with erdma resource in init container",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									deviceplugin.ERDMAResName: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "pod without erdma resource",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "pod with zero erdma resource",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									deviceplugin.ERDMAResName: resource.MustParse("0"),
								},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "pod with erdma in limits but not requests",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									deviceplugin.ERDMAResName: resource.MustParse("1"),
								},
								Requests: corev1.ResourceList{
									deviceplugin.ERDMAResName: resource.MustParse("0"),
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "pod with erdma in requests but not limits",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									deviceplugin.ERDMAResName: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "pod with multiple containers, one with erdma",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "container1",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("1"),
								},
							},
						},
						{
							Name: "container2",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									deviceplugin.ERDMAResName: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "empty pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isERDMA(tt.pod)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPodNetworkType(t *testing.T) {
	pod := &corev1.Pod{}

	result := podNetworkType(daemon.ModeENIMultiIP, pod)
	assert.Equal(t, daemon.PodNetworkTypeENIMultiIP, result)

	result = podNetworkType(daemon.ModeENIOnly, pod)
	assert.Equal(t, daemon.PodNetworkTypeVPCENI, result)
}

func TestParseBool(t *testing.T) {
	assert.True(t, parseBool("true"))
	assert.False(t, parseBool("false"))
	assert.False(t, parseBool("invalid"))
	assert.False(t, parseBool(""))
}

func TestPatchNodeIPResCondition(t *testing.T) {
	tests := []struct {
		name          string
		status        corev1.ConditionStatus
		reason        string
		message       string
		existingCond  *corev1.NodeCondition
		expectUpdated bool
	}{
		{
			name:          "add new condition",
			status:        corev1.ConditionTrue,
			reason:        "IPAvailable",
			message:       "IP is available",
			expectUpdated: true,
		},
		{
			name:    "update existing condition with different status",
			status:  corev1.ConditionFalse,
			reason:  "IPNotAvailable",
			message: "IP is not available",
			existingCond: &corev1.NodeCondition{
				Type:               types.SufficientIPCondition,
				Status:             corev1.ConditionTrue,
				Reason:             "IPAvailable",
				Message:            "IP is available",
				LastHeartbeatTime:  metav1.NewTime(time.Now().Add(-10 * time.Minute)),
				LastTransitionTime: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
			},
			expectUpdated: true,
		},
		{
			name:    "update existing condition with same values but old heartbeat",
			status:  corev1.ConditionTrue,
			reason:  "IPAvailable",
			message: "IP is available",
			existingCond: &corev1.NodeCondition{
				Type:               types.SufficientIPCondition,
				Status:             corev1.ConditionTrue,
				Reason:             "IPAvailable",
				Message:            "IP is available",
				LastHeartbeatTime:  metav1.NewTime(time.Now().Add(-6 * time.Minute)),
				LastTransitionTime: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
			},
			expectUpdated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client

			// Create node object
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{},
				},
			}
			client := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(node).Build()

			// Add existing condition (if any)
			if tt.existingCond != nil {
				node.Status.Conditions = append(node.Status.Conditions, *tt.existingCond)
			}

			// Create k8s instance
			k8sObj := &k8s{
				client:   client,
				nodeName: "test-node",
				node:     node,
			}

			// Execute PatchNodeIPResCondition
			err := k8sObj.PatchNodeIPResCondition(tt.status, tt.reason, tt.message)
			assert.NoError(t, err)

			// Verify results
			updatedNode, err := getNode(context.Background(), client, "test-node")
			assert.NoError(t, err)
			assert.NotNil(t, updatedNode)

			// Find SufficientIPCondition condition
			var condition *corev1.NodeCondition
			for i, cond := range updatedNode.Status.Conditions {
				if cond.Type == types.SufficientIPCondition {
					condition = &updatedNode.Status.Conditions[i]
					break
				}
			}

			assert.NotNil(t, condition)
			assert.Equal(t, types.SufficientIPCondition, condition.Type)
			assert.Equal(t, tt.status, condition.Status)
			assert.Equal(t, tt.reason, condition.Reason)
			assert.Equal(t, tt.message, condition.Message)

			// Check if timestamps were updated (based on test case)
			if tt.existingCond != nil && !tt.expectUpdated {
				// Should keep original timestamps
				assert.Equal(t, tt.existingCond.LastHeartbeatTime, condition.LastHeartbeatTime)
			} else {
				// Should update timestamps
				assert.True(t, condition.LastHeartbeatTime.After(time.Now().Add(-1*time.Minute)))
			}
		})
	}
}

func TestPatchNodeIPResCondition_NodeNotFound(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	k8sObj := &k8s{
		client:   client,
		nodeName: "non-existent-node",
	}

	err := k8sObj.PatchNodeIPResCondition(corev1.ConditionTrue, "TestReason", "TestMessage")
	assert.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestPatchNodeAnnotations(t *testing.T) {
	tests := []struct {
		name               string
		initialAnnotations map[string]string
		newAnnotations     map[string]string
		expectUpdated      bool
		expectError        bool
	}{
		{
			name:               "add new annotations",
			initialAnnotations: map[string]string{},
			newAnnotations:     map[string]string{"key1": "value1", "key2": "value2"},
			expectUpdated:      true,
			expectError:        false,
		},
		{
			name:               "update existing annotations",
			initialAnnotations: map[string]string{"key1": "oldValue", "key2": "value2"},
			newAnnotations:     map[string]string{"key1": "newValue", "key2": "value2"},
			expectUpdated:      true,
			expectError:        false,
		},
		{
			name:               "no update needed",
			initialAnnotations: map[string]string{"key1": "value1", "key2": "value2"},
			newAnnotations:     map[string]string{"key1": "value1", "key2": "value2"},
			expectUpdated:      false,
			expectError:        false,
		},
		{
			name:               "partial match annotations",
			initialAnnotations: map[string]string{"key1": "value1", "key2": "value2"},
			newAnnotations:     map[string]string{"key1": "value1", "key3": "value3"},
			expectUpdated:      true,
			expectError:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client

			// Create node object
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-node",
					Annotations: tt.initialAnnotations,
				},
			}
			client := fake.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(node).Build()

			// Create k8s instance
			k8sObj := &k8s{
				client:   client,
				nodeName: "test-node",
				node:     node,
			}

			// Execute PatchNodeAnnotations
			err := k8sObj.PatchNodeAnnotations(tt.newAnnotations)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify results
			updatedNode, err := getNode(context.Background(), client, "test-node")
			assert.NoError(t, err)
			assert.NotNil(t, updatedNode)

			if tt.expectUpdated || len(tt.newAnnotations) > 0 {
				// Check that all new annotations are set
				for key, value := range tt.newAnnotations {
					actualValue, exists := updatedNode.Annotations[key]
					assert.True(t, exists, "Annotation key %s should exist", key)
					assert.Equal(t, value, actualValue, "Annotation value for key %s should match", key)
				}
			} else if len(tt.newAnnotations) == 0 {
				// If new annotations are empty, should be no change
				assert.Equal(t, tt.initialAnnotations, updatedNode.Annotations)
			}
		})
	}
}

func TestPatchNodeAnnotations_NodeNotFound(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	k8sObj := &k8s{
		client:   client,
		nodeName: "non-existent-node",
	}

	err := k8sObj.PatchNodeAnnotations(map[string]string{"key": "value"})
	assert.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))
}

// Test for SetCustomStatefulWorkloadKinds
func TestSetCustomStatefulWorkloadKinds(t *testing.T) {
	k8sObj := &k8s{
		Locker: &sync.RWMutex{},
	}

	// Test initial stateful workload kinds
	err := k8sObj.SetCustomStatefulWorkloadKinds([]string{"StatefulSet"})
	assert.NoError(t, err)
	assert.True(t, k8sObj.statefulWorkloadKindSet.Has("statefulset"))

	// Test adding custom stateful workload kinds
	err = k8sObj.SetCustomStatefulWorkloadKinds([]string{"GameDeployment", "StatefulSetPlus"})
	assert.NoError(t, err)
	assert.True(t, k8sObj.statefulWorkloadKindSet.Has("statefulset"))
	assert.True(t, k8sObj.statefulWorkloadKindSet.Has("gamedeployment"))
	assert.True(t, k8sObj.statefulWorkloadKindSet.Has("statefulsetplus"))

	// Test adding duplicate kinds
	err = k8sObj.SetCustomStatefulWorkloadKinds([]string{" StatefulSet ", "  GameDeployment  "})
	assert.NoError(t, err)
	assert.True(t, k8sObj.statefulWorkloadKindSet.Has("statefulset"))
	assert.True(t, k8sObj.statefulWorkloadKindSet.Has("gamedeployment"))
}

// Test for PatchPodIPInfo
func TestPatchPodIPInfo(t *testing.T) {
	tests := []struct {
		name          string
		initialPodIPs string
		newPodIPs     string
		expectUpdate  bool
		expectError   bool
		podNotFound   bool
	}{
		{
			name:          "update pod ips",
			initialPodIPs: "10.0.0.1",
			newPodIPs:     "10.0.0.1,10.0.0.2",
			expectUpdate:  true,
			expectError:   false,
			podNotFound:   false,
		},
		{
			name:          "no update needed",
			initialPodIPs: "10.0.0.1",
			newPodIPs:     "10.0.0.1",
			expectUpdate:  false,
			expectError:   false,
			podNotFound:   false,
		},
		{
			name:          "pod not found",
			initialPodIPs: "",
			newPodIPs:     "10.0.0.1",
			expectUpdate:  false,
			expectError:   true,
			podNotFound:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create pod object
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			}

			if !tt.podNotFound {
				if tt.initialPodIPs != "" {
					pod.Annotations[types.PodIPs] = tt.initialPodIPs
				}
			}

			// Create fake client
			builder := fake.NewClientBuilder().WithScheme(scheme.Scheme)
			if !tt.podNotFound {
				builder.WithObjects(pod)
			}
			c := builder.Build()

			// Create k8s instance
			k8sObj := &k8s{
				client: c,
			}

			// Execute PatchPodIPInfo
			podInfo := &daemon.PodInfo{
				Name:      "test-pod",
				Namespace: "default",
			}
			err := k8sObj.PatchPodIPInfo(podInfo, tt.newPodIPs)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			if tt.expectUpdate {
				// Verify results
				updatedPod := &corev1.Pod{}
				err = c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-pod"}, updatedPod)
				assert.NoError(t, err)
				assert.Equal(t, tt.newPodIPs, updatedPod.Annotations[types.PodIPs])
			}
		})
	}
}

// Test for GetTrunkID
func TestGetTrunkID(t *testing.T) {
	tests := []struct {
		name          string
		trunkID       string
		expectedTrunk string
	}{
		{
			name:          "trunk id exists",
			trunkID:       "eni-12345",
			expectedTrunk: "eni-12345",
		},
		{
			name:          "trunk id empty",
			trunkID:       "",
			expectedTrunk: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create node object
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-node",
					Annotations: map[string]string{},
				},
			}
			if tt.trunkID != "" {
				node.Annotations[types.TrunkOn] = tt.trunkID
			}

			// Create k8s instance
			k8sObj := &k8s{
				node: node,
			}

			// Execute GetTrunkID
			trunkID := k8sObj.GetTrunkID()
			assert.Equal(t, tt.expectedTrunk, trunkID)
		})
	}
}

// Mock storage for testing
type mockStorage struct {
	mock.Mock
	data map[string]interface{}
}

func (m *mockStorage) Put(key string, item interface{}) error {
	args := m.Called(key, item)
	if args.Error(0) == nil {
		if m.data == nil {
			m.data = make(map[string]interface{})
		}
		m.data[key] = item
	}
	return args.Error(0)
}

func (m *mockStorage) Get(key string) (interface{}, error) {
	args := m.Called(key)
	if args.Error(1) != nil {
		return nil, args.Error(1)
	}
	if item, exists := m.data[key]; exists {
		return item, nil
	}
	return args.Get(0), args.Error(1)
}

func (m *mockStorage) Delete(key string) error {
	args := m.Called(key)
	if args.Error(0) == nil && m.data != nil {
		delete(m.data, key)
	}
	return args.Error(0)
}

func (m *mockStorage) List() ([]interface{}, error) {
	args := m.Called()
	if args.Error(1) != nil {
		return nil, args.Error(1)
	}
	var items []interface{}
	for _, item := range m.data {
		items = append(items, item)
	}
	if args.Get(0) != nil {
		return args.Get(0).([]interface{}), args.Error(1)
	}
	return items, args.Error(1)
}

// ==============================================================================
// setSvcCIDR TESTS
// ==============================================================================

func TestSetSvcCIDR(t *testing.T) {
	tests := []struct {
		name        string
		inputCIDR   *types.IPNetSet
		expectError bool
		description string
	}{
		{
			name: "set service CIDR with IPv4",
			inputCIDR: func() *types.IPNetSet {
				cidr := &types.IPNetSet{}
				_, ipNet, _ := net.ParseCIDR("10.96.0.0/12")
				cidr.IPv4 = ipNet
				return cidr
			}(),
			expectError: false,
			description: "Should successfully set IPv4 service CIDR",
		},
		{
			name: "set service CIDR with IPv6",
			inputCIDR: func() *types.IPNetSet {
				cidr := &types.IPNetSet{}
				_, ipNet, _ := net.ParseCIDR("fd00:10:96::/108")
				cidr.IPv6 = ipNet
				return cidr
			}(),
			expectError: true, // Will fail because IPv4 is nil and no kubeadm config
			description: "Should attempt to fetch IPv4 service CIDR from API server when IPv4 is nil",
		},
		{
			name: "set service CIDR with both IPv4 and IPv6",
			inputCIDR: func() *types.IPNetSet {
				cidr := &types.IPNetSet{}
				_, ipNet4, _ := net.ParseCIDR("10.96.0.0/12")
				_, ipNet6, _ := net.ParseCIDR("fd00:10:96::/108")
				cidr.IPv4 = ipNet4
				cidr.IPv6 = ipNet6
				return cidr
			}(),
			expectError: false,
			description: "Should successfully set dual-stack service CIDR",
		},
		{
			name: "set service CIDR with nil IPv4 - should fetch from API server",
			inputCIDR: func() *types.IPNetSet {
				cidr := &types.IPNetSet{}
				// IPv4 is nil, should trigger API server fetch
				return cidr
			}(),
			expectError: true, // Will fail because no kubeadm-config in fake client
			description: "Should attempt to fetch service CIDR from API server when IPv4 is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with kubeadm config for successful API server fetch
			builder := fake.NewClientBuilder().WithScheme(scheme.Scheme)
			if tt.name == "set service CIDR with nil IPv4 - should fetch from API server" ||
				tt.name == "set service CIDR with IPv6" {
				// Add kubeadm configmap for successful test
				kubeadmCM := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      k8sKubeadmConfigmap,
						Namespace: k8sSystemNamespace,
					},
					Data: map[string]string{
						k8sKubeadmConfigmapNetworking: "networking:\n  serviceSubnet: 10.96.0.0/12",
					},
				}
				builder.WithObjects(kubeadmCM)
				tt.expectError = false // Should succeed now
			}

			client := builder.Build()

			k8sObj := &k8s{
				client: client,
				Locker: &sync.RWMutex{},
			}

			err := k8sObj.setSvcCIDR(tt.inputCIDR)

			if tt.expectError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
				assert.NotNil(t, k8sObj.svcCIDR, "Service CIDR should be set")
				if tt.inputCIDR.IPv4 != nil {
					assert.Equal(t, tt.inputCIDR.IPv4, k8sObj.svcCIDR.IPv4, "IPv4 CIDR should match")
				}
				if tt.inputCIDR.IPv6 != nil {
					assert.Equal(t, tt.inputCIDR.IPv6, k8sObj.svcCIDR.IPv6, "IPv6 CIDR should match")
				}
			}
		})
	}
}

func TestSetSvcCIDR_APIServerError(t *testing.T) {
	// Test case where API server fetch fails
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build() // No kubeadm config

	k8sObj := &k8s{
		client: client,
		Locker: &sync.RWMutex{},
	}

	cidr := &types.IPNetSet{} // IPv4 is nil, should trigger API server fetch

	err := k8sObj.setSvcCIDR(cidr)
	assert.Error(t, err, "Should fail when kubeadm config is not found")
	assert.Contains(t, err.Error(), "error retrieving service cidr", "Error should mention service CIDR retrieval")
}

// ==============================================================================
// GetPod TESTS
// ==============================================================================

func TestGetPod(t *testing.T) {
	tests := []struct {
		name          string
		podExists     bool
		useCache      bool
		storageExists bool
		storageError  error
		expectError   bool
		description   string
	}{
		{
			name:          "get existing pod with cache",
			podExists:     true,
			useCache:      true,
			storageExists: false,
			expectError:   false,
			description:   "Should successfully get pod from Kubernetes API",
		},
		{
			name:          "get existing pod without cache",
			podExists:     true,
			useCache:      false,
			storageExists: false,
			expectError:   false,
			description:   "Should successfully get pod from Kubernetes API without cache",
		},
		{
			name:          "get non-existent pod with storage fallback",
			podExists:     false,
			useCache:      true,
			storageExists: true,
			expectError:   false,
			description:   "Should get pod from storage when not found in Kubernetes API",
		},
		{
			name:          "get non-existent pod without storage fallback",
			podExists:     false,
			useCache:      true,
			storageExists: false,
			expectError:   true,
			description:   "Should return error when pod not found in API and storage",
		},
		{
			name:          "storage error on fallback",
			podExists:     false,
			useCache:      true,
			storageExists: false,
			storageError:  fmt.Errorf("storage error"),
			expectError:   true,
			description:   "Should return storage error when storage operation fails",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client
			builder := fake.NewClientBuilder().WithScheme(scheme.Scheme)
			if tt.podExists {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						PodIP: "10.0.0.1",
					},
				}
				builder.WithObjects(pod)
			}
			client := builder.Build()

			// Setup mock storage
			mockStore := &mockStorage{data: make(map[string]interface{})}

			// Only set up storage expectations when pod doesn't exist in Kubernetes
			if !tt.podExists {
				if tt.storageExists {
					storageItem := &storageItem{
						Pod: &daemon.PodInfo{
							Name:      "test-pod",
							Namespace: "default",
						},
					}
					mockStore.data["default/test-pod"] = storageItem
					mockStore.On("Get", "default/test-pod").Return(storageItem, nil)
				} else if tt.storageError != nil {
					mockStore.On("Get", "default/test-pod").Return(nil, tt.storageError)
				} else {
					mockStore.On("Get", "default/test-pod").Return(nil, storage.ErrNotFound)
				}
			} else {
				// When pod exists in Kubernetes, we should expect Put to be called
				mockStore.On("Put", "default/test-pod", mock.Anything).Return(nil)
			}

			k8sObj := &k8s{
				client:                  client,
				storage:                 mockStore,
				mode:                    daemon.ModeENIMultiIP,
				enableErdma:             false,
				statefulWorkloadKindSet: sets.New[string](),
			}

			// Execute GetPod
			podInfo, err := k8sObj.GetPod(context.Background(), "default", "test-pod", tt.useCache)

			if tt.expectError {
				assert.Error(t, err, tt.description)
				assert.Nil(t, podInfo, "PodInfo should be nil on error")
			} else {
				assert.NoError(t, err, tt.description)
				assert.NotNil(t, podInfo, "PodInfo should not be nil")
				assert.Equal(t, "test-pod", podInfo.Name, "Pod name should match")
				assert.Equal(t, "default", podInfo.Namespace, "Pod namespace should match")
			}

			mockStore.AssertExpectations(t)
		})
	}
}

func TestGetPod_StoragePutError(t *testing.T) {
	// Test case where storage put fails
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pod).Build()

	mockStore := &mockStorage{}
	mockStore.On("Put", "default/test-pod", mock.Anything).Return(fmt.Errorf("storage put error"))

	k8sObj := &k8s{
		client:                  client,
		storage:                 mockStore,
		mode:                    daemon.ModeENIMultiIP,
		enableErdma:             false,
		statefulWorkloadKindSet: sets.New[string](),
	}

	podInfo, err := k8sObj.GetPod(context.Background(), "default", "test-pod", true)

	assert.Error(t, err, "Should return error when storage put fails")
	assert.Nil(t, podInfo, "PodInfo should be nil on error")
	assert.Contains(t, err.Error(), "storage put error", "Error should contain storage error message")
}

// ==============================================================================
// clean TESTS
// ==============================================================================

func TestClean(t *testing.T) {
	tests := []struct {
		name           string
		storageItems   []interface{}
		localPods      []*daemon.PodInfo
		expectDeleted  []string
		expectTagged   []string
		expectUntagged []string
		description    string
	}{
		{
			name: "clean deleted pods from storage",
			storageItems: []interface{}{
				&storageItem{
					Pod: &daemon.PodInfo{Name: "pod1", Namespace: "default"},
				},
				&storageItem{
					Pod: &daemon.PodInfo{Name: "pod2", Namespace: "default"},
				},
			},
			localPods: []*daemon.PodInfo{
				{Name: "pod1", Namespace: "default"}, // pod1 still exists
			},
			expectTagged:   []string{"default/pod2"}, // pod2 should be tagged for deletion
			expectUntagged: []string{},               // pod1 should not need untagging since it's not tagged
			description:    "Should tag non-existent pods for deletion",
		},
		{
			name: "delete old tagged items",
			storageItems: []interface{}{
				&storageItem{
					Pod:          &daemon.PodInfo{Name: "old-pod", Namespace: "default"},
					deletionTime: func() *time.Time { t := time.Now().Add(-2 * time.Hour); return &t }(), // Old deletion time
				},
			},
			localPods:     []*daemon.PodInfo{},
			expectDeleted: []string{"default/old-pod"},
			description:   "Should delete items tagged for deletion beyond timeout",
		},
		{
			name: "keep recently tagged items",
			storageItems: []interface{}{
				&storageItem{
					Pod:          &daemon.PodInfo{Name: "recent-pod", Namespace: "default"},
					deletionTime: func() *time.Time { t := time.Now().Add(-30 * time.Minute); return &t }(), // Recent deletion time
				},
			},
			localPods:   []*daemon.PodInfo{},
			description: "Should keep items tagged for deletion within timeout",
		},
		{
			name: "untag restored pods",
			storageItems: []interface{}{
				&storageItem{
					Pod:          &daemon.PodInfo{Name: "restored-pod", Namespace: "default"},
					deletionTime: func() *time.Time { t := time.Now().Add(-30 * time.Minute); return &t }(), // Was tagged for deletion
				},
			},
			localPods: []*daemon.PodInfo{
				{Name: "restored-pod", Namespace: "default"}, // Pod came back
			},
			expectUntagged: []string{"default/restored-pod"},
			description:    "Should untag pods that were restored",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client for GetLocalPods
			builder := fake.NewClientBuilder().WithScheme(scheme.Scheme)
			for _, pod := range tt.localPods {
				k8sPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pod.Name,
						Namespace: pod.Namespace,
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
				}
				builder.WithObjects(k8sPod)
			}
			client := builder.Build()

			// Setup mock storage
			mockStore := &mockStorage{data: make(map[string]interface{})}
			for _, item := range tt.storageItems {
				storageItem := item.(*storageItem)
				key := fmt.Sprintf("%s/%s", storageItem.Pod.Namespace, storageItem.Pod.Name)
				mockStore.data[key] = item
			}

			mockStore.On("List").Return(tt.storageItems, nil)

			// Setup expectations for Put operations (tagging/untagged)
			for _, key := range tt.expectTagged {
				mockStore.On("Put", key, mock.MatchedBy(func(item interface{}) bool {
					if si, ok := item.(*storageItem); ok {
						return si.deletionTime != nil
					}
					return false
				})).Return(nil)
			}
			for _, key := range tt.expectUntagged {
				mockStore.On("Put", key, mock.MatchedBy(func(item interface{}) bool {
					if si, ok := item.(*storageItem); ok {
						return si.deletionTime == nil
					}
					return false
				})).Return(nil)
			}

			// Setup expectations for Delete operations
			for _, key := range tt.expectDeleted {
				mockStore.On("Delete", key).Return(nil)
			}

			k8sObj := &k8s{
				client:                  client,
				storage:                 mockStore,
				nodeName:                "test-node",
				mode:                    daemon.ModeENIMultiIP,
				enableErdma:             false,
				statefulWorkloadKindSet: sets.New[string](),
			}

			// Execute clean
			err := k8sObj.clean()

			assert.NoError(t, err, tt.description)
			mockStore.AssertExpectations(t)
		})
	}
}

func TestClean_Errors(t *testing.T) {
	tests := []struct {
		name        string
		storageErr  error
		description string
	}{
		{
			name:        "storage list error",
			storageErr:  fmt.Errorf("storage list error"),
			description: "Should return error when storage list fails",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client
			client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

			// Setup mock storage
			mockStore := &mockStorage{}
			mockStore.On("List").Return(nil, tt.storageErr)

			k8sObj := &k8s{
				client:   client,
				storage:  mockStore,
				nodeName: "test-node",
				mode:     daemon.ModeENIMultiIP,
			}

			err := k8sObj.clean()

			assert.Error(t, err, tt.description)
			assert.Contains(t, err.Error(), "storage list error", "Should contain storage error")
		})
	}
}

// Test clean method with storage operations errors
func TestClean_StorageOperationErrors(t *testing.T) {
	tests := []struct {
		name        string
		operation   string
		expectError bool
		description string
	}{
		{
			name:        "storage put error when tagging for deletion",
			operation:   "put_error_tag",
			expectError: true,
			description: "Should return error when storage Put fails during tagging",
		},
		{
			name:        "storage put error when untagging",
			operation:   "put_error_untag",
			expectError: true,
			description: "Should return error when storage Put fails during untagging",
		},
		{
			name:        "storage delete error",
			operation:   "delete_error",
			expectError: true,
			description: "Should return error when storage Delete fails",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client
			client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

			// Setup mock storage with different error scenarios
			mockStore := &mockStorage{data: make(map[string]interface{})}

			switch tt.operation {
			case "put_error_tag":
				// Item exists in storage but not in local pods - should be tagged for deletion
				storageItem := &storageItem{
					Pod: &daemon.PodInfo{Name: "pod1", Namespace: "default"},
				}
				mockStore.data["default/pod1"] = storageItem
				mockStore.On("List").Return([]interface{}{storageItem}, nil)
				mockStore.On("Put", "default/pod1", mock.Anything).Return(fmt.Errorf("put error"))

			case "put_error_untag":
				// Item exists in storage and also in local pods - should be untagged
				storageItem := &storageItem{
					Pod:          &daemon.PodInfo{Name: "pod1", Namespace: "default"},
					deletionTime: func() *time.Time { t := time.Now(); return &t }(),
				}
				mockStore.data["default/pod1"] = storageItem
				mockStore.On("List").Return([]interface{}{storageItem}, nil)
				mockStore.On("Put", "default/pod1", mock.Anything).Return(fmt.Errorf("put error"))

				// Add corresponding pod to client
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod1",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{NodeName: "test-node"},
				}
				client = fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pod).Build()

			case "delete_error":
				// Old item should be deleted
				oldTime := time.Now().Add(-2 * time.Hour)
				storageItem := &storageItem{
					Pod:          &daemon.PodInfo{Name: "old-pod", Namespace: "default"},
					deletionTime: &oldTime,
				}
				mockStore.data["default/old-pod"] = storageItem
				mockStore.On("List").Return([]interface{}{storageItem}, nil)
				mockStore.On("Delete", "default/old-pod").Return(fmt.Errorf("delete error"))
			}

			k8sObj := &k8s{
				client:                  client,
				storage:                 mockStore,
				nodeName:                "test-node",
				mode:                    daemon.ModeENIMultiIP,
				enableErdma:             false,
				statefulWorkloadKindSet: sets.New[string](),
			}

			err := k8sObj.clean()

			if tt.expectError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}

			mockStore.AssertExpectations(t)
		})
	}
}

// ==============================================================================
// RecordPodEvent TESTS
// ==============================================================================

func TestRecordPodEvent(t *testing.T) {
	tests := []struct {
		name        string
		podExists   bool
		expectError bool
		description string
	}{
		{
			name:        "record event for existing pod",
			podExists:   true,
			expectError: false,
			description: "Should successfully record event for existing pod",
		},
		{
			name:        "record event for non-existent pod",
			podExists:   false,
			expectError: true,
			description: "Should return error when pod does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client
			builder := fake.NewClientBuilder().WithScheme(scheme.Scheme)
			if tt.podExists {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						UID:       "test-uid",
					},
				}
				builder.WithObjects(pod)
			}
			client := builder.Build()

			// Create fake recorder
			recorder := record.NewFakeRecorder(10)

			k8sObj := &k8s{
				client:   client,
				recorder: recorder,
			}

			// Execute RecordPodEvent
			err := k8sObj.RecordPodEvent("test-pod", "default", corev1.EventTypeNormal, "TestReason", "Test message")

			if tt.expectError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)

				// Verify event was recorded
				select {
				case event := <-recorder.Events:
					assert.Contains(t, event, "TestReason", "Event should contain the reason")
					assert.Contains(t, event, "Test message", "Event should contain the message")
				case <-time.After(100 * time.Millisecond):
					t.Fatal("Expected event was not recorded")
				}
			}
		})
	}
}

func TestRecordPodEvent_GetPodError(t *testing.T) {
	// Test specific error scenarios
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build() // No pod exists

	recorder := record.NewFakeRecorder(10)

	k8sObj := &k8s{
		client:   client,
		recorder: recorder,
	}

	err := k8sObj.RecordPodEvent("non-existent-pod", "default", corev1.EventTypeWarning, "TestReason", "Test message")

	assert.Error(t, err, "Should return error when pod does not exist")
	assert.True(t, apierrors.IsNotFound(err), "Error should be NotFound")

	// Verify no event was recorded
	select {
	case <-recorder.Events:
		t.Fatal("No event should have been recorded for non-existent pod")
	case <-time.After(100 * time.Millisecond):
		// Expected - no event recorded
	}
}

// ==============================================================================
// GetNodeDynamicConfigLabel TESTS
// ==============================================================================

func TestGetNodeDynamicConfigLabel(t *testing.T) {
	tests := []struct {
		name           string
		nodeLabels     map[string]string
		expectedConfig string
		description    string
	}{
		{
			name: "node with dynamic config label",
			nodeLabels: map[string]string{
				labelDynamicConfig: "my-config",
				"other-label":      "other-value",
			},
			expectedConfig: "my-config",
			description:    "Should return the dynamic config label value",
		},
		{
			name: "node without dynamic config label",
			nodeLabels: map[string]string{
				"other-label": "other-value",
			},
			expectedConfig: "",
			description:    "Should return empty string when label is not present",
		},
		{
			name:           "node with no labels",
			nodeLabels:     nil,
			expectedConfig: "",
			description:    "Should return empty string when node has no labels",
		},
		{
			name: "node with empty dynamic config label",
			nodeLabels: map[string]string{
				labelDynamicConfig: "",
				"other-label":      "other-value",
			},
			expectedConfig: "",
			description:    "Should return empty string when label value is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create node with specified labels
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-node",
					Labels: tt.nodeLabels,
				},
			}

			k8sObj := &k8s{
				node: node,
			}

			// Execute GetNodeDynamicConfigLabel
			result := k8sObj.GetNodeDynamicConfigLabel()

			assert.Equal(t, tt.expectedConfig, result, tt.description)
		})
	}
}

// ==============================================================================
// ADDITIONAL HELPER TESTS
// ==============================================================================

func TestGetDynamicConfigWithName(t *testing.T) {
	tests := []struct {
		name            string
		configMapExists bool
		hasEniConf      bool
		eniConfContent  string
		expectError     bool
		expectedContent string
		description     string
	}{
		{
			name:            "get config with valid eni_conf",
			configMapExists: true,
			hasEniConf:      true,
			eniConfContent:  `{"ip_stack": "dual"}`,
			expectError:     false,
			expectedContent: `{"ip_stack": "dual"}`,
			description:     "Should return eni_conf content from ConfigMap",
		},
		{
			name:            "get config without eni_conf key",
			configMapExists: true,
			hasEniConf:      false,
			expectError:     true,
			description:     "Should return error when eni_conf key is missing",
		},
		{
			name:            "get config with non-existent ConfigMap",
			configMapExists: false,
			expectError:     true,
			description:     "Should return error when ConfigMap does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client
			builder := fake.NewClientBuilder().WithScheme(scheme.Scheme)
			if tt.configMapExists {
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-config",
						Namespace: "test-namespace",
					},
					Data: map[string]string{},
				}
				if tt.hasEniConf {
					cm.Data["eni_conf"] = tt.eniConfContent
				}
				builder.WithObjects(cm)
			}
			client := builder.Build()

			k8sObj := &k8s{
				client:          client,
				daemonNamespace: "test-namespace",
			}

			// Execute GetDynamicConfigWithName
			content, err := k8sObj.GetDynamicConfigWithName(context.Background(), "test-config")

			if tt.expectError {
				assert.Error(t, err, tt.description)
				assert.Empty(t, content, "Content should be empty on error")
			} else {
				assert.NoError(t, err, tt.description)
				assert.Equal(t, tt.expectedContent, content, "Content should match expected")
			}
		})
	}
}

func TestSerialize(t *testing.T) {
	v, err := serialize(&storageItem{})
	assert.NoError(t, err)
	assert.NotEmpty(t, v)

	_, err = deserialize(v)
	assert.NoError(t, err)
}
