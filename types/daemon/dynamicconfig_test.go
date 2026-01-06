package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConfigFromConfigMapReturnsErrorWhenBaseConfigMapNotFound(t *testing.T) {
	client := fake.NewFakeClient()
	_, err := ConfigFromConfigMap(context.Background(), client, "")
	assert.Error(t, err)
}

func TestConfigFromConfigMapReturnsErrorWhenBaseConfigIsEmpty(t *testing.T) {
	client := fake.NewFakeClient(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "eni-config",
			Namespace: "kube-system",
		},
		Data: map[string]string{"eni_conf": ""},
	})
	_, err := ConfigFromConfigMap(context.Background(), client, "")
	assert.Error(t, err)
}

func TestConfigFromConfigMapReturnsConfigWhenNodeNameIsNotEmpty(t *testing.T) {
	client := fake.NewFakeClient(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "eni-config",
				Namespace: "kube-system",
			},
			Data: map[string]string{"eni_conf": `{"version": "1"}`},
		},
	)
	cfg, err := ConfigFromConfigMap(context.Background(), client, "node-1")
	assert.NoError(t, err)
	assert.Equal(t, "1", cfg.Version)
}

func TestConfigFromConfigMapReturnsErrorWhenSecurityGroupsExceedLimit(t *testing.T) {
	client := fake.NewFakeClient(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "eni-config",
			Namespace: "kube-system",
		},
		Data: map[string]string{"eni_conf": `{"security_groups": ["sg-1", "sg-2", "sg-3", "sg-4", "sg-5", "sg-6","sg-7","sg-8","sg-9","sg-10","sg-11"]}`},
	})
	_, err := ConfigFromConfigMap(context.Background(), client, "")
	assert.Error(t, err)
}

func TestGetConfigReturnsNilInitially(t *testing.T) {
	// Reset config to nil before test
	SetConfig(nil)
	cfg := GetConfig()
	assert.Nil(t, cfg)
}

func TestSetConfigAndGetConfig(t *testing.T) {
	// Reset config to nil before test
	SetConfig(nil)

	// Test setting and getting config
	testConfig := &Config{
		Version: "test-version",
	}
	SetConfig(testConfig)

	cfg := GetConfig()
	assert.NotNil(t, cfg)
	assert.Equal(t, "test-version", cfg.Version)

	// Test updating config
	updatedConfig := &Config{
		Version: "updated-version",
	}
	SetConfig(updatedConfig)

	cfg = GetConfig()
	assert.NotNil(t, cfg)
	assert.Equal(t, "updated-version", cfg.Version)
}

func TestConfigFromConfigMapWithDynamicConfigMap(t *testing.T) {
	// Test case: node has terway-config label and the corresponding ConfigMap exists
	client := fake.NewFakeClient(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "eni-config",
				Namespace: "kube-system",
			},
			Data: map[string]string{"eni_conf": `{"version": "base"}`},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					"terway-config": "node-1-config",
				},
			},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node-1-config",
				Namespace: "kube-system",
			},
			Data: map[string]string{"eni_conf": `{"version": "dynamic"}`},
		},
	)

	cfg, err := ConfigFromConfigMap(context.Background(), client, "node-1")
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, "dynamic", cfg.Version)
}

func TestConfigFromConfigMapWithDynamicConfigMapNotFound(t *testing.T) {
	// Test case: node has terway-config label but the corresponding ConfigMap doesn't exist
	client := fake.NewFakeClient(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "eni-config",
				Namespace: "kube-system",
			},
			Data: map[string]string{"eni_conf": `{"version": "base"}`},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					"terway-config": "non-existent-config",
				},
			},
		},
	)

	_, err := ConfigFromConfigMap(context.Background(), client, "node-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error get configmap")
}

func TestConfigFromConfigMapWithNodeWithoutLabel(t *testing.T) {
	// Test case: node exists but doesn't have terway-config label (cmName == "")
	client := fake.NewFakeClient(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "eni-config",
				Namespace: "kube-system",
			},
			Data: map[string]string{"eni_conf": `{"version": "base"}`},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-1",
				Labels: map[string]string{},
			},
		},
	)

	cfg, err := ConfigFromConfigMap(context.Background(), client, "node-1")
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, "base", cfg.Version)
}

func TestConfigFromConfigMapWithNodeNotFound(t *testing.T) {
	// Test case: node doesn't exist (cmName == "")
	client := fake.NewFakeClient(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "eni-config",
				Namespace: "kube-system",
			},
			Data: map[string]string{"eni_conf": `{"version": "base"}`},
		},
	)

	cfg, err := ConfigFromConfigMap(context.Background(), client, "non-existent-node")
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, "base", cfg.Version)
}
