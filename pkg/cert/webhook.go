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
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/pkg/utils"
)

var log = ctrl.Log.WithName("webhook-cert")

const (
	serverCertKey = "tls.crt"
	serverKeyKey  = "tls.key"

	caCertKey = "ca.crt"
)

// SyncCert sync cert for webhook
func SyncCert(ctx context.Context, c client.Client, ns, name, domain, certDir string) error {
	secretName := fmt.Sprintf("%s-webhook-cert", name)
	// check secret
	var serverCertBytes, serverKeyBytes, caCertBytes []byte

	// get cert from secret or generate it
	existSecret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: secretName}, existSecret)
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("error get cert from secret, %w", err)
		}
		// create certs
		s, err := GenerateCerts(ns, name, domain)
		if err != nil {
			return fmt.Errorf("error generate cert, %w", err)
		}

		serverCertBytes = s.Data[serverCertKey]
		serverKeyBytes = s.Data[serverKeyKey]
		caCertBytes = s.Data[caCertKey]
		s.Name = secretName
		s.Namespace = ns
		// create secret this make sure one is the leader
		err = c.Create(ctx, s)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("error create cert to secret, %w", err)
			}

			secret := &corev1.Secret{}
			err = c.Get(ctx, types.NamespacedName{Namespace: ns, Name: secretName}, secret)
			if err != nil {
				return fmt.Errorf("error get cert from secret, %w", err)
			}
			serverCertBytes = secret.Data[serverCertKey]
			serverKeyBytes = secret.Data[serverKeyKey]
			caCertBytes = secret.Data[caCertKey]
		}
	} else {
		serverCertBytes = existSecret.Data[serverCertKey]
		serverKeyBytes = existSecret.Data[serverKeyKey]
		caCertBytes = existSecret.Data[caCertKey]
	}
	if len(serverCertBytes) == 0 || len(serverKeyBytes) == 0 || len(caCertBytes) == 0 {
		return fmt.Errorf("invalid cert")
	}

	err = os.MkdirAll(certDir, os.ModeDir)
	if err != nil {
		return err
	}

	// write cert to file
	err = os.WriteFile(filepath.Join(certDir, serverCertKey), serverCertBytes, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error create secret file, %w", err)
	}
	err = os.WriteFile(filepath.Join(certDir, serverKeyKey), serverKeyBytes, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error create secret file, %w", err)
	}
	err = os.WriteFile(filepath.Join(certDir, caCertKey), caCertBytes, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error create secret file, %w", err)
	}

	// update webhook
	mutatingWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err = c.Get(ctx, types.NamespacedName{Name: name}, mutatingWebhook)
	if err != nil {
		return err
	}
	if len(mutatingWebhook.Webhooks) == 0 {
		return fmt.Errorf("no webhook config found")
	}
	oldMutatingWebhook := mutatingWebhook.DeepCopy()
	changed := false
	for i, hook := range mutatingWebhook.Webhooks {
		if len(hook.ClientConfig.CABundle) != 0 {
			continue
		}
		changed = true
		// patch ca
		mutatingWebhook.Webhooks[i].ClientConfig.CABundle = caCertBytes
	}
	if changed {
		err = wait.ExponentialBackoffWithContext(ctx, utils.DefaultPatchBackoff, func(ctx context.Context) (done bool, err error) {
			innerErr := c.Patch(ctx, mutatingWebhook, client.StrategicMergeFrom(oldMutatingWebhook))
			if innerErr != nil {
				log.Error(innerErr, "error patch ca")
				return false, nil
			}
			return true, nil
		})

		if err != nil {
			return err
		}
		log.Info("update MutatingWebhook ca bundle success")
	}

	validateWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	err = c.Get(ctx, types.NamespacedName{Name: name}, validateWebhook)
	if err != nil {
		return err
	}
	if len(validateWebhook.Webhooks) == 0 {
		return fmt.Errorf("no webhook config found")
	}
	changed = false
	oldValidateWebhook := validateWebhook.DeepCopy()
	for i, hook := range validateWebhook.Webhooks {
		if len(hook.ClientConfig.CABundle) != 0 {
			continue
		}
		// patch ca
		validateWebhook.Webhooks[i].ClientConfig.CABundle = caCertBytes
		changed = true
	}
	if changed {
		err = wait.ExponentialBackoffWithContext(ctx, utils.DefaultPatchBackoff, func(ctx context.Context) (done bool, err error) {
			innerErr := c.Patch(ctx, validateWebhook, client.StrategicMergeFrom(oldValidateWebhook))
			if innerErr != nil {
				log.Error(innerErr, "error patch ca")
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		log.Info("update ValidatingWebhook ca bundle success")
	}

	return nil
}

func GenerateCerts(serviceNamespace, serviceName, clusterDomain string) (*corev1.Secret, error) {
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

	caPEM = new(bytes.Buffer)
	err = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})
	if err != nil {
		return nil, err
	}

	commonName := fmt.Sprintf("%s.%s.svc", serviceName, serviceNamespace)
	dnsNames := []string{serviceName,
		fmt.Sprintf("%s.%s", serviceName, serviceNamespace),
		commonName}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2021),
		DNSNames:     dnsNames,
		Subject: pkix.Name{
			CommonName:   commonName,
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
