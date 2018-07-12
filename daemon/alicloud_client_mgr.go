package daemon

import (
	"sync"
	"time"

	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
	"github.com/denverdino/aliyungo/metadata"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

var ROLE_NAME = "KubernetesMasterRole"

var TOKEN_RESYNC_PERIOD = 5 * time.Minute

var KUBERNETES_ALICLOUD_IDENTITY = "Kubernetes.Alicloud"

type TokenAuth struct {
	lock   sync.RWMutex
	auth   metadata.RoleAuth
	active bool
}

func (token *TokenAuth) authid() (string, string, string) {
	token.lock.RLock()
	defer token.lock.RUnlock()

	return token.auth.AccessKeyId,
		token.auth.AccessKeySecret,
		token.auth.SecurityToken
}

type ClientMgr struct {
	stop <-chan struct{}

	token *TokenAuth

	meta *metadata.MetaData
	ecs  *ecs.Client
	vpc  *ecs.Client
}

func NewClientMgr(key, secret string) (*ClientMgr, error) {
	token := &TokenAuth{
		auth: metadata.RoleAuth{
			AccessKeyId:     key,
			AccessKeySecret: secret,
		},
		active: false,
	}
	m := metadata.NewMetaData(nil)

	if key == "" || secret == "" {
		if rolename, err := m.RoleName(); err != nil {
			return nil, err
		} else {
			ROLE_NAME = rolename
			role, err := m.RamRoleToken(ROLE_NAME)
			if err != nil {
				return nil, err
			}
			log.Infof("alicloud: clientmgr, using role=[%s] with initial token=[%+v]", ROLE_NAME, role)
			token.auth = role
			token.active = true
		}
	}

	metaRegion, err := m.Region()
	if err != nil {
		return nil, err
	}
	regionId := common.Region(metaRegion)
	keyid, sec, tok := token.authid()
	ecsclient := ecs.NewECSClientWithSecurityToken(keyid, sec, tok, regionId)
	ecsclient.SetUserAgent(KUBERNETES_ALICLOUD_IDENTITY)
	vpcclient := ecs.NewVPCClientWithSecurityToken(keyid, sec, tok, regionId)

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
		role, err := mgr.meta.RamRoleToken(ROLE_NAME)
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
	}, time.Duration(TOKEN_RESYNC_PERIOD), mgr.stop)

	return mgr, nil
}

func (c *ClientMgr) MetaData() *metadata.MetaData {
	return c.meta
}
