//go:build default_build

package credential

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/logger"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

type Client interface {
	ECS() *ecs.Client
	VPC() *vpc.Client
}

var (
	mgrLog                     = logger.DefaultLogger.WithField("subSys", "clientMgr")
	kubernetesAlicloudIdentity = "Kubernetes.Alicloud"

	tokenReSyncPeriod = 5 * time.Minute
)

func clientCfg() *sdk.Config {
	scheme := "HTTPS"
	if os.Getenv("ALICLOUD_CLIENT_SCHEME") == "HTTP" {
		scheme = "HTTP"
	}
	return &sdk.Config{
		Timeout:   20 * time.Second,
		Transport: http.DefaultTransport,
		UserAgent: kubernetesAlicloudIdentity,
		Scheme:    scheme,
	}
}

// ClientMgr manager of aliyun openapi clientset
type ClientMgr struct {
	regionID string

	auth Interface

	// protect things below
	sync.RWMutex

	expireAt time.Time
	updateAt time.Time

	ecs *ecs.Client
	vpc *vpc.Client

	ecsDomainOverride string
	vpcDomainOverride string
}

// NewClientMgr return new aliyun client manager
func NewClientMgr(regionID string, providers ...Interface) (*ClientMgr, error) {
	mgr := &ClientMgr{
		regionID: regionID,
	}

	var err error
	mgr.ecsDomainOverride, err = parseURL(os.Getenv("ECS_ENDPOINT"))
	if err != nil {
		return nil, err
	}
	mgr.vpcDomainOverride, err = parseURL(os.Getenv("VPC_ENDPOINT"))
	if err != nil {
		return nil, err
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

		if c.ecsDomainOverride != "" {
			c.ecs.Domain = c.ecsDomainOverride
		}

		c.vpc, err = vpc.NewClientWithOptions(c.regionID, clientCfg(), cc.Credential)
		if err != nil {
			return false, err
		}
		c.vpc.SetEndpointRules(c.vpc.EndpointMap, "regional", "vpc")

		if c.vpcDomainOverride != "" {
			c.vpc.Domain = c.vpcDomainOverride
		}

		c.expireAt = cc.Expiration
		return true, nil
	}

	return false, nil
}

func parseURL(str string) (string, error) {
	if str == "" {
		return "", nil
	}
	if !strings.HasPrefix(str, "http") {
		str = "http://" + str
	}
	u, err := url.Parse(str)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}
