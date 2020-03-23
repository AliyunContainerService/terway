package aliyun

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/golang/glog"

	"github.com/denverdino/aliyungo/common"
	"github.com/sirupsen/logrus"

	"github.com/pkg/errors"

	"github.com/denverdino/aliyungo/ecs"
	"github.com/denverdino/aliyungo/metadata"
)

var (
	tokenResyncPeriod          = 5 * time.Minute
	kubernetesAlicloudIdentity = "Kubernetes.Alicloud"
)

type authProvider interface {
	GetAuthToken() (metadata.RoleAuth, error)
}

type akPairProvider struct {
	accessKeyID     string
	accessKeySecret string
}

type encryptedCredentialProvider struct {
	credentialPath string
}

type metadataProvider struct {
	roleName string
	meta     *metadata.MetaData
}

// ClientMgr manager of aliyun openapi clientset
type ClientMgr struct {
	sync.Locker

	auth            authProvider
	token           metadata.RoleAuth
	tokenUpdateTime time.Time
	regionID        common.Region

	meta *metadata.MetaData
	ecs  *ecs.Client
	vpc  *ecs.Client
}

// NewClientMgr return new aliyun client manager
func NewClientMgr(key, secret, credentialPath string) (*ClientMgr, error) {
	var (
		mgr = &ClientMgr{
			Locker: &sync.RWMutex{},
			meta:   metadata.NewMetaData(nil),
		}
		err error
	)

	regionID, err := mgr.meta.Region()
	if err != nil {
		return nil, err
	}
	mgr.regionID = common.Region(regionID)

	if key != "" && secret != "" {
		logrus.Infof("using AkPairProvider as auth provider")
		mgr.auth = newAkPairProvider(key, secret)
		return mgr, nil
	}
	if credentialPath != "" {
		logrus.Infof("using EncryptedCredentialProvider as auth provider")
		_, err = os.Stat(credentialPath)
		if err == nil {
			mgr.auth, err = newEncryptedCredentialProvider(credentialPath)
			if err != nil {
				return nil, err
			}
			return mgr, nil
		}
		if !os.IsNotExist(err) {
			return nil, errors.Errorf("credential files invalid: %v, %v", credentialPath, err)
		}
	}
	// fallback to Metadata Provider
	logrus.Infof("using MetadataProvider as auth provider")
	mgr.auth, err = newMetadataProvider()
	return mgr, err
}

func (c *ClientMgr) refreshToken() (bool, error) {
	if c.tokenUpdateTime.IsZero() || c.token.Expiration.Before(time.Now()) || time.Since(c.tokenUpdateTime) > tokenResyncPeriod {
		var err error
		c.token, err = c.auth.GetAuthToken()
		if err != nil {
			return false, errors.Errorf("error refresh auth token: %v", err)
		}
		c.tokenUpdateTime = time.Now()
		return true, nil
	}
	return false, nil
}

// Vpc Get latest vpc client
func (c *ClientMgr) Vpc() *ecs.Client {
	c.Lock()
	defer c.Unlock()
	tokenUpdated, err := c.refreshToken()
	if err != nil {
		logrus.Errorf("Error refresh OpenAPI token: %v", err)
	}
	if c.vpc == nil {
		c.vpc = ecs.NewVPCClientWithSecurityToken4RegionalDomain(c.token.AccessKeyId, c.token.AccessKeySecret, c.token.SecurityToken, c.regionID)
		c.vpc.SetUserAgent(kubernetesAlicloudIdentity)
	} else if tokenUpdated {
		c.vpc.WithSecurityToken(c.token.SecurityToken).
			WithAccessKeyId(c.token.AccessKeyId).
			WithAccessKeySecret(c.token.AccessKeySecret)
	}
	return c.vpc
}

// Ecs Get latest Ecs client
func (c *ClientMgr) Ecs() *ecs.Client {
	tokenUpdated, err := c.refreshToken()
	if err != nil {
		logrus.Errorf("Error refresh OpenAPI token: %v", err)
	}
	if c.ecs == nil {
		c.ecs = ecs.NewECSClientWithSecurityToken4RegionalDomain(c.token.AccessKeyId, c.token.AccessKeySecret, c.token.SecurityToken, c.regionID)
		c.ecs.SetUserAgent(kubernetesAlicloudIdentity)
	} else if tokenUpdated {
		c.ecs.WithSecurityToken(c.token.SecurityToken).
			WithAccessKeyId(c.token.AccessKeyId).
			WithAccessKeySecret(c.token.AccessKeySecret)
	}
	return c.ecs
}

// MetaData return aliyun metadata client
func (c *ClientMgr) MetaData() *metadata.MetaData {
	return c.meta
}

func newAkPairProvider(accessKeyID string, accessKeySecret string) *akPairProvider {
	return &akPairProvider{accessKeyID: accessKeyID, accessKeySecret: accessKeySecret}
}

func (a *akPairProvider) GetAuthToken() (metadata.RoleAuth, error) {
	return metadata.RoleAuth{
		AccessKeyId:     a.accessKeyID,
		AccessKeySecret: a.accessKeySecret,
		Expiration:      time.Now().Add(time.Hour),
	}, nil
}

type encryptedCredentialInfo struct {
	AccessKeyID     string `json:"access.key.id"`
	AccessKeySecret string `json:"access.key.secret"`
	SecurityToken   string `json:"security.token"`
	Expiration      string `json:"expiration"`
	Keyring         string `json:"keyring"`
}

func pks5UnPadding(origData []byte) []byte {
	length := len(origData)
	unpadding := int(origData[length-1])
	return origData[:(length - unpadding)]
}

func decrypt(s string, keyring []byte) ([]byte, error) {
	cdata, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		glog.Errorf("failed to decode base64 string, err: %v", err)
		return nil, err
	}
	block, err := aes.NewCipher(keyring)
	if err != nil {
		glog.Errorf("failed to new cipher, err: %v", err)
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

func newEncryptedCredentialProvider(credentialPath string) (*encryptedCredentialProvider, error) {
	var ec = &encryptedCredentialProvider{credentialPath: credentialPath}
	_, err := ec.GetAuthToken()
	if err != nil {
		return nil, errors.Errorf("error get auth token from encrypted credential info: %v", err)
	}
	return ec, nil
}

func (e *encryptedCredentialProvider) GetAuthToken() (metadata.RoleAuth, error) {
	var akInfo encryptedCredentialInfo
	encodeTokenCfg, err := ioutil.ReadFile(e.credentialPath)
	if err != nil {
		return metadata.RoleAuth{}, errors.Errorf("failed to read token config, err: %v", err)
	}
	err = json.Unmarshal(encodeTokenCfg, &akInfo)
	if err != nil {
		return metadata.RoleAuth{}, errors.Errorf("error unmarshal token config: %v", err)
	}
	keyring := []byte(akInfo.Keyring)
	ak, err := decrypt(akInfo.AccessKeyID, keyring)
	if err != nil {
		return metadata.RoleAuth{}, errors.Errorf("failed to decode ak, err: %v", err)
	}
	sk, err := decrypt(akInfo.AccessKeySecret, keyring)
	if err != nil {
		return metadata.RoleAuth{}, errors.Errorf("failed to decode sk, err: %v", err)
	}
	token, err := decrypt(akInfo.SecurityToken, keyring)
	if err != nil {
		return metadata.RoleAuth{}, errors.Errorf("failed to decode token, err: %v", err)
	}
	layout := "2006-01-02T15:04:05Z"
	t, err := time.Parse(layout, akInfo.Expiration)
	if err != nil {
		return metadata.RoleAuth{}, errors.Errorf("failed to parse expiration time, err: %v", err)
	}

	return metadata.RoleAuth{
		AccessKeyId:     string(ak),
		AccessKeySecret: string(sk),
		Expiration:      t,
		SecurityToken:   string(token),
	}, nil
}

const defaultRoleName = "KubernetesMasterRole"

func newMetadataProvider() (*metadataProvider, error) {
	mp := &metadataProvider{
		roleName: defaultRoleName,
		meta:     metadata.NewMetaData(nil),
	}
	var err error
	mp.roleName, err = mp.meta.RoleName()
	if err != nil {
		return nil, errors.Wrapf(err, "error get instance role from metadata")
	}

	return mp, nil
}

func (m *metadataProvider) GetAuthToken() (metadata.RoleAuth, error) {
	role, err := m.meta.RamRoleToken(m.roleName)
	if err != nil {
		return metadata.RoleAuth{}, fmt.Errorf("alicloud: clientmgr, error get ram role token: %s", err)
	}
	return role, nil
}
