package aliyun

import (
	"sync"
	"time"

	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
	"github.com/denverdino/aliyungo/metadata"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

var (
	roleName                   = "KubernetesMasterRole"
	tokenResyncPeriod          = 5 * time.Minute
	kubernetesAlicloudIdentity = "Kubernetes.Alicloud"
)

type tokenAuth struct {
	lock   sync.RWMutex
	auth   metadata.RoleAuth
	active bool
}

func (token *tokenAuth) authid() (string, string, string) {
	token.lock.RLock()
	defer token.lock.RUnlock()

	return token.auth.AccessKeyId,
		token.auth.AccessKeySecret,
		token.auth.SecurityToken
}

// ClientMgr manager of aliyun openapi clientset
type ClientMgr struct {
	stop <-chan struct{}

	token *tokenAuth

	meta *metadata.MetaData
	ecs  *ecs.Client
	vpc  *ecs.Client
}

// NewClientMgr return new aliyun client manager
func NewClientMgr(key, secret string) (*ClientMgr, error) {
	token := &tokenAuth{
		auth: metadata.RoleAuth{
			AccessKeyId:     key,
			AccessKeySecret: secret,
		},
		active: false,
	}
	m := metadata.NewMetaData(nil)

	if key == "" || secret == "" {
		var err error
		roleName, err = m.RoleName()
		if err != nil {
			return nil, err
		}
		role, err := m.RamRoleToken(roleName)
		if err != nil {
			return nil, err
		}
		log.Debugf("alicloud: clientmgr, using role=[%s] with initial token=[%+v]", roleName, role)
		token.auth = role
		token.active = true
	}

	metaRegion, err := m.Region()
	if err != nil {
		return nil, err
	}
	regionID := common.Region(metaRegion)
	keyid, sec, tok := token.authid()
	ecsclient := ecs.NewECSClientWithSecurityToken(keyid, sec, tok, regionID)
	ecsclient.SetUserAgent(kubernetesAlicloudIdentity)
	vpcclient := ecs.NewVPCClientWithSecurityToken(keyid, sec, tok, regionID)

	mgr := &ClientMgr{
		stop:  make(<-chan struct{}, 1),
		token: token,
		meta:  m,
		ecs:   ecsclient,
		vpc:   vpcclient,
	}
	if !token.active {
		// use key and secret
		log.Infof("alicloud: clientmgr, use accesskeyid and accesskeysecret mode to authenticate user. without token")
		return mgr, nil
	}
	go wait.Until(func() {
		// refresh client token periodically
		token.lock.Lock()
		defer token.lock.Unlock()
		role, err := mgr.meta.RamRoleToken(roleName)
		if err != nil {
			log.Errorf("alicloud: clientmgr, error get ram role token [%s]\n", err.Error())
			return
		}
		token.auth = role
		ecsclient.WithSecurityToken(role.SecurityToken).
			WithAccessKeyId(role.AccessKeyId).
			WithAccessKeySecret(role.AccessKeySecret)
		vpcclient.WithSecurityToken(role.SecurityToken).
			WithAccessKeyId(role.AccessKeyId).
			WithAccessKeySecret(role.AccessKeySecret)
	}, time.Duration(tokenResyncPeriod), mgr.stop)

	return mgr, nil
}

// MetaData return aliyun metadata client
func (c *ClientMgr) MetaData() *metadata.MetaData {
	return c.meta
}
