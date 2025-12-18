package utils

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestLingjunToleration(t *testing.T) {
	toleration := LingjunToleration()

	if toleration.Key != LingjunTaintKey {
		t.Errorf("Expected key %s, got %s", LingjunTaintKey, toleration.Key)
	}
	if toleration.Operator != corev1.TolerationOpExists {
		t.Errorf("Expected operator %s, got %s", corev1.TolerationOpExists, toleration.Operator)
	}
}

func TestLingjunTolerations(t *testing.T) {
	tolerations := LingjunTolerations()

	if len(tolerations) != 1 {
		t.Fatalf("Expected 1 toleration, got %d", len(tolerations))
	}
	if tolerations[0].Key != LingjunTaintKey {
		t.Errorf("Expected key %s, got %s", LingjunTaintKey, tolerations[0].Key)
	}
}

func TestIsLingjunNodeType(t *testing.T) {
	tests := []struct {
		nodeType string
		expected bool
	}{
		{"lingjun-shared-eni", true},
		{"lingjun-exclusive-eni", true},
		{"ecs-shared-eni", false},
		{"ecs-exclusive-eni", false},
		{"", false},
		{"lingjun", false},
	}

	for _, tt := range tests {
		t.Run(tt.nodeType, func(t *testing.T) {
			result := IsLingjunNodeType(tt.nodeType)
			if result != tt.expected {
				t.Errorf("IsLingjunNodeType(%s) = %v, want %v", tt.nodeType, result, tt.expected)
			}
		})
	}
}

func TestDeploymentWithLingjunToleration(t *testing.T) {
	deploy := NewDeployment("test", "default", 1).
		WithLingjunToleration()

	tolerations := deploy.Spec.Template.Spec.Tolerations
	if len(tolerations) != 1 {
		t.Fatalf("Expected 1 toleration, got %d", len(tolerations))
	}

	found := false
	for _, tol := range tolerations {
		if tol.Key == LingjunTaintKey && tol.Operator == corev1.TolerationOpExists {
			found = true
			break
		}
	}
	if !found {
		t.Error("Lingjun toleration not found in deployment")
	}
}

