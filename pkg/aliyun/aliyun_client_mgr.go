package aliyun

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
)

var (
	mgrLog                     = logrus.WithField("subSys", "clientMgr")
	kubernetesAlicloudIdentity = "Kubernetes.Alicloud"

	tokenReSyncPeriod = 5 * time.Minute
)

func clientCfg() *sdk.Config {
	return &sdk.Config{
		Timeout:   20 * time.Second,
		Transport: http.DefaultTransport,
		UserAgent: kubernetesAlicloudIdentity,
		Scheme:    "HTTPS",
	}
}

// ClientMgr manager of aliyun openapi clientset
type ClientMgr struct {
	regionID string

	auth credential.Interface

	// protect things below
	sync.RWMutex

	expireAt time.Time
	updateAt time.Time

	ecs *ecs.Client
	vpc *vpc.Client
}

// NewClientMgr return new aliyun client manager
func NewClientMgr(key, secret, credentialPath, regionID string) (*ClientMgr, error) {
	mgr := &ClientMgr{
		regionID: regionID,
	}
	providers := []credential.Interface{
		credential.NewAKPairProvider(key, secret),
		credential.NewEncryptedCredentialProvider(credentialPath),
		credential.NewMetadataProvider(),
	}
	for _, p := range providers {
		c, err := p.Resolve()
		if err != nil {
			return nil, err
		}
		if c == nil {
			continue
		}
		mgr.auth = p
		mgrLog.Infof("using %s provider", mgr.auth.Name())
		break
	}
	if mgr.auth == nil {
		return nil, errors.New("unable to found a valid credential provider")
	}

	return mgr, nil
}

func (c *ClientMgr) VPC() *vpc.Client {
	c.Lock()
	defer c.Unlock()
	ok, err := c.refreshToken()
	if err != nil {
		mgrLog.Error(err)
	}
	if ok {
		mgrLog.WithFields(map[string]interface{}{"updateAt": c.updateAt, "expireAt": c.expireAt}).Infof("credential update")
	}
	return c.vpc
}

func (c *ClientMgr) ECS() *ecs.Client {
	c.Lock()
	defer c.Unlock()
	ok, err := c.refreshToken()
	if err != nil {
		mgrLog.Error(err)
	}
	if ok {
		mgrLog.WithFields(map[string]interface{}{"updateAt": c.updateAt, "expireAt": c.expireAt}).Infof("credential update")
	}
	return c.ecs
}

func (c *ClientMgr) refreshToken() (bool, error) {
	if c.updateAt.IsZero() || c.expireAt.Before(time.Now()) || time.Since(c.updateAt) > tokenReSyncPeriod {
		var err error
		defer func() {
			if err == nil {
				c.updateAt = time.Now()
			}
		}()

		cc, err := c.auth.Resolve()
		if err != nil {
			return false, err
		}

		c.ecs, err = ecs.NewClientWithOptions(c.regionID, clientCfg(), cc.Credential)
		if err != nil {
			return false, err
		}
		c.ecs.SetEndpointRules(c.ecs.EndpointMap, "regional", "vpc")

		c.vpc, err = vpc.NewClientWithOptions(c.regionID, clientCfg(), cc.Credential)
		if err != nil {
			return false, err
		}
		c.vpc.SetEndpointRules(c.vpc.EndpointMap, "regional", "vpc")

		c.expireAt = cc.Expiration
		return true, nil
	}

	return false, nil
}
