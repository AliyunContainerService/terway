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

	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

type Client interface {
	ECS() *ecs.Client
	VPC() *vpc.Client

	EFLO() *eflo.Client
}

var (
	mgrLog                     = ctrl.Log.WithName("clientMgr")
	kubernetesAlicloudIdentity = "Kubernetes.Alicloud"

	tokenReSyncPeriod = 5 * time.Minute
)

type headerTransport struct {
	headers map[string]string
}

func (m *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range m.headers {
		req.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func clientCfg() *sdk.Config {
	scheme := "HTTPS"
	if os.Getenv("ALICLOUD_CLIENT_SCHEME") == "HTTP" {
		scheme = "HTTP"
	}
	s := &sdk.Config{
		Timeout:   20 * time.Second,
		Transport: http.DefaultTransport,
		UserAgent: kubernetesAlicloudIdentity,
		Scheme:    scheme,
	}
	if os.Getenv("X-ACSPROXY-ASCM-CONTEXT") != "" {
		s.Transport = &headerTransport{
			headers: map[string]string{
				"x-acsproxy-ascm-context": os.Getenv("X-ACSPROXY-ASCM-CONTEXT"),
			},
		}
	}

	return s
}

// ClientMgr manager of aliyun openapi clientset
type ClientMgr struct {
	regionID string

	auth Interface

	// protect things below
	sync.RWMutex

	expireAt time.Time
	updateAt time.Time

	ecs  *ecs.Client
	vpc  *vpc.Client
	eflo *eflo.Client

	ecsDomainOverride  string
	vpcDomainOverride  string
	efloDomainOverride string

	efloRegionOverride string

	endpointType string
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
	mgr.efloDomainOverride, err = parseURL(os.Getenv("EFLO_ENDPOINT"))
	if err != nil {
		return nil, err
	}

	mgr.efloRegionOverride = os.Getenv("EFLO_REGION_ID")
	if mgr.efloRegionOverride == "" {
		mgr.efloRegionOverride = regionID
	}

	mgr.endpointType = "vpc"
	if os.Getenv("ALICLOUD_ENDPOINT_TYPE") != "" {
		mgr.endpointType = os.Getenv("ALICLOUD_ENDPOINT_TYPE")
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
		mgrLog.Error(err, "refresh token error")
	}
	if ok {
		mgrLog.WithValues("updateAt", c.updateAt, "expireAt", c.expireAt).Info("credential update")
	}
	return c.vpc
}

func (c *ClientMgr) ECS() *ecs.Client {
	c.Lock()
	defer c.Unlock()
	ok, err := c.refreshToken()
	if err != nil {
		mgrLog.Error(err, "refresh token error")
	}
	if ok {
		mgrLog.WithValues("updateAt", c.updateAt, "expireAt", c.expireAt).Info("credential update")
	}
	return c.ecs
}

func (c *ClientMgr) EFLO() *eflo.Client {
	c.Lock()
	defer c.Unlock()
	ok, err := c.refreshToken()
	if err != nil {
		mgrLog.Error(err, "refresh token error")
	}
	if ok {
		mgrLog.WithValues("updateAt", c.updateAt, "expireAt", c.expireAt).Info("credential update")
	}
	return c.eflo
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
		c.ecs.SetEndpointRules(c.ecs.EndpointMap, "regional", c.endpointType)

		if c.ecsDomainOverride != "" {
			c.ecs.Domain = c.ecsDomainOverride
		}

		c.vpc, err = vpc.NewClientWithOptions(c.regionID, clientCfg(), cc.Credential)
		if err != nil {
			return false, err
		}
		c.vpc.SetEndpointRules(c.vpc.EndpointMap, "regional", c.endpointType)

		if c.vpcDomainOverride != "" {
			c.vpc.Domain = c.vpcDomainOverride
		}

		c.eflo, err = eflo.NewClientWithOptions(c.efloRegionOverride, clientCfg(), cc.Credential)
		if err != nil {
			return false, err
		}
		c.eflo.SetEndpointRules(c.eflo.EndpointMap, "regional", c.endpointType)

		if c.efloDomainOverride != "" {
			c.eflo.Domain = c.efloDomainOverride
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
