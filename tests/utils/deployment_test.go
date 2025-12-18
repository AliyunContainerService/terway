package utils

import (
	"testing"
)

func TestNewDeployment(t *testing.T) {
	deploy := NewDeployment("test-deploy", "test-ns", 10)

	if deploy.Deployment == nil {
		t.Fatal("Deployment is nil")
	}
	if deploy.Name != "test-deploy" {
		t.Errorf("Name = %s, want test-deploy", deploy.Name)
	}
	if deploy.Namespace != "test-ns" {
		t.Errorf("Namespace = %s, want test-ns", deploy.Namespace)
	}
	if *deploy.Spec.Replicas != 10 {
		t.Errorf("Replicas = %d, want 10", *deploy.Spec.Replicas)
	}
}

func TestDeploymentWithNodeAffinity(t *testing.T) {
	deploy := NewDeployment("test-deploy", "test-ns", 1).
		WithNodeAffinity(map[string]string{
			"node-type": "test",
		})

	affinity := deploy.Spec.Template.Spec.Affinity
	if affinity == nil {
		t.Fatal("Affinity is nil")
	}
	if affinity.NodeAffinity == nil {
		t.Fatal("NodeAffinity is nil")
	}

	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) == 0 {
		t.Fatal("NodeSelectorTerms is empty")
	}

	found := false
	for _, expr := range terms[0].MatchExpressions {
		if expr.Key == "node-type" && len(expr.Values) > 0 && expr.Values[0] == "test" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected node affinity not found")
	}
}

func TestDeploymentWithNodeAffinityExclude(t *testing.T) {
	deploy := NewDeployment("test-deploy", "test-ns", 1).
		WithNodeAffinityExclude(map[string]string{
			"exclude-label": "value",
		})

	affinity := deploy.Spec.Template.Spec.Affinity
	if affinity == nil {
		t.Fatal("Affinity is nil")
	}
	if affinity.NodeAffinity == nil {
		t.Fatal("NodeAffinity is nil")
	}

	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) == 0 {
		t.Fatal("NodeSelectorTerms is empty")
	}

	// Should have at least 2 expressions: the exclude label and virtual-kubelet exclusion
	if len(terms[0].MatchExpressions) < 2 {
		t.Error("Expected at least 2 match expressions")
	}
}

func TestDeploymentWithLabels(t *testing.T) {
	deploy := NewDeployment("test-deploy", "test-ns", 1).
		WithLabels(map[string]string{
			"custom-label": "value",
		})

	if deploy.Spec.Template.Labels["custom-label"] != "value" {
		t.Errorf("Expected label custom-label=value, got %s", deploy.Spec.Template.Labels["custom-label"])
	}
}

func TestDeploymentWithAnnotations(t *testing.T) {
	deploy := NewDeployment("test-deploy", "test-ns", 1).
		WithAnnotations(map[string]string{
			"custom-annotation": "value",
		})

	if deploy.Spec.Template.Annotations["custom-annotation"] != "value" {
		t.Errorf("Expected annotation custom-annotation=value, got %s", deploy.Spec.Template.Annotations["custom-annotation"])
	}
}
