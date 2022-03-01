package credential

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/pkg/utils"
)

type EncryptedCredentialInfo struct {
	AccessKeyID     string `json:"access.key.id"`
	AccessKeySecret string `json:"access.key.secret"`
	SecurityToken   string `json:"security.token"`
	Expiration      string `json:"expiration"`
	Keyring         string `json:"keyring"`
}

type EncryptedCredentialProvider struct {
	credentialPath  string
	secretNamespace string
	secretName      string
}

// NewEncryptedCredentialProvider get token from file or secret. default filepath /var/addon/token-config
func NewEncryptedCredentialProvider(credentialPath, secretNamespace, secretName string) *EncryptedCredentialProvider {
	return &EncryptedCredentialProvider{credentialPath: credentialPath, secretNamespace: secretNamespace, secretName: secretName}
}

func (e *EncryptedCredentialProvider) Resolve() (*Credential, error) {
	if e.credentialPath == "" && e.secretNamespace == "" && e.secretName == "" {
		return nil, nil
	}
	var encodeTokenCfg []byte
	var err error
	var akInfo EncryptedCredentialInfo

	if e.credentialPath != "" {
		log.Infof("resolve encrypted credential %s", e.credentialPath)
		if utils.IsWindowsOS() {
			// NB(thxCode): since os.Stat has not worked as expected,
			// we use os.Lstat instead of os.Stat here,
			// ref to https://github.com/microsoft/Windows-Containers/issues/97#issuecomment-887713195.
			_, err = os.Lstat(e.credentialPath)
		} else {
			_, err = os.Stat(e.credentialPath)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read config %s, err: %w", e.credentialPath, err)
		}
		encodeTokenCfg, err = ioutil.ReadFile(e.credentialPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read token config, err: %w", err)
		}
	} else {
		log.Infof("resolve secret %s/%s", e.secretNamespace, e.secretName)

		secret, err := utils.K8sClient.CoreV1().Secrets(e.secretNamespace).Get(context.Background(), e.secretName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		var ok bool
		encodeTokenCfg, ok = secret.Data["addon.token.config"]
		if !ok {
			return nil, fmt.Errorf("token is not found in addon.network.token")
		}
	}

	err = json.Unmarshal(encodeTokenCfg, &akInfo)
	if err != nil {
		return nil, fmt.Errorf("error unmarshal token config: %w", err)
	}
	keyring := []byte(akInfo.Keyring)
	ak, err := decrypt(akInfo.AccessKeyID, keyring)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ak, err: %w", err)
	}
	sk, err := decrypt(akInfo.AccessKeySecret, keyring)
	if err != nil {
		return nil, fmt.Errorf("failed to decode sk, err: %w", err)
	}
	token, err := decrypt(akInfo.SecurityToken, keyring)
	if err != nil {
		return nil, fmt.Errorf("failed to decode token, err: %w", err)
	}
	layout := "2006-01-02T15:04:05Z"
	t, err := time.Parse(layout, akInfo.Expiration)
	if err != nil {
		return nil, fmt.Errorf("failed to parse expiration time, err: %w", err)
	}
	return &Credential{
		Credential: credentials.NewStsTokenCredential(string(ak), string(sk), string(token)),
		Expiration: t,
	}, nil
}

func (e *EncryptedCredentialProvider) Name() string {
	return "EncryptedCredentialProvider"
}

func pks5UnPadding(origData []byte) []byte {
	length := len(origData)
	unpadding := int(origData[length-1])
	return origData[:(length - unpadding)]
}

func decrypt(s string, keyring []byte) ([]byte, error) {
	cdata, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		logrus.Errorf("failed to decode base64 string, err: %v", err)
		return nil, err
	}
	block, err := aes.NewCipher(keyring)
	if err != nil {
		logrus.Errorf("failed to new cipher, err: %v", err)
		return nil, err
	}
	blockSize := block.BlockSize()

	iv := cdata[:blockSize]
	blockMode := cipher.NewCBCDecrypter(block, iv)
	origData := make([]byte, len(cdata)-blockSize)

	blockMode.CryptBlocks(origData, cdata[blockSize:])

	origData = pks5UnPadding(origData)
	return origData, nil
}
