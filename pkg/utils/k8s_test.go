package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func TestFinalStatus(t *testing.T) {
	now := metav1.Now()
	later := metav1.Time{Time: now.Add(1 * time.Hour)}
	earlier := metav1.Time{Time: now.Add(-1 * time.Hour)}

	tests := []struct {
		name           string
		status         map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo
		expectedStatus v1beta1.CNIStatus
		expectedInfo   *v1beta1.CNIStatusInfo
		expectedOk     bool
	}{
		{
			name:           "Empty map",
			status:         map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{},
			expectedStatus: "", // should return zero value
			expectedInfo:   nil,
			expectedOk:     false,
		},
		{
			name: "Single Initial status",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusInitial: {LastUpdateTime: now},
			},
			expectedStatus: v1beta1.CNIStatusInitial,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: now},
			expectedOk:     true,
		},
		{
			name: "Single Deleted status",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusDeleted: {LastUpdateTime: now},
			},
			expectedStatus: v1beta1.CNIStatusDeleted,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: now},
			expectedOk:     true,
		},
		{
			name: "Both status with Initial newer",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusInitial: {LastUpdateTime: later},
				v1beta1.CNIStatusDeleted: {LastUpdateTime: earlier},
			},
			expectedStatus: v1beta1.CNIStatusInitial,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: later},
			expectedOk:     true,
		},
		{
			name: "Both status with Deleted newer",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusInitial: {LastUpdateTime: earlier},
				v1beta1.CNIStatusDeleted: {LastUpdateTime: later},
			},
			expectedStatus: v1beta1.CNIStatusDeleted,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: later},
			expectedOk:     true,
		},
		{
			name: "Nil status info",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusInitial: nil,
				v1beta1.CNIStatusDeleted: {LastUpdateTime: now},
			},
			expectedStatus: v1beta1.CNIStatusDeleted,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: now},
			expectedOk:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultStatus, resultInfo, resultOk := RuntimeFinalStatus(tt.status)
			assert.Equal(t, tt.expectedStatus, resultStatus)
			assert.Equal(t, tt.expectedInfo, resultInfo)
			assert.Equal(t, tt.expectedOk, resultOk)
		})
	}
}

func TestSlimPod(t *testing.T) {
	t.Run("Slim normal pod", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-pod",
				Annotations: map[string]string{"foo": "bar"},
				Labels:      map[string]string{"app": "test"},
			},
			Spec: corev1.PodSpec{
				HostNetwork: false,
				Volumes:     []corev1.Volume{{Name: "vol"}},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{Name: "container"}},
			},
		}

		result, err := SlimPod(pod)
		require.NoError(t, err)

		slimPod := result.(*corev1.Pod)
		assert.NotNil(t, slimPod.Annotations)
		assert.NotNil(t, slimPod.Labels)
		assert.Nil(t, slimPod.Spec.Volumes)
		assert.Nil(t, slimPod.Status.ContainerStatuses)
	})

	t.Run("Slim host network pod", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-pod",
				Annotations: map[string]string{"foo": "bar"},
				Labels:      map[string]string{"app": "test"},
			},
			Spec: corev1.PodSpec{
				HostNetwork: true,
			},
		}

		result, err := SlimPod(pod)
		require.NoError(t, err)

		slimPod := result.(*corev1.Pod)
		assert.Nil(t, slimPod.Annotations)
		assert.Nil(t, slimPod.Labels)
	})

	t.Run("Unexpected type", func(t *testing.T) {
		result, err := SlimPod("not a pod")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestSlimNode(t *testing.T) {
	t.Run("Slim node", func(t *testing.T) {
		node := &corev1.Node{
			Status: corev1.NodeStatus{
				Images:          []corev1.ContainerImage{{Names: []string{"img"}}},
				VolumesInUse:    []corev1.UniqueVolumeName{"vol"},
				VolumesAttached: []corev1.AttachedVolume{{Name: "vol"}},
			},
		}

		result, err := SlimNode(node)
		require.NoError(t, err)

		slimNode := result.(*corev1.Node)
		assert.Nil(t, slimNode.Status.Images)
		assert.Nil(t, slimNode.Status.VolumesInUse)
		assert.Nil(t, slimNode.Status.VolumesAttached)
	})

	t.Run("Unexpected type", func(t *testing.T) {
		result, err := SlimNode("not a node")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

// TestSetStsKinds tests the SetStsKinds function
func TestSetStsKinds(t *testing.T) {
	// Test case 1: Add single custom kind
	t.Run("Add single custom kind", func(t *testing.T) {
		// Save original state
		originalKinds := make([]string, len(stsKinds))
		copy(originalKinds, stsKinds)
		defer func() {
			// Restore original state
			stsKinds = originalKinds
		}()

		SetStsKinds([]string{"CustomStatefulSet"})

		// Verify the custom kind was added
		found := false
		for _, kind := range stsKinds {
			if kind == "CustomStatefulSet" {
				found = true
				break
			}
		}
		require.True(t, found, "Expected CustomStatefulSet to be added to stsKinds")
	})

	// Test case 2: Add multiple custom kinds
	t.Run("Add multiple custom kinds", func(t *testing.T) {
		// Save original state
		originalKinds := make([]string, len(stsKinds))
		copy(originalKinds, stsKinds)
		defer func() {
			// Restore original state
			stsKinds = originalKinds
		}()

		customKinds := []string{"CustomKind1", "CustomKind2", "CustomKind3"}
		SetStsKinds(customKinds)

		// Verify all custom kinds were added
		for _, customKind := range customKinds {
			found := false
			for _, kind := range stsKinds {
				if kind == customKind {
					found = true
					break
				}
			}
			require.True(t, found, "Expected %s to be added to stsKinds", customKind)
		}
	})

	// Test case 3: Add empty slice
	t.Run("Add empty slice", func(t *testing.T) {
		// Save original state
		originalKinds := make([]string, len(stsKinds))
		copy(originalKinds, stsKinds)
		originalLen := len(stsKinds)
		defer func() {
			// Restore original state
			stsKinds = originalKinds
		}()

		SetStsKinds([]string{})

		// Verify length didn't change
		require.Equal(t, originalLen, len(stsKinds), "Expected stsKinds length to remain unchanged")
	})

	// Test case 4: Verify integration with IsFixedNamePod
	t.Run("Integration with IsFixedNamePod", func(t *testing.T) {
		// Save original state
		originalKinds := make([]string, len(stsKinds))
		copy(originalKinds, stsKinds)
		defer func() {
			// Restore original state
			stsKinds = originalKinds
		}()

		// Add a custom kind
		SetStsKinds([]string{"CustomWorkload"})

		// Create a pod with the custom kind as owner
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind: "CustomWorkload",
						Name: "test-custom-workload",
					},
				},
			},
		}

		// Verify the pod is recognized as fixed name pod
		result := IsFixedNamePod(pod)
		require.True(t, result, "Expected pod with custom workload owner to be recognized as fixed name pod")
	})
}

// TestIsFixedNamePod tests the IsFixedNamePod function
func TestIsFixedNamePod(t *testing.T) {
	// Test case 1: Pod without owner references should return true
	t.Run("Pod without owner references", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-pod-1",
				OwnerReferences: []metav1.OwnerReference{}, // No owner references
			},
		}

		result := IsFixedNamePod(pod)
		require.True(t, result, "Expected true for pod without owner references, got false")
	})

	// Test case 2: Pod with owner references but not StatefulSet kind
	t.Run("Pod with non-StatefulSet owner", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-2",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind: "Deployment",
						Name: "test-deployment",
					},
					{
						Kind: "ReplicaSet",
						Name: "test-replicaset",
					},
				},
			},
		}

		result := IsFixedNamePod(pod)
		require.False(t, result, "Expected true for pod with non-StatefulSet owner, got false")
	})

	// Test case 3: Pod with StatefulSet owner reference
	t.Run("Pod with StatefulSet owner", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-3",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind: "StatefulSet",
						Name: "test-statefulset",
					},
				},
			},
		}

		result := IsFixedNamePod(pod)
		require.True(t, result, "Expected true for pod with StatefulSet owner, got false")
	})
}

// TestIsDaemonSetPod tests the IsDaemonSetPod function
func TestIsDaemonSetPod(t *testing.T) {
	// Test case 1: Pod without owner references should return false
	t.Run("Pod without owner references", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-pod-1",
				OwnerReferences: []metav1.OwnerReference{},
			},
		}

		result := IsDaemonSetPod(pod)
		require.False(t, result, "Expected false for pod without owner references, got true")
	})

	// Test case 2: Pod with owner references but not DaemonSet kind
	t.Run("Pod with non-DaemonSet owner", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-2",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind: "Deployment",
						Name: "test-deployment",
					},
					{
						Kind: "ReplicaSet",
						Name: "test-replicaset",
					},
				},
			},
		}

		result := IsDaemonSetPod(pod)
		require.False(t, result, "Expected false for pod with non-DaemonSet owner, got true")
	})

	// Test case 3: Pod with DaemonSet owner reference
	t.Run("Pod with DaemonSet owner", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-3",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind: "DaemonSet",
						Name: "test-daemonset",
					},
				},
			},
		}

		result := IsDaemonSetPod(pod)
		require.True(t, result, "Expected true for pod with DaemonSet owner, got false")
	})

	// Test case 4: Pod with mixed owner references including DaemonSet
	t.Run("Pod with mixed owners including DaemonSet", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-4",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind: "ReplicaSet",
						Name: "test-replicaset",
					},
					{
						Kind: "DaemonSet",
						Name: "test-daemonset",
					},
				},
			},
		}

		result := IsDaemonSetPod(pod)
		require.True(t, result, "Expected true for pod with DaemonSet owner, got false")
	})
}

// TestISVKNode tests the ISVKNode function
func TestISVKNode(t *testing.T) {
	// Test case 1: Node with VK label should return true
	t.Run("Node with VK label", func(t *testing.T) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-vk-node",
				Labels: map[string]string{
					"type": "virtual-kubelet",
				},
			},
		}

		result := ISVKNode(node)
		require.True(t, result, "Expected true for node with VK label, got false")
	})

	// Test case 2: Node without VK label should return false
	t.Run("Node without VK label", func(t *testing.T) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-normal-node",
				Labels: map[string]string{
					"kubernetes.io/os":        "linux",
					"beta.kubernetes.io/arch": "amd64",
				},
			},
		}

		result := ISVKNode(node)
		require.False(t, result, "Expected false for node without VK label, got true")
	})

	// Test case 3: Node with empty labels should return false
	t.Run("Node with empty labels", func(t *testing.T) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "test-empty-node",
				Labels: map[string]string{},
			},
		}

		result := ISVKNode(node)
		require.False(t, result, "Expected false for node with empty labels, got true")
	})

	// Test case 4: Node without labels should return false
	t.Run("Node without labels", func(t *testing.T) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-nil-node",
			},
		}

		result := ISVKNode(node)
		require.False(t, result, "Expected false for node without labels, got true")
	})
}

// TestISLingJunNode tests the ISLingJunNode function
func TestISLingJunNode(t *testing.T) {
	// Test case 1: Node with LingJun annotation should return true
	t.Run("Node with LingJun annotation", func(t *testing.T) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-lingjun-node",
				Labels: map[string]string{
					"alibabacloud.com/lingjun-worker": "true",
				},
			},
		}

		result := ISLingJunNode(node.Labels)
		require.True(t, result, "Expected true for node with LingJun annotation, got false")
	})

	// Test case 2: Node without LingJun annotation should return false
	t.Run("Node without LingJun annotation", func(t *testing.T) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-normal-node",
			},
		}

		result := ISLingJunNode(node.Labels)
		require.False(t, result, "Expected false for node without LingJun annotation, got true")
	})
}

// TestPodSandboxExited tests the PodSandboxExited function
func TestPodSandboxExited(t *testing.T) {
	// Test case 1: Pod with empty container status should return false
	t.Run("Pod with empty container status", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-empty",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{},
			},
		}

		result := PodSandboxExited(pod)
		require.False(t, result, "Expected false for pod with empty container status, got true")
	})

	// Test case 2: Pod with container but no state should return false
	t.Run("Pod with container but no state", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-no-state",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "test-container",
					},
				},
			},
		}

		result := PodSandboxExited(pod)
		require.False(t, result, "Expected false for pod with container but no state, got true")
	})

	// Test case 3: Pod with running container should return false
	t.Run("Pod with running container", func(t *testing.T) {
		now := metav1.Now()
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-running",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "test-container",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: now,
							},
						},
					},
				},
			},
		}

		result := PodSandboxExited(pod)
		require.False(t, result, "Expected false for pod with running container, got true")
	})

	// Test case 4: Pod with terminated container should return true
	t.Run("Pod with terminated container", func(t *testing.T) {
		now := metav1.Now()
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-terminated",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodFailed,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "test-container",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								FinishedAt: now,
							},
						},
					},
				},
			},
		}

		result := PodSandboxExited(pod)
		require.True(t, result, "Expected true for pod with terminated container, got false")
	})

	// Test case 5: Pod with waiting container should return false
	t.Run("Pod with waiting container", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-waiting",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "test-container",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "ContainerCreating",
							},
						},
					},
				},
			},
		}

		result := PodSandboxExited(pod)
		require.False(t, result, "Expected false for pod with waiting container, got true")
	})
}

// TestPodInfoKey tests the PodInfoKey function
func TestPodInfoKey(t *testing.T) {
	// Test case: Valid pod should return correct key format
	t.Run("Valid pod", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "test-namespace",
			},
		}

		expected := "test-namespace/test-pod"
		result := PodInfoKey(pod.Namespace, pod.Name)
		require.Equal(t, expected, result, "Expected %s, got %s", expected, result)
	})
}

func TestEventName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal case",
			input:    "test-event",
			expected: "terway-controlplane/test-event",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "terway-controlplane/",
		},
		{
			name:     "with special characters",
			input:    "event-with-dash",
			expected: "terway-controlplane/event-with-dash",
		},
		{
			name:     "with numbers",
			input:    "event123",
			expected: "terway-controlplane/event123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EventName(tt.input)
			if result != tt.expected {
				t.Errorf("EventName(%s) = %s; expected %s", tt.input, result, tt.expected)
			}
		})
	}
}
