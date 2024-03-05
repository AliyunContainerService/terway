package aliyun

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	vswpool "github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ factory.Factory = &Eflo{}

type Eflo struct {
	ctx context.Context

	enableIPv4, enableIPv6 bool

	instanceID string
	zoneID     string

	api              *client.OpenAPI
	vSwitchOptions   []string
	securityGroupIDs []string
	resourceGroupID  string
	vsw              *vswpool.SwitchPool
}

func NewEflo(ctx context.Context, openAPI *client.OpenAPI, vsw *vswpool.SwitchPool, cfg *types.ENIConfig) *Eflo {
	return &Eflo{
		ctx:              ctx,
		api:              openAPI,
		vsw:              vsw,
		enableIPv4:       cfg.EnableIPv4,
		enableIPv6:       cfg.EnableIPv6,
		instanceID:       cfg.InstanceID,
		zoneID:           cfg.ZoneID,
		vSwitchOptions:   cfg.VSwitchOptions,
		securityGroupIDs: cfg.SecurityGroupIDs,
		resourceGroupID:  cfg.ResourceGroupID,
	}
}

func (p *Eflo) CreateNetworkInterface(ipv4, ipv6 int, eniType string) (*daemon.ENI, []netip.Addr, []netip.Addr, error) {
	ctx, cancel := context.WithTimeout(p.ctx, time.Second*60)
	defer cancel()

	vsw, innerErr := p.vsw.GetOne(ctx, p.api, p.zoneID, p.vSwitchOptions)
	if innerErr != nil {
		return nil, nil, nil, innerErr
	}

	klog.Infof("CreateNetworkInterface %s %s %s %s", p.zoneID, p.instanceID, vsw.ID, p.securityGroupIDs[0])

	_, eniID, err := p.api.CreateElasticNetworkInterface(p.zoneID, p.instanceID, vsw.ID, p.securityGroupIDs[0])
	if err != nil {
		return nil, nil, nil, err
	}

	eni := &daemon.ENI{
		ID: eniID,
	}

	var resp *eflo.Content
	err = retry.OnError(wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   1.1,
		Jitter:   0,
		Steps:    5,
	}, func(err error) bool {
		return true
	}, func() error {
		resp, err = p.api.GetElasticNetworkInterface(eniID)
		if err != nil {
			return err
		}

		if resp.Status != "Available" {
			klog.Infof("eni status is not ready %s %s", eniID, resp.Status)
			return fmt.Errorf("not ready %s", resp.Status)
		}
		return nil
	})
	if err != nil {
		return eni, nil, nil, err
	}
	if resp == nil {
		return eni, nil, nil, fmt.Errorf("GetElasticNetworkInterface return nil resp %s", eniID)
	}

	paimaryAddr, err := netip.ParseAddr(resp.Ip)
	if err != nil {
		return eni, nil, nil, err
	}

	primaryIP := types.IPSet{}
	primaryIP.SetIP(resp.Ip)

	gatewayIP := types.IPSet{}
	gatewayIP.SetIP(resp.Gateway)

	vswCIDR := types.IPNetSet{}
	gwAddr, err := netip.ParseAddr(resp.Gateway)
	if err != nil {
		return eni, nil, nil, err
	}
	mask, err := strconv.Atoi(resp.Mask)
	if err != nil {
		return eni, nil, nil, err
	}

	prefix := netip.PrefixFrom(gwAddr, mask)
	vswCIDR.SetIPNet(prefix.Masked().String())

	eni.MAC = resp.Mac
	eni.VSwitchID = resp.VSwitchId
	eni.PrimaryIP = primaryIP
	eni.GatewayIP = gatewayIP
	eni.VSwitchCIDR = vswCIDR

	ipv4Slice := make([]netip.Addr, 0)
	ipv6Slice := make([]netip.Addr, 0)

	ipv4Slice = append(ipv4Slice, paimaryAddr)
	return eni, ipv4Slice, ipv6Slice, nil
}

func (p *Eflo) AssignNIPv4(eniID string, count int, mac string) ([]netip.Addr, error) {
	ipName, err := p.api.AssignLeniPrivateIPAddress(p.ctx, eniID, "")
	if err != nil {
		return nil, err
	}

	re := make([]netip.Addr, 0)
	err = retry.OnError(wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   1,
		Jitter:   0,
		Steps:    3,
	}, func(err error) bool {
		return true
	}, func() error {
		content, err := p.api.ListLeniPrivateIPAddresses(p.ctx, "", ipName, "")
		if err != nil {
			klog.Infof("failed to find ip %s", ipName)
			return err
		}
		for _, data := range content.Data {
			addr, err := netip.ParseAddr(data.PrivateIpAddress)
			if err != nil {
				klog.Infof("failed to parse privateIP %#v", data)
				return err
			}
			re = append(re, addr)
		}
		return nil
	})

	return re, err
}

func (p *Eflo) AssignNIPv6(eniID string, count int, mac string) ([]netip.Addr, error) {
	return nil, nil
}

func (p *Eflo) UnAssignNIPv4(eniID string, ips []netip.Addr, mac string) error {
	klog.Infof("unassign nipv4 id %s ips %s", eniID, ips)
	for _, ip := range ips {
		content, err := p.api.ListLeniPrivateIPAddresses(p.ctx, "", "", ip.String())
		if err != nil {
			return err
		}
		if len(content.Data) == 0 {
			return nil
		}

		err = p.api.UnassignLeniPrivateIPAddress(p.ctx, eniID, content.Data[0].IpName)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Eflo) UnAssignNIPv6(eniID string, ips []netip.Addr, mac string) error {
	return nil
}

func (p *Eflo) DeleteNetworkInterface(eniID string) error {
	return p.api.DeleteElasticNetworkInterface(p.ctx, eniID)
}

func (p *Eflo) LoadNetworkInterface(mac string) ([]netip.Addr, []netip.Addr, error) {
	content, err := p.api.ListElasticNetworkInterfaces(p.ctx, p.zoneID, p.instanceID, "")
	if err != nil {
		return nil, nil, fmt.Errorf("LoadNetworkInterface err %w", err)
	}

	klog.Infof("ListElasticNetworkInterfaces %s %s len %d", p.zoneID, p.instanceID, len(content.Data))

	for _, data := range content.Data {
		if data.Mac != mac {
			continue
		}

		ipv4 := make([]netip.Addr, 0)
		ipv6 := make([]netip.Addr, 0)

		primaryAddr, err := netip.ParseAddr(data.Ip)
		if err != nil {
			return nil, nil, err
		}

		ipv4 = append(ipv4, primaryAddr)

		privateIPAddresses, err := p.api.ListLeniPrivateIPAddresses(p.ctx, data.ElasticNetworkInterfaceId, "", "")
		if err != nil {
			return nil, nil, err
		}

		klog.Infof("ListLeniPrivateIpAddresses %s %d", data.ElasticNetworkInterfaceId, len(privateIPAddresses.Data))

		for _, ip := range privateIPAddresses.Data {
			addr, err := netip.ParseAddr(ip.PrivateIpAddress)
			if err != nil {
				return nil, nil, err
			}
			ipv4 = append(ipv4, addr)
		}
		return ipv4, ipv6, nil
	}

	return nil, nil, nil
}

func (p *Eflo) GetAttachedNetworkInterface(preferTrunkID string) ([]*daemon.ENI, error) {
	content, err := p.api.ListElasticNetworkInterfaces(p.ctx, p.zoneID, p.instanceID, "")
	if err != nil {
		return nil, err
	}
	enis := make([]*daemon.ENI, 0)
	for _, data := range content.Data {
		klog.Infof("ListElasticNetworkInterfaces %s %s %s", data.ElasticNetworkInterfaceId, data.Type, data.Mac)
		if data.Type == "DEFAULT" { // CUSTOM for our own card
			continue
		}

		primaryIP := types.IPSet{}
		primaryIP.SetIP(data.Ip)

		gatewayIP := types.IPSet{}
		gatewayIP.SetIP(data.Gateway)

		vswCIDR := types.IPNetSet{}
		gwAddr, err := netip.ParseAddr(data.Gateway)
		if err != nil {
			return nil, err
		}
		mask, err := strconv.Atoi(data.Mask)
		if err != nil {
			return nil, err
		}
		prefix := netip.PrefixFrom(gwAddr, mask)
		vswCIDR.SetIPNet(prefix.Masked().String())

		eni := &daemon.ENI{
			ID:          data.ElasticNetworkInterfaceId,
			MAC:         data.Mac,
			VSwitchCIDR: vswCIDR,
			VSwitchID:   data.VSwitchId,
			PrimaryIP:   primaryIP,
			GatewayIP:   gatewayIP,
		}

		enis = append(enis, eni)
	}
	return enis, nil
}
