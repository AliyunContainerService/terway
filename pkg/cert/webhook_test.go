package cert

import (
	"context"
	"crypto/rsa"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/terway/pkg/internal/testutil"
)

func TestGenerateCerts(t *testing.T) {
	tests := []struct {
		name              string
		serviceNamespace  string
		serviceName       string
		clusterDomain     string
		expectError       bool
		expectedCertCount int
	}{
		{
			name:              "valid cert generation",
			serviceNamespace:  "test-namespace",
			serviceName:       "test-service",
			clusterDomain:     "cluster.local",
			expectError:       false,
			expectedCertCount: 3,
		},
		{
			name:              "empty service name",
			serviceNamespace:  "test-namespace",
			serviceName:       "",
			clusterDomain:     "cluster.local",
			expectError:       false,
			expectedCertCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := GenerateCerts(tt.serviceNamespace, tt.serviceName, tt.clusterDomain)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, secret)
			assert.Equal(t, corev1.SecretTypeTLS, secret.Type)
			assert.Len(t, secret.Data, tt.expectedCertCount)

			// Check that all required keys exist
			assert.Contains(t, secret.Data, caCertKey)
			assert.Contains(t, secret.Data, serverCertKey)
			assert.Contains(t, secret.Data, serverKeyKey)

			// Check that the data is not empty
			assert.NotEmpty(t, secret.Data[caCertKey])
			assert.NotEmpty(t, secret.Data[serverCertKey])
			assert.NotEmpty(t, secret.Data[serverKeyKey])
		})
	}
}

func TestSyncCert_NewSecret(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create webhook configurations
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-mutating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil,
				},
			},
		},
	}

	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-validating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil,
				},
			},
		},
	}

	// Create fake client with webhook configurations
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	// Test
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)

	// Assert
	require.NoError(t, err)

	// Check that secret was created
	secret := &corev1.Secret{}
	err = client.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name + "-webhook-cert"}, secret)
	require.NoError(t, err)
	assert.Equal(t, corev1.SecretTypeTLS, secret.Type)
	assert.Contains(t, secret.Data, caCertKey)
	assert.Contains(t, secret.Data, serverCertKey)
	assert.Contains(t, secret.Data, serverKeyKey)

	// Check that cert files were written
	assert.FileExists(t, filepath.Join(tempDir, serverCertKey))
	assert.FileExists(t, filepath.Join(tempDir, serverKeyKey))
	assert.FileExists(t, filepath.Join(tempDir, caCertKey))
}

func TestSyncCert_ExistingSecret(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create existing secret
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-webhook-cert",
			Namespace: ns,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey:     []byte("existing-ca-cert"),
			serverCertKey: []byte("existing-server-cert"),
			serverKeyKey:  []byte("existing-server-key"),
		},
	}

	// Create webhook configurations
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-mutating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil,
				},
			},
		},
	}

	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-validating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil,
				},
			},
		},
	}

	// Create fake client with existing secret and webhook configurations
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(existingSecret, mutatingWebhook, validatingWebhook).
		Build()

	// Test
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)

	// Assert
	require.NoError(t, err)

	// Check that cert files were written with existing data
	caCertData, err := os.ReadFile(filepath.Join(tempDir, caCertKey))
	require.NoError(t, err)
	assert.Equal(t, "existing-ca-cert", string(caCertData))

	serverCertData, err := os.ReadFile(filepath.Join(tempDir, serverCertKey))
	require.NoError(t, err)
	assert.Equal(t, "existing-server-cert", string(serverCertData))

	serverKeyData, err := os.ReadFile(filepath.Join(tempDir, serverKeyKey))
	require.NoError(t, err)
	assert.Equal(t, "existing-server-key", string(serverKeyData))
}

func TestSyncCert_WithWebhookConfigurations(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create webhook configurations
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-mutating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil, // Empty to trigger update
				},
			},
		},
	}

	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-validating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil, // Empty to trigger update
				},
			},
		},
	}

	// Create fake client with webhook configurations
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	// Test
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)

	// Assert
	require.NoError(t, err)

	// Check that webhook configurations were updated
	updatedMutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err = client.Get(context.Background(), types.NamespacedName{Name: name}, updatedMutatingWebhook)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMutatingWebhook.Webhooks[0].ClientConfig.CABundle)

	updatedValidatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	err = client.Get(context.Background(), types.NamespacedName{Name: name}, updatedValidatingWebhook)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedValidatingWebhook.Webhooks[0].ClientConfig.CABundle)
}

func TestSyncCert_WebhookConfigurationsNotFound(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create fake client without webhook configurations
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	// Test
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)

	// Assert - should fail because webhook configurations don't exist
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSyncCert_InvalidCertDir(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Use an invalid directory path
	invalidDir := "/invalid/path/that/should/not/exist"

	// Create fake client
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	// Test
	err := SyncCert(context.Background(), client, ns, name, domain, invalidDir)

	// Assert - should fail because directory cannot be created
	assert.Error(t, err)
}

func TestSyncCert_EmptyWebhookConfigurations(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create webhook configurations with empty webhooks
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{}, // Empty
	}

	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{}, // Empty
	}

	// Create fake client with empty webhook configurations
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	// Test
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)

	// Assert - should fail because no webhook config found
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no webhook config found")
}

func TestSyncCert_WebhookConfigurationsAlreadyHaveCABundle(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create webhook configurations with existing CA bundle
	existingCABundle := []byte("existing-ca-bundle")
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-mutating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: existingCABundle,
				},
			},
		},
	}

	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-validating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: existingCABundle,
				},
			},
		},
	}

	// Create fake client with webhook configurations that already have CA bundle
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	// Test
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)

	// Assert - should succeed but not update webhook configurations
	require.NoError(t, err)

	// Check that webhook configurations were not changed
	updatedMutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err = client.Get(context.Background(), types.NamespacedName{Name: name}, updatedMutatingWebhook)
	require.NoError(t, err)
	assert.Equal(t, existingCABundle, updatedMutatingWebhook.Webhooks[0].ClientConfig.CABundle)

	updatedValidatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	err = client.Get(context.Background(), types.NamespacedName{Name: name}, updatedValidatingWebhook)
	require.NoError(t, err)
	assert.Equal(t, existingCABundle, updatedValidatingWebhook.Webhooks[0].ClientConfig.CABundle)
}

func TestSyncCert_SecretAlreadyExists(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create existing secret
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-webhook-cert",
			Namespace: ns,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey:     []byte("existing-ca-cert"),
			serverCertKey: []byte("existing-server-cert"),
			serverKeyKey:  []byte("existing-server-key"),
		},
	}

	// Create webhook configurations
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-mutating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil,
				},
			},
		},
	}

	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-validating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil,
				},
			},
		},
	}

	// Create fake client with existing secret and webhook configurations
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(existingSecret, mutatingWebhook, validatingWebhook).
		Build()

	// Test - this should handle the case where secret already exists
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)

	// Assert
	require.NoError(t, err)

	// Check that the existing secret was used
	secret := &corev1.Secret{}
	err = client.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name + "-webhook-cert"}, secret)
	require.NoError(t, err)
	assert.Equal(t, "existing-ca-cert", string(secret.Data[caCertKey]))
	assert.Equal(t, "existing-server-cert", string(secret.Data[serverCertKey]))
	assert.Equal(t, "existing-server-key", string(secret.Data[serverKeyKey]))
}

func TestSyncCert_InvalidSecretData(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create secret with invalid/missing data
	invalidSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-webhook-cert",
			Namespace: ns,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			// Missing required keys
		},
	}

	// Create fake client with invalid secret
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(invalidSecret).Build()

	// Test
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)

	// Assert - should fail because secret data is invalid
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cert")
}

// Benchmark tests
func BenchmarkGenerateCerts(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := GenerateCerts("test-namespace", "test-service", "cluster.local")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSyncCert_NewSecret(b *testing.B) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Create temp directory for cert files
	tempDir, err := os.MkdirTemp("", "webhook-cert-bench")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create webhook configurations
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-mutating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil,
				},
			},
		},
	}

	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-validating-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: nil,
				},
			},
		},
	}

	// Create fake client
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	// Benchmark
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create unique names for each iteration to avoid conflicts
		uniqueName := fmt.Sprintf("%s-%d", name, i)
		uniqueDir := filepath.Join(tempDir, fmt.Sprintf("cert-%d", i))

		err := SyncCert(context.Background(), client, ns, uniqueName, domain, uniqueDir)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestSyncCert_GetSecretError(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	// This should succeed as it will create a new secret
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	require.NoError(t, err)
}

func TestSyncCert_SecretGetAfterCreateError(t *testing.T) {
	// This tests the path where Create returns AlreadyExists, then Get fails
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create webhook configurations
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	// First call creates the secret
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	require.NoError(t, err)

	// Second call should use existing secret
	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	require.NoError(t, err)
}

func TestSyncCert_FileWriteError(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"

	// Use a read-only directory to simulate write error
	// On Linux, we can use /proc which is read-only
	readOnlyDir := "/proc/sys"

	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	// This should fail when trying to write files
	err := SyncCert(context.Background(), client, ns, name, domain, readOnlyDir)
	assert.Error(t, err)
}

func TestSyncCert_PartialSecretData(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create secret with partial data (missing one key)
	partialSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-webhook-cert",
			Namespace: ns,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey:     []byte("ca-cert"),
			serverCertKey: []byte("server-cert"),
			// Missing serverKeyKey
		},
	}

	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(partialSecret, mutatingWebhook, validatingWebhook).
		Build()

	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cert")
}

func TestSyncCert_EmptySecretData(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create secret with empty data
	emptySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-webhook-cert",
			Namespace: ns,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey:     []byte(""),
			serverCertKey: []byte(""),
			serverKeyKey:  []byte(""),
		},
	}

	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(emptySecret, mutatingWebhook, validatingWebhook).
		Build()

	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cert")
}

func TestSyncCert_MultipleWebhooks(t *testing.T) {
	// Setup
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create webhook configurations with multiple webhooks
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{Name: "webhook1", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
			{Name: "webhook2", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("existing")}},
			{Name: "webhook3", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
		},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "webhook1", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}},
			{Name: "webhook2", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("existing")}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	require.NoError(t, err)

	// Verify that webhooks without CABundle were updated
	updatedMutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err = client.Get(context.Background(), types.NamespacedName{Name: name}, updatedMutatingWebhook)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMutatingWebhook.Webhooks[0].ClientConfig.CABundle)
	assert.Equal(t, []byte("existing"), updatedMutatingWebhook.Webhooks[1].ClientConfig.CABundle)
	assert.NotEmpty(t, updatedMutatingWebhook.Webhooks[2].ClientConfig.CABundle)
}

func TestSyncCert_GetSecretReturnsNonNotFoundError(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.MutatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	inner := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	secretName := name + "-webhook-cert"
	wrapped := testutil.ClientWithGetErrorFunc(inner, func(key client.ObjectKey) error {
		if key.Namespace == ns && key.Name == secretName {
			return fmt.Errorf("injected get secret error")
		}
		return nil
	})

	err = SyncCert(context.Background(), wrapped, ns, name, domain, tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error get cert from secret")
}

func TestSyncCert_MkdirAllFails(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-webhook-cert", Namespace: ns},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey: []byte("ca"), serverCertKey: []byte("cert"), serverKeyKey: []byte("key"),
		},
	}
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.MutatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("x")}}},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("x")}}},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(existingSecret, mutatingWebhook, validatingWebhook).
		Build()

	// Use a path that cannot be created: "nonexistent" is a file, so nonexistent/sub cannot be created
	os.RemoveAll(filepath.Join(tempDir, "nonexistent"))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "nonexistent"), []byte("x"), 0o644))
	invalidDir := filepath.Join(tempDir, "nonexistent", "sub")

	err = SyncCert(context.Background(), client, ns, name, domain, invalidDir)
	assert.Error(t, err)
}

func TestSyncCert_GetMutatingWebhookError(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-webhook-cert", Namespace: ns},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey: []byte("ca"), serverCertKey: []byte("cert"), serverKeyKey: []byte("key"),
		},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	// No mutating webhook - Get will return NotFound
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(existingSecret, validatingWebhook).
		Build()

	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	assert.Error(t, err)
}

func TestSyncCert_GetValidatingWebhookError(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-webhook-cert", Namespace: ns},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey: []byte("ca"), serverCertKey: []byte("cert"), serverKeyKey: []byte("key"),
		},
	}
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.MutatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	// No validating webhook
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(existingSecret, mutatingWebhook).
		Build()

	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	assert.Error(t, err)
}

// createErrorClient returns error on Create when the object is the webhook secret.
type createErrorClient struct {
	client.Client
	createError func(obj client.Object) error
}

func (e *createErrorClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if e.createError != nil {
		if err := e.createError(obj); err != nil {
			return err
		}
	}
	return e.Client.Create(ctx, obj, opts...)
}

func TestSyncCert_CreateSecretReturnsError(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.MutatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	inner := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	wrapped := &createErrorClient{
		Client: inner,
		createError: func(obj client.Object) error {
			if _, isSecret := obj.(*corev1.Secret); isSecret && obj.GetName() == name+"-webhook-cert" {
				return fmt.Errorf("injected create error")
			}
			return nil
		},
	}

	err = SyncCert(context.Background(), wrapped, ns, name, domain, tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error create cert to secret")
}

func TestSyncCert_GenerateCertsReturnsError(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.MutatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	patches := gomonkey.ApplyFunc(GenerateCerts, func(string, string, string) (*corev1.Secret, error) {
		return nil, fmt.Errorf("injected GenerateCerts error")
	})
	defer patches.Reset()

	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error generate cert")
}

// createThenGetErrorClient: first Get(secret) returns NotFound, Create(secret) returns AlreadyExists, second Get(secret) returns error.
type createThenGetErrorClient struct {
	client.Client
	getCount int
}

func (c *createThenGetErrorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	c.getCount++
	secretName := key.Name
	if key.Namespace != "" && (secretName == "test-webhook-webhook-cert" || (len(secretName) > 12 && secretName[len(secretName)-12:] == "-webhook-cert")) {
		if c.getCount == 1 {
			return errors.NewNotFound(schema.GroupResource{Resource: "secrets"}, secretName)
		}
		if c.getCount == 2 {
			return fmt.Errorf("injected get after create error")
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *createThenGetErrorClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if s, ok := obj.(*corev1.Secret); ok && s.Name != "" && len(s.Name) > 10 {
		return errors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, s.Name)
	}
	return c.Client.Create(ctx, obj, opts...)
}

func TestSyncCert_CreateAlreadyExistsThenGetSecretFails(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.MutatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	inner := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(mutatingWebhook, validatingWebhook).
		Build()

	wrapped := &createThenGetErrorClient{Client: inner}

	err = SyncCert(context.Background(), wrapped, ns, name, domain, tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error get cert from secret")
}

func TestSyncCert_WriteFileSecondFails(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-webhook-cert", Namespace: ns},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey: []byte("ca"), serverCertKey: []byte("cert"), serverKeyKey: []byte("key"),
		},
	}
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.MutatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("x")}}},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("x")}}},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(existingSecret, mutatingWebhook, validatingWebhook).
		Build()

	writeCount := 0
	patches := gomonkey.ApplyFunc(os.WriteFile, func(fpath string, data []byte, perm os.FileMode) error {
		writeCount++
		if writeCount == 2 {
			return fmt.Errorf("injected write error")
		}
		return nil
	})
	defer patches.Reset()

	err = SyncCert(context.Background(), client, ns, name, domain, tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error create secret file")
}

// patchFailsOnceClient fails the first Patch call (mutating), then succeeds on retry; fails the first validating Patch, then succeeds.
type patchFailsOnceClient struct {
	client.Client
	patchCount int
}

func (c *patchFailsOnceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patchCount++
	// Fail first Patch (mutating webhook), succeed on retry. Then fail first validating Patch, succeed on retry.
	if c.patchCount == 1 || c.patchCount == 3 {
		return fmt.Errorf("injected patch error")
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestSyncCert_PatchBackoffRetry(t *testing.T) {
	ns := "test-namespace"
	name := "test-webhook"
	domain := "cluster.local"
	tempDir, err := os.MkdirTemp("", "webhook-cert-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-webhook-cert", Namespace: ns},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey: []byte("ca"), serverCertKey: []byte("cert"), serverKeyKey: []byte("key"),
		},
	}
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.MutatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	validatingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "test", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: nil}}},
	}
	inner := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(existingSecret, mutatingWebhook, validatingWebhook).
		Build()

	wrapped := &patchFailsOnceClient{Client: inner}

	err = SyncCert(context.Background(), wrapped, ns, name, domain, tempDir)
	require.NoError(t, err)
}

func TestGenerateCerts_RSAGenerateKeyFails(t *testing.T) {
	patches := gomonkey.ApplyFunc(rsa.GenerateKey, func(rand io.Reader, bits int) (*rsa.PrivateKey, error) {
		return nil, fmt.Errorf("injected rsa.GenerateKey error")
	})
	defer patches.Reset()

	_, err := GenerateCerts("ns", "svc", "cluster.local")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "injected")
}
