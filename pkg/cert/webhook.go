package cert

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

var log = ctrl.Log.WithName("webhook-cert")

const (
	serverCertKey = "tls.crt"
	serverKeyKey  = "tls.key"

	caCertKey = "ca.crt"
)

// SyncCert sync cert for webhook
func SyncCert(ctx context.Context, c client.Client, serviceNamespace, serviceName, clusterDomain, certDir, clusterID string, urlMode bool) error {
	// check secret
	var caCertBytes []byte

	var secret *corev1.Secret
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		secret, err = createOrUpdateCert(ctx, c, serviceNamespace, serviceName, clusterDomain, clusterID, urlMode)
		return err
	})
	if err != nil {
		return err
	}
	caCertBytes = secret.Data[caCertKey]

	urlEndpoint := ""
	if urlMode {
		urlEndpoint = fmt.Sprintf("https://%s.%s.svc.cluster.local./mutating", serviceName, clusterID)
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return createOrUpdateWebhook(ctx, c, serviceName, urlEndpoint, caCertBytes)
	})
	if err != nil {
		return err
	}

	err = os.MkdirAll(certDir, os.ModeDir)
	if err != nil {
		return err
	}

	// write cert to file
	err = os.WriteFile(filepath.Join(certDir, serverCertKey), secret.Data[serverCertKey], os.ModePerm)
	if err != nil {
		return fmt.Errorf("error create secret file, %w", err)
	}
	err = os.WriteFile(filepath.Join(certDir, serverKeyKey), secret.Data[serverKeyKey], os.ModePerm)
	if err != nil {
		return fmt.Errorf("error create secret file, %w", err)
	}
	err = os.WriteFile(filepath.Join(certDir, caCertKey), caCertBytes, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error create secret file, %w", err)
	}
	return nil
}

func updateWebhook(ctx context.Context, c client.Client, oldObj, newObj client.Object) error {
	if !reflect.DeepEqual(oldObj, newObj) {
		err := c.Update(ctx, newObj)
		if err != nil {
			return err
		}
		log.Info("update webhook success")
	}
	return nil
}

func createOrUpdateCert(ctx context.Context, c client.Client, serviceNamespace, serviceName, clusterDomain, clusterID string, urlMode bool) (*corev1.Secret, error) {
	secretName := fmt.Sprintf("%s-webhook-cert", serviceName)
	cn := fmt.Sprintf("%s.%s.svc", serviceName, serviceNamespace)
	dnsNames := []string{
		serviceName,
		fmt.Sprintf("%s.%s", serviceName, serviceNamespace),
		fmt.Sprintf("%s.%s.svc", serviceName, serviceNamespace),
	}
	if urlMode {
		if clusterID == "" {
			return nil, fmt.Errorf("clusterID is required in urlMode")
		}
		dnsNames = append(dnsNames,
			fmt.Sprintf("%s.%s", serviceName, clusterID),
			fmt.Sprintf("%s.%s.svc", serviceName, clusterID),
			fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, clusterID),
		)
	}

	// check secret
	var serverCertBytes, serverKeyBytes, caCertBytes []byte

	// get cert from secret or generate it
	existSecret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Namespace: serviceNamespace, Name: secretName}, existSecret)
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, fmt.Errorf("error get cert from secret, %w", err)
		}
	}

	if needRegenerate(existSecret.Data[serverCertKey], dnsNames) {
		// create certs
		s, err := GenerateCerts(cn, clusterDomain, dnsNames)
		if err != nil {
			return nil, fmt.Errorf("error generate cert, %w", err)
		}

		s.Name = secretName
		s.Namespace = serviceNamespace

		update := s.DeepCopy()
		result, err := controllerutil.CreateOrUpdate(ctx, c, update, func() error {
			update.Data = s.Data
			return nil
		})
		if err != nil {
			return nil, err
		}
		existSecret = update
		log.Info("update secret", "result", result)
	}

	serverCertBytes = existSecret.Data[serverCertKey]
	serverKeyBytes = existSecret.Data[serverKeyKey]
	caCertBytes = existSecret.Data[caCertKey]

	if len(serverCertBytes) == 0 || len(serverKeyBytes) == 0 || len(caCertBytes) == 0 {
		return nil, fmt.Errorf("invalid cert")
	}

	return existSecret, nil
}

func createOrUpdateWebhook(ctx context.Context, c client.Client, hookName string, urlEndpoint string, caCertBytes []byte) error {
	// update webhook
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err := c.Get(ctx, types.NamespacedName{Name: hookName}, mutatingWebhook)
	if err != nil {
		return err
	}
	if len(mutatingWebhook.Webhooks) == 0 {
		return fmt.Errorf("no webhook config found")
	}

	validateWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	err = c.Get(ctx, types.NamespacedName{Name: hookName}, validateWebhook)
	if err != nil {
		return err
	}
	if len(validateWebhook.Webhooks) == 0 {
		return fmt.Errorf("no webhook config found")
	}

	oldMutatingWebhook := mutatingWebhook.DeepCopy()
	oldValidateWebhook := validateWebhook.DeepCopy()

	for i, _ := range mutatingWebhook.Webhooks {
		if string(mutatingWebhook.Webhooks[i].ClientConfig.CABundle) != string(caCertBytes) {
			mutatingWebhook.Webhooks[i].ClientConfig.CABundle = caCertBytes
		}
		if urlEndpoint != "" {
			mutatingWebhook.Webhooks[i].ClientConfig.Service = nil
			mutatingWebhook.Webhooks[i].ClientConfig.URL = &urlEndpoint
		}
	}

	for i, _ := range validateWebhook.Webhooks {
		if string(validateWebhook.Webhooks[i].ClientConfig.CABundle) != string(caCertBytes) {
			validateWebhook.Webhooks[i].ClientConfig.CABundle = caCertBytes
		}
		if urlEndpoint != "" {
			validateWebhook.Webhooks[i].ClientConfig.Service = nil
			validateWebhook.Webhooks[i].ClientConfig.URL = &urlEndpoint
		}
	}

	err = updateWebhook(ctx, c, oldMutatingWebhook, mutatingWebhook)
	if err != nil {
		return err
	}

	err = updateWebhook(ctx, c, oldValidateWebhook, validateWebhook)
	if err != nil {
		return err
	}
	return nil
}

// needRegenerate check exist cert with dnsNames,
func needRegenerate(certPEM []byte, dnsNames []string) bool {
	if certPEM == nil {
		return true
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		log.Info("failed to decode PEM block containing the certificate")
		return true
	}
	// Parse the certificate
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Error(err, "error parse cert")
		return true
	}
	oldSet := sets.New[string](cert.DNSNames...)
	newSet := sets.New[string](dnsNames...)
	return !oldSet.Equal(newSet)
}

func GenerateCerts(cn, clusterDomain string, dnsNames []string) (*corev1.Secret, error) {
	var caPEM, serverCertPEM, serverPrivateKeyPEM *bytes.Buffer
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2021),
		Subject: pkix.Name{
			Organization: []string{clusterDomain},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(100, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPK, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		return nil, err
	}

	caBytes, err := x509.CreateCertificate(cryptorand.Reader, ca, ca, &caPK.PublicKey, caPK)
	if err != nil {
		return nil, err
	}

	if len(dnsNames) == 0 {
		return nil, fmt.Errorf("dnsNames is empty")
	}
	caPEM = new(bytes.Buffer)
	err = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})
	if err != nil {
		return nil, err
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2021),
		DNSNames:     dnsNames,
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{clusterDomain},
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().AddDate(100, 0, 0),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature,
	}

	serverPrivateKey, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		return nil, err
	}

	serverCertBytes, err := x509.CreateCertificate(cryptorand.Reader, cert, ca, &serverPrivateKey.PublicKey, caPK)
	if err != nil {
		return nil, err
	}

	serverCertPEM = new(bytes.Buffer)
	err = pem.Encode(serverCertPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertBytes,
	})
	if err != nil {
		return nil, err
	}

	serverPrivateKeyPEM = new(bytes.Buffer)
	err = pem.Encode(serverPrivateKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverPrivateKey),
	})
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			caCertKey:     caPEM.Bytes(),
			serverCertKey: serverCertPEM.Bytes(),
			serverKeyKey:  serverPrivateKeyPEM.Bytes(),
		}}, nil
}
