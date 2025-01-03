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
		Data: map[string]string{"eni_conf": `{"security_groups": ["sg-1", "sg-2", "sg-3", "sg-4", "sg-5", "sg-6"]}`},
	})
	_, err := ConfigFromConfigMap(context.Background(), client, "")
	assert.Error(t, err)
}
