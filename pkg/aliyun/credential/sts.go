package credential

import (
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
)

type EncryptedCredentialInfo struct {
	AccessKeyID     string `json:"access.key.id"`
	AccessKeySecret string `json:"access.key.secret"`
	SecurityToken   string `json:"security.token"`
	Expiration      string `json:"expiration"`
	Keyring         string `json:"keyring"`
}

type EncryptedCredentialProvider struct {
	credentialPath string
}

// NewEncryptedCredentialProvider get token from /var/addon/token-config
func NewEncryptedCredentialProvider(credentialPath string) *EncryptedCredentialProvider {
	return &EncryptedCredentialProvider{credentialPath: credentialPath}
}

func (e *EncryptedCredentialProvider) Resolve() (*Credential, error) {
	log.Infof("resolve encrypted credential %s", e.credentialPath)
	if e.credentialPath == "" {
		return nil, nil
	}
	_, err := os.Stat(e.credentialPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s, err: %w", e.credentialPath, err)
	}
	var akInfo EncryptedCredentialInfo
	encodeTokenCfg, err := ioutil.ReadFile(e.credentialPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read token config, err: %w", err)
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
