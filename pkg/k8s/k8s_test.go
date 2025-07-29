package k8s

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/deviceplugin"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/terway/types/daemon"
)

type mockClient struct {
	client.Client
	mock.Mock
}

func (m *mockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	args := m.Called(ctx, key, obj)
	if pod, ok := obj.(*corev1.Pod); ok {
		if returnedPod, ok := args.Get(0).(*corev1.Pod); ok {
			*pod = *returnedPod
		}
	}
	return args.Error(1)
}

func (m *mockClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	args := m.Called(ctx, list)
	if podList, ok := list.(*corev1.PodList); ok {
		if returnedList, ok := args.Get(0).(*corev1.PodList); ok {
			*podList = *returnedList
		}
	}
	return args.Error(1)
}

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
