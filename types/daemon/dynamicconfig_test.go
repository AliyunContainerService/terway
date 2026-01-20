package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConfigFromConfigMap_OverrideScenarios(t *testing.T) {
	baseConfig := `{
		"version": "1",
		"max_pool_size": 10,
		"min_pool_size": 5,
		"ip_warm_up_size": 2,
		"vswitches": {"zone-a": ["vsw-a1"]},
		"eni_tags": {"tag1": "val1"},
		"security_group": "sg-base",
		"security_groups": ["sg-1", "sg-2"]
	}`

	tests := []struct {
		name      string
		topConfig string
		verify    func(t *testing.T, cfg *Config)
	}{
		{
			name:      "override MaxPoolSize with 0",
			topConfig: `{"max_pool_size": 0}`,
			verify: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 0, cfg.MaxPoolSize)
				assert.Equal(t, 5, cfg.MinPoolSize) // remains base
			},
		},
		{
			name:      "override MinPoolSize with 0",
			topConfig: `{"min_pool_size": 0}`,
			verify: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 0, cfg.MinPoolSize)
				assert.Equal(t, 10, cfg.MaxPoolSize) // remains base
			},
		},
		{
			name:      "override IPWarmUpSize with 0",
			topConfig: `{"ip_warm_up_size": 0}`,
			verify: func(t *testing.T, cfg *Config) {
				assert.NotNil(t, cfg.IPWarmUpSize)
				assert.Equal(t, 0, *cfg.IPWarmUpSize)
			},
		},
		{
			name:      "override SecurityGroup with empty string",
			topConfig: `{"security_group": ""}`,
			verify: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "", cfg.SecurityGroup)
			},
		},
		{
			name:      "override SecurityGroups with empty list",
			topConfig: `{"security_groups": []}`,
			verify: func(t *testing.T, cfg *Config) {
				assert.Empty(t, cfg.SecurityGroups)
			},
		},
		{
			name:      "override VSwitches (map merge behavior check)",
			topConfig: `{"vswitches": {"zone-b": ["vsw-b1"]}}`,
			verify: func(t *testing.T, cfg *Config) {
				assert.Contains(t, cfg.VSwitches, "zone-b")
				assert.Contains(t, cfg.VSwitches, "zone-a")
			},
		},
		{
			name:      "override VSwitches (map merge behavior check) 2",
			topConfig: `{"vswitches": {"zone-b": ["vsw-b1"],"zone-a":null}}`,
			verify: func(t *testing.T, cfg *Config) {
				assert.Contains(t, cfg.VSwitches, "zone-b")
				assert.NotContains(t, cfg.VSwitches, "zone-a")
			},
		},
		{
			name:      "override ENITags (map merge behavior check)",
			topConfig: `{"eni_tags": {"tag2": "val2"}}`,
			verify: func(t *testing.T, cfg *Config) {
				assert.Contains(t, cfg.ENITags, "tag2")
				assert.Contains(t, cfg.ENITags, "tag1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewFakeClient(
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "eni-config",
						Namespace: "kube-system",
					},
					Data: map[string]string{"eni_conf": baseConfig},
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
					Data: map[string]string{"eni_conf": tt.topConfig},
				},
			)

			cfg, err := ConfigFromConfigMap(context.Background(), client, "node-1")
			assert.NoError(t, err)
			tt.verify(t, cfg)
		})
	}
}

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
