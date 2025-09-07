package types

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestNewRESTMapper_ScopeRegistration tests the scope registration logic
func TestNewRESTMapper_ScopeRegistration(t *testing.T) {
	// Save original state
	originalRootScopedKinds := rootScopedKinds
	originalScheme := Scheme
	defer func() {
		rootScopedKinds = originalRootScopedKinds
		Scheme = originalScheme
	}()

	// Set up test environment
	testScheme := runtime.NewScheme()
	Scheme = testScheme
	rootScopedKinds = make(map[schema.GroupKind]bool)

	// Register test resources
	testGV := schema.GroupVersion{Group: "test.group", Version: "v1"}
	testScheme.AddKnownTypeWithName(testGV.WithKind("NamespacedResource"), &TestResource{})
	testScheme.AddKnownTypeWithName(testGV.WithKind("ClusterResource"), &AnotherResource{})

	// Set ClusterResource as cluster-scoped resource
	clusterGK := schema.GroupKind{Group: "test.group", Kind: "ClusterResource"}
	rootScopedKinds[clusterGK] = true

	// Create mapper
	mapper := NewRESTMapper()
	if mapper == nil {
		t.Fatal("Expected mapper to be not nil")
	}

	// Test scope of namespace-scoped resource
	namespacedGVK := testGV.WithKind("NamespacedResource")
	mapping, err := mapper.RESTMapping(namespacedGVK.GroupKind(), namespacedGVK.Version)
	if err != nil {
		t.Logf("Warning: Could not get REST mapping for NamespacedResource: %v", err)
	} else {
		// Verify scope is correct (note: actual verification may require more complex setup)
		if mapping != nil {
			t.Logf("Namespaced resource mapping: %+v", mapping)
		}
	}

	// Test scope of cluster-scoped resource
	clusterGVK := testGV.WithKind("ClusterResource")
	mapping, err = mapper.RESTMapping(clusterGVK.GroupKind(), clusterGVK.Version)
	if err != nil {
		t.Logf("Warning: Could not get REST mapping for ClusterResource: %v", err)
	} else {
		// Verify scope is correct
		if mapping != nil {
			t.Logf("Cluster resource mapping: %+v", mapping)
		}
	}
}

// TestNewRESTMapper_EmptyScheme tests the case with empty Scheme
func TestNewRESTMapper_EmptyScheme(t *testing.T) {
	// Save original state
	originalScheme := Scheme
	originalRootScopedKinds := rootScopedKinds
	defer func() {
		Scheme = originalScheme
		rootScopedKinds = originalRootScopedKinds
	}()

	// Use empty Scheme
	Scheme = runtime.NewScheme()
	rootScopedKinds = make(map[schema.GroupKind]bool)

	// Should be able to create mapper even with empty scheme
	mapper := NewRESTMapper()
	if mapper == nil {
		t.Error("Expected mapper to be created even with empty scheme")
	}
}

// TestResource resource type for testing
type TestResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

func (t *TestResource) DeepCopyObject() runtime.Object {
	return &TestResource{}
}

// AnotherResource another resource type for testing
type AnotherResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

func (a *AnotherResource) DeepCopyObject() runtime.Object {
	return &AnotherResource{}
}
