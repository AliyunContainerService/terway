package daemon

import (
	"encoding/hex"
	"fmt"
	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/types"
	"github.com/d2g/dhcp4"
	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	eniNamePrefix    = "eni-cni-"
	eniDescription   = "interface create by eni-cni-plugin"
	eniTimeout       = 60
	metadataBase     = "http://100.100.100.200/latest/meta-data/"
	mainEniPath      = "mac"
	enisPath         = "network/interfaces/macs/"
	eniIDPath        = "network/interfaces/macs/%s/network-interface-id"
	eniIPPath        = "network/interfaces/macs/%s/primary-ip-address"
	instanceTypePath = "instance/instance-type"
)

func generateEniName() string {
	b := make([]byte, 3)
	rand.Seed(time.Now().UnixNano())
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return eniNamePrefix + hex.EncodeToString(b)
}

func metadataUrl(url string, arrayValue bool) (interface{}, error) {
	if !strings.HasPrefix(url, metadataBase) {
		url = metadataBase + url
	}
	resp, err := http.DefaultClient.Get(url)
	if err != nil {
		return "", err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	result := strings.Split(string(body), "\n")
	if !arrayValue {
		trimResult := strings.Trim(result[0], "/")
		return trimResult, nil
	} else {
		for i, str := range result {
			result[i] = strings.Trim(str, "/")
		}
		return result, nil
	}
}

var eniCache = struct {
	sync.RWMutex
	IfCache map[string]*types.ENI
}{IfCache: map[string]*types.ENI{}}

func getEniConfigFromLink(link netlink.Link) (*types.ENI, error) {
	return nil, fmt.Errorf("")
}

func getEniConfigFromDHCP(link netlink.Link) (*types.ENI, error) {
	c, err := newDHCPClient(link)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	pkt, err := backoffRetry(func() (*dhcp4.Packet, error) {
		if (link.Attrs().Flags & net.FlagUp) != net.FlagUp {
			logrus.Printf("Link %q down. Attempting to set up", link.Attrs().Name)
			if err = netlink.LinkSetUp(link); err != nil {
				return nil, err
			}
		}
		ok, ack, err := c.Request()
		logrus.Debugf("get ack: %v, %++v, %v", ok, ack, err)
		switch {
		case err != nil:
			return nil, err
		case !ok:
			return nil, fmt.Errorf("DHCP server NACK'd own offer")
		default:
			return &ack, nil
		}
		// avoid conflict with ecs dhclient
		//if len(([]byte)(ack)) != 0 {
		//	return &ack, nil
		//} else {
		//	return nil, fmt.Errorf("DHCP server NACK'd own offer")
		//}
	})

	if err != nil {
		return nil, err
	}

	address := net.IPNet{
		IP:   pkt.YIAddr(),
		Mask: parseSubnetMask(pkt.ParseOptions()),
	}

	eni := &types.ENI{
		Name:    link.Attrs().Name,
		Address: address,
		MAC:     link.Attrs().HardwareAddr.String(),
		Gateway: parseGateway(pkt.ParseOptions()),
	}
	return eni, nil
}

func getEniByMac(mac string) (*types.ENI, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}

	var linkByMAC netlink.Link
	for _, link := range links {
		if link.Attrs().HardwareAddr.String() == mac {
			linkByMAC = link
		}
	}
	if linkByMAC == nil {
		return nil, fmt.Errorf("cannot found interface of mac: %s", mac)
	}

	eniCache.RLock()
	eni, ok := eniCache.IfCache[mac]
	eniCache.RUnlock()
	if ok {
		eni.Name = linkByMAC.Attrs().Name
		return eni, nil
	}

	eni, err = getEniConfigFromLink(linkByMAC)
	if err != nil {
		//backoff use dhcp
		eni, err = getEniConfigFromDHCP(linkByMAC)
		if err != nil {
			return nil, err
		}
	}

	eniID, err := metadataUrl(fmt.Sprintf(metadataBase+eniIDPath, mac), false)
	if err != nil {
		return nil, err
	}
	if eni.ID, ok = eniID.(string); !ok {
		return nil, fmt.Errorf("can not get eni metadata: %s", mac)
	}

	eniCache.Lock()
	eniCache.IfCache[mac] = eni
	eniCache.Unlock()

	return eni, nil
}

// simple rate limit
type ratelimit struct {
	throttle chan time.Time
	ticker   *time.Ticker
}

func (rl *ratelimit) Take() {
	<-rl.throttle
}

func NewRateLimiter(thro int) *ratelimit {
	rl := &ratelimit{
		ticker:   time.NewTicker(30 * time.Second),
		throttle: make(chan time.Time, 3),
	}
	go func() {
		for t := range rl.ticker.C {
			select {
			case rl.throttle <- t:
			default:
			}
		}
	}()
	return rl
}

type Pool struct {
	mx           sync.RWMutex
	min          int
	max          int
	inuse        int
	isFull       bool
	interfaces   chan *types.ENI
	aliClientMgr *ClientMgr
	poolConfig   *types.PoolConfig
	rl           *ratelimit
	hotPlug      bool
}

func (p *Pool) createInterface() (*types.ENI, error) {
	p.rl.Take()
	if !p.hotPlug {
		return nil, fmt.Errorf("hotplug is disabled, enable it at terway config file")
	}

	createNetworkInterfaceArgs := &ecs.CreateNetworkInterfaceArgs{
		RegionId:             common.Region(p.poolConfig.Region),
		VSwitchId:            p.poolConfig.VSwitch,
		SecurityGroupId:      p.poolConfig.SecurityGroup,
		NetworkInterfaceName: generateEniName(),
		Description:          eniDescription,
	}
	createNetworkInterfaceResponse, err := p.aliClientMgr.ecs.CreateNetworkInterface(createNetworkInterfaceArgs)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			eniDestroy := &types.ENI{
				ID: createNetworkInterfaceResponse.NetworkInterfaceId,
			}
			p.destroyInterface(eniDestroy, true)
		}
	}()

	if err = p.aliClientMgr.ecs.WaitForNetworkInterface(createNetworkInterfaceArgs.RegionId,
		createNetworkInterfaceResponse.NetworkInterfaceId, "Available", eniTimeout); err != nil {
		return nil, err
	}

	attachNetworkInterfaceArgs := &ecs.AttachNetworkInterfaceArgs{
		RegionId:           common.Region(p.poolConfig.Region),
		NetworkInterfaceId: createNetworkInterfaceResponse.NetworkInterfaceId,
		InstanceId:         p.poolConfig.InstanceID,
	}

	if err = p.aliClientMgr.ecs.AttachNetworkInterface(attachNetworkInterfaceArgs); err != nil {
		return nil, err
	}

	if err = p.aliClientMgr.ecs.WaitForNetworkInterface(createNetworkInterfaceArgs.RegionId,
		createNetworkInterfaceResponse.NetworkInterfaceId, "InUse", eniTimeout); err != nil {
		return nil, err
	}

	describeNetworkInterfacesArgs := &ecs.DescribeNetworkInterfacesArgs{
		RegionId:           createNetworkInterfaceArgs.RegionId,
		NetworkInterfaceId: []string{createNetworkInterfaceResponse.NetworkInterfaceId},
	}
	var describeNetworkInterfacesResp *ecs.DescribeNetworkInterfacesResponse
	describeNetworkInterfacesResp, err = p.aliClientMgr.ecs.DescribeNetworkInterfaces(describeNetworkInterfacesArgs)
	if err != nil {
		return nil, err
	}

	if len(describeNetworkInterfacesResp.NetworkInterfaceSets.NetworkInterfaceSet) != 1 {
		err = fmt.Errorf("error get ENI interface: %s", createNetworkInterfaceResponse.NetworkInterfaceId)
		return nil, err
	}
	var eni *types.ENI
	eni, err = getEniByMac(describeNetworkInterfacesResp.NetworkInterfaceSets.NetworkInterfaceSet[0].MacAddress)
	return eni, err
}

func (p *Pool) destroyInterface(eni *types.ENI, force bool) error {
	p.rl.Take()
	detachNetworkInterfaceArgs := &ecs.DetachNetworkInterfaceArgs{
		RegionId:           common.Region(p.poolConfig.Region),
		NetworkInterfaceId: eni.ID,
		InstanceId:         p.poolConfig.InstanceID,
	}
	_, err := p.aliClientMgr.ecs.DetachNetworkInterface(detachNetworkInterfaceArgs)
	if err != nil && !force {
		return err
	}

	if err := p.aliClientMgr.ecs.WaitForNetworkInterface(detachNetworkInterfaceArgs.RegionId,
		eni.ID, "Available", eniTimeout); err != nil && !force {
		return err
	}

	deleteNetworkInterfaceArgs := &ecs.DeleteNetworkInterfaceArgs{
		RegionId:           common.Region(p.poolConfig.Region),
		NetworkInterfaceId: eni.ID,
	}

	if _, err = p.aliClientMgr.ecs.DeleteNetworkInterface(deleteNetworkInterfaceArgs); err != nil && !force {
		return err
	}
	return nil
}

func (p *Pool) addInterface() error {
	eni, err := p.createInterface()
	if err != nil {
		return err
	}

	p.interfaces <- eni
	return nil
}

func (p *Pool) releaseInterface(mac string) error {
	eni, err := getEniByMac(mac)
	if err != nil {
		return err
	}
	return p.destroyInterface(eni, false)
}

func (p *Pool) Allocate() (*types.ENI, error) {
	var allocatedENI *types.ENI
	for {
		if p.AvailableNics() > 0 {
			allocatedENI = <-p.interfaces
			var err error
			allocatedENI, err = getEniByMac(allocatedENI.MAC)
			if err != nil {
				return nil, err
			}
			break
		} else {
			if err := p.addInterface(); err != nil {
				logrus.Errorf("error add interface: %v", err)
			}
		}
	}

	if p.AvailableNics() < p.min {
		go p.addInterface()
	}
	return allocatedENI, nil
}

func (p *Pool) Release(mac string) error {
	if p.AvailableNics() >= p.max {
		if err := p.releaseInterface(mac); err != nil {
			return fmt.Errorf("error release interface of mac %s to release: %v", mac, err)
		}
	} else {
		eniRelease, err := getEniByMac(mac)
		if err != nil {
			logrus.Errorf("error get ENI config of mac %s to release: %v", mac, err)
			return err
		}
		p.interfaces <- eniRelease
	}

	return nil
}

func (p *Pool) getMainMacFromMetadata() (string, error) {
	//refresh system available Nics
	eniMainMacInterface, err := metadataUrl(mainEniPath, false)
	if err != nil {
		return "", err
	}
	eniMainMac, ok := eniMainMacInterface.(string)
	if !ok {
		return "", fmt.Errorf("error get main eni mac from metadata")
	}
	return eniMainMac, nil
}

// to avoid ecs metadata wrong mac address bug
func (p *Pool) getMainMacFromEth0() (string, error) {
	inf, err := net.InterfaceByName("eth0")
	if err != nil {
		return "", err
	}
	return inf.HardwareAddr.String(), nil
}

func (p *Pool) RefreshNics() error {
	p.mx.Lock()
	defer p.mx.Unlock()

	eniMainMac, err := p.getMainMacFromEth0()
	if err != nil {
		return err
	}

	eniMacsInterface, err := metadataUrl(enisPath, true)
	if err != nil {
		return err
	}
	eniMacs, ok := eniMacsInterface.([]string)
	if !ok {
		return fmt.Errorf("error get eni macs from metadata")
	}

	for _, eniMac := range eniMacs {
		if eniMac == eniMainMac {
			continue
		} else {
			logrus.Infof("getting available enis on system: %+v", eniMac)
			eni, err := getEniByMac(eniMac)
			logrus.Infof("got available enis on system: %+v", eni)
			if err != nil {
				logrus.Warnf("Error get mac: %s interface, err: %v", eniMac, err)
				continue
			}
			p.interfaces <- eni
		}
	}

	return nil
}

func (p *Pool) AvailableNics() int {
	p.mx.Lock()
	defer p.mx.Unlock()
	return len(p.interfaces)
}

func newPool(cfg *types.PoolConfig) (*Pool, error) {
	pool := &Pool{
		min:        cfg.MinPoolSize,
		max:        cfg.MaxPoolSize,
		rl:         NewRateLimiter(3),
		poolConfig: cfg,
		hotPlug:    cfg.HotPlug,
	}

	logrus.Debugf("Init Pool Config: %++v", pool)

	pool.interfaces = make(chan *types.ENI, 2*pool.max)

	if err := pool.RefreshNics(); err != nil {
		return nil, fmt.Errorf("error Refresh Nics: %v", err)
	}

	var err error
	if pool.aliClientMgr, err = NewClientMgr(cfg.AccessId, cfg.AccessSecret); err != nil {
		return nil, err
	}

	if pool.poolConfig.SecurityGroup == "" {
		instanceId, err := pool.aliClientMgr.meta.InstanceID()
		if err != nil {
			return nil, err
		}
		instanceInfo, err := pool.aliClientMgr.ecs.DescribeInstanceAttribute(instanceId)
		if err != nil {
			return nil, fmt.Errorf("error get security group for current instance: err: %v", err)
		}
		pool.poolConfig.SecurityGroup = instanceInfo.SecurityGroupIds.SecurityGroupId[0]
	}

	for i := pool.AvailableNics(); i < pool.min; {
		if err := pool.addInterface(); err != nil {
			logrus.Errorf("error add interface: %v", err)
		} else {
			i++
		}
	}

	for i := pool.AvailableNics(); i > pool.max; {
		nicRelease := <-pool.interfaces
		if err := pool.releaseInterface(nicRelease.MAC); err != nil {
			logrus.Errorf("error release interface: %v")
		} else {
			i--
		}
	}

	eniCap := pool.AvailableNics()
	if cfg.HotPlug {
		itVal, err := metadataUrl(instanceTypePath, false)
		if err != nil {
			return nil, err
		}
		instanceType, _ := itVal.(string)
		eniCap, err = deviceplugin.GetEniCapByInstanceType(instanceType)
		if err != nil {
			return nil, err
		}
	}
	plg := deviceplugin.NewEniDevicePlugin(eniCap)
	err = plg.Serve(deviceplugin.DefaultResourceName)
	if err != nil {
		logrus.Errorf("device plugin init failed: %v", err)
	}

	return pool, nil
}
