package cert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
