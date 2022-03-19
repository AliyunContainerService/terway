package cert

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/utils"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

var log = logger.DefaultLogger.WithField("subSys", "webhook-cert")

const (
	serverCertKey = "tls.crt"
	serverKeyKey  = "tls.key"

	caCertKey = "ca.crt"
)

// SyncCert sync cert for webhook
func SyncCert(ns, name, domain, certDir string) error {
	secretName := fmt.Sprintf("%s-webhook-cert", name)
	cs := utils.K8sClient
	// check secret
	var serverCertBytes, serverKeyBytes, caCertBytes []byte

	// get cert from secret or generate it
	existSecret, err := cs.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
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
		_, err = cs.CoreV1().Secrets(ns).Create(context.Background(), s, metav1.CreateOptions{})
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("error create cert to secret, %w", err)
			}
			secret, err := cs.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
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
	mutatingWebhook, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(mutatingWebhook.Webhooks) == 0 {
		return fmt.Errorf("no webhook config found")
	}
	mutatingCfg := mutatingWebhook.DeepCopy()
	changed := false
	for i, hook := range mutatingWebhook.Webhooks {
		if len(hook.ClientConfig.CABundle) != 0 {
			continue
		}
		changed = true
		// patch ca
		mutatingCfg.Webhooks[i].ClientConfig.CABundle = caCertBytes
	}
	if changed {
		mutatPatchBytes, err := json.Marshal(mutatingCfg)
		if err != nil {
			return err
		}
		err = wait.ExponentialBackoff(utils.DefaultPatchBackoff, func() (bool, error) {
			_, innerErr := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().Patch(context.Background(), mutatingWebhook.Name, types.StrategicMergePatchType, mutatPatchBytes, metav1.PatchOptions{})
			if innerErr != nil {
				log.Error(innerErr)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		log.Info("update MutatingWebhook ca bundle success")
	}

	validateWebhook, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(validateWebhook.Webhooks) == 0 {
		return fmt.Errorf("no webhook config found")
	}
	changed = false
	validateCfg := validateWebhook.DeepCopy()
	for i, hook := range validateWebhook.Webhooks {
		if len(hook.ClientConfig.CABundle) != 0 {
			continue
		}
		// patch ca
		validateCfg.Webhooks[i].ClientConfig.CABundle = caCertBytes
		changed = true
	}
	if changed {
		validatePatchBytes, err := json.Marshal(validateCfg)
		if err != nil {
			return err
		}
		err = wait.ExponentialBackoff(utils.DefaultPatchBackoff, func() (bool, error) {
			_, innerErr := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Patch(context.Background(), validateWebhook.Name, types.StrategicMergePatchType, validatePatchBytes, metav1.PatchOptions{})
			if innerErr != nil {
				log.Error(innerErr)
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
