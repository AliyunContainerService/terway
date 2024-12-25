package cert

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateCerts_ValidInput(t *testing.T) {
	cn := "test-service"
	clusterDomain := "cluster.local"
	dnsNames := []string{"test-service", "test-service.namespace", "test-service.namespace.svc"}

	secret, err := GenerateCerts(cn, clusterDomain, dnsNames)

	assert.NoError(t, err)
	assert.NotNil(t, secret)
	assert.Equal(t, corev1.SecretTypeTLS, secret.Type)
	assert.Contains(t, secret.Data, "ca.crt")
	assert.Contains(t, secret.Data, "tls.crt")
	assert.Contains(t, secret.Data, "tls.key")

	need := needRegenerate(secret.Data["tls.crt"], dnsNames)
	assert.False(t, need)
}

func TestGenerateCerts_EmptyDNSNames(t *testing.T) {
	cn := "test-service"
	clusterDomain := "cluster.local"
	dnsNames := []string{}

	secret, err := GenerateCerts(cn, clusterDomain, dnsNames)

	assert.Error(t, err)
	assert.Nil(t, secret)
}

func TestGenerateCerts_InvalidKeyGeneration(t *testing.T) {
	cn := "test-service"
	clusterDomain := "cluster.local"
	dnsNames := []string{"test-service", "test-service.namespace", "test-service.namespace.svc"}

	originalRandReader := rand.Reader
	defer func() { rand.Reader = originalRandReader }()
	rand.Reader = bytes.NewReader([]byte{})

	secret, err := GenerateCerts(cn, clusterDomain, dnsNames)

	assert.Error(t, err)
	assert.Nil(t, secret)
}

func TestWebhookUpdatedSuccessfully(t *testing.T) {
	ctx := context.TODO()
	oldObj := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
	}
	c := fake.NewClientBuilder().WithObjects(oldObj).Build()
	newObj := oldObj.DeepCopy()
	newObj.Webhooks = append(newObj.Webhooks, admissionregistrationv1.MutatingWebhook{})

	err := updateWebhook(ctx, c, oldObj, newObj)

	assert.NoError(t, err)
}

func TestWebhookNotUpdatedWhenEqual(t *testing.T) {
	ctx := context.TODO()
	c := fake.NewClientBuilder().Build()
	oldObj := &admissionregistrationv1.MutatingWebhookConfiguration{}
	newObj := oldObj.DeepCopy()

	err := updateWebhook(ctx, c, oldObj, newObj)

	assert.NoError(t, err)
}

func TestWebhookUpdatedWithCABundle(t *testing.T) {
	ctx := context.TODO()
	c := fake.NewClientBuilder().WithObjects(&admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}, &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}).Build()
	caCertBytes := []byte("new-ca-bundle")

	err := createOrUpdateWebhook(ctx, c, "test-webhook", "", caCertBytes)

	assert.NoError(t, err)

	updated := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err = c.Get(ctx, types.NamespacedName{Name: "test-webhook"}, updated)
	assert.NoError(t, err)

	assert.Equal(t, caCertBytes, updated.Webhooks[0].ClientConfig.CABundle)
}

func TestWebhookUpdatedWithURLEndpoint(t *testing.T) {
	ctx := context.TODO()
	c := fake.NewClientBuilder().WithObjects(&admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}, &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}).Build()
	caCertBytes := []byte("new-ca-bundle")
	urlEndpoint := "https://example.com/webhook"

	err := createOrUpdateWebhook(ctx, c, "test-webhook", urlEndpoint, caCertBytes)

	assert.NoError(t, err)

	updated := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err = c.Get(ctx, types.NamespacedName{Name: "test-webhook"}, updated)
	assert.NoError(t, err)

	assert.Equal(t, urlEndpoint, *updated.Webhooks[0].ClientConfig.URL)
}

func TestWebhookUpdateFailsWhenNoWebhookConfig(t *testing.T) {
	ctx := context.TODO()
	c := fake.NewClientBuilder().Build()
	caCertBytes := []byte("new-ca-bundle")

	err := createOrUpdateWebhook(ctx, c, "test-webhook", "", caCertBytes)

	assert.Error(t, err)
}

func TestWebhookUpdateFailsWhenNoWebhooks(t *testing.T) {
	ctx := context.TODO()
	c := fake.NewClientBuilder().WithObjects(&admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
	}, &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-webhook",
		},
	}).Build()
	caCertBytes := []byte("new-ca-bundle")

	err := createOrUpdateWebhook(ctx, c, "test-webhook", "", caCertBytes)

	assert.Error(t, err)
}

func TestCreateOrUpdateCertSuccessfully(t *testing.T) {
	ctx := context.TODO()
	c := fake.NewClientBuilder().Build()
	serviceNamespace := "default"
	serviceName := "test-service"
	clusterDomain := "cluster.local"
	clusterID := "test-cluster"
	urlMode := false

	secret, err := createOrUpdateCert(ctx, c, serviceNamespace, serviceName, clusterDomain, clusterID, urlMode)

	assert.NoError(t, err)
	assert.NotNil(t, secret)
	assert.Equal(t, fmt.Sprintf("%s-webhook-cert", serviceName), secret.Name)
	assert.Equal(t, serviceNamespace, secret.Namespace)
	assert.Contains(t, secret.Data, serverCertKey)
	assert.Contains(t, secret.Data, serverKeyKey)
	assert.Contains(t, secret.Data, caCertKey)
}

func TestCreateOrUpdateCertWithURLMode(t *testing.T) {
	ctx := context.TODO()
	c := fake.NewClientBuilder().Build()
	serviceNamespace := "default"
	serviceName := "test-service"
	clusterDomain := "cluster.local"
	clusterID := "test-cluster"
	urlMode := true

	secret, err := createOrUpdateCert(ctx, c, serviceNamespace, serviceName, clusterDomain, clusterID, urlMode)

	assert.NoError(t, err)
	assert.NotNil(t, secret)
	assert.Equal(t, fmt.Sprintf("%s-webhook-cert", serviceName), secret.Name)
	assert.Equal(t, serviceNamespace, secret.Namespace)
	assert.Contains(t, secret.Data, serverCertKey)
	assert.Contains(t, secret.Data, serverKeyKey)
	assert.Contains(t, secret.Data, caCertKey)
}

func TestCreateOrUpdateCertFailsWhenClusterIDMissing(t *testing.T) {
	ctx := context.TODO()
	c := fake.NewClientBuilder().Build()
	serviceNamespace := "default"
	serviceName := "test-service"
	clusterDomain := "cluster.local"
	clusterID := ""
	urlMode := true

	secret, err := createOrUpdateCert(ctx, c, serviceNamespace, serviceName, clusterDomain, clusterID, urlMode)

	assert.Error(t, err)
	assert.Nil(t, secret)
}
