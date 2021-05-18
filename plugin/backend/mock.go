package backend

import (
	"context"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/rpc"
	"google.golang.org/grpc"
)

type ENI struct {
	PodIPs    []*rpc.IPSet
	Subnet    *rpc.IPSet
	MAC       string
	GatewayIP *rpc.IPSet

	UsedPodIPs []*rpc.IPSet
}

// IPAM for now just specific ip
type IPAM struct {
	IPs []*ENI
}

func NewIPAM(enis []*ENI) *IPAM {
	i := &IPAM{
		IPs: enis,
	}

	return i
}

func (i *IPAM) Alloc() *rpc.ENI {
	for _, v := range i.IPs {
		if len(v.PodIPs) == 0 {
			continue
		}
		ipSet := v.PodIPs[0]
		v.UsedPodIPs = append(v.UsedPodIPs, ipSet)

		return &rpc.ENI{
			PodIP:     ipSet,
			Subnet:    v.Subnet,
			MAC:       v.MAC,
			GatewayIP: v.GatewayIP,
		}
	}
	return nil
}

func (i *IPAM) Release(ip *rpc.IPSet) {
}

// Mock is used for e2e test and require ecs with at least 3 eni
type Mock struct {
	IPAM *IPAM

	IPStack string // v4 or dual
	TPType  rpc.IPType
}

func NewMock() *Mock {
	macs, err := metadata.GetENIsMAC()
	if err != nil {
		panic(err)
	}
	primaryMAC, err := metadata.GetPrimaryENIMAC()
	if err != nil {
		panic(err)
	}

	var enis []*ENI
	for _, mac := range macs {
		if mac == primaryMAC {
			continue
		}
		eni := getENI(mac)
		enis = append(enis, eni)
	}
	return &Mock{
		IPAM:    NewIPAM(enis),
		IPStack: "dual",
		TPType:  rpc.IPType_TypeENIMultiIP,
	}
}

func getENI(mac string) *ENI {
	v4IPs, err := metadata.GetENIPrivateIPs(mac)
	if err != nil {
		panic(err)
	}
	v6IPs, err := metadata.GetENIPrivateIPv6IPs(mac)
	if err != nil {
		panic(err)
	}

	v4CIDR, err := metadata.GetVSwitchCIDR(mac)
	if err != nil {
		panic(err)
	}
	v6CIDR, err := metadata.GetVSwitchIPv6CIDR(mac)
	if err != nil {
		panic(err)
	}
	v4GW, err := metadata.GetENIGateway(mac)
	if err != nil {
		panic(err)
	}
	v6GW, err := metadata.GetENIV6Gateway(mac)
	if err != nil {
		panic(err)
	}

	var ips []*rpc.IPSet
	ips = append(ips, &rpc.IPSet{
		IPv4: v4IPs[0].String(),
		IPv6: v6IPs[0].String(),
	})

	return &ENI{
		PodIPs: ips,
		Subnet: &rpc.IPSet{
			IPv4: v4CIDR.String(),
			IPv6: v6CIDR.String(),
		},
		MAC: mac,
		GatewayIP: &rpc.IPSet{
			IPv4: v4GW.String(),
			IPv6: v6GW.String(),
		},
	}
}

func (m *Mock) AllocIP(ctx context.Context, in *rpc.AllocIPRequest, opts ...grpc.CallOption) (*rpc.AllocIPReply, error) {
	switch m.TPType {
	case rpc.IPType_TypeENIMultiIP:

		eni := m.IPAM.Alloc()
		result := &rpc.AllocIPReply_ENIMultiIP{
			ENIMultiIP: &rpc.ENIMultiIP{
				ENIConfig: eni,
				PodConfig: &rpc.Pod{
					Ingress: 0,
					Egress:  0,
				},
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/16",
					IPv6: "2002::/100",
				},
			},
		}
		return &rpc.AllocIPReply{
			Success:     true,
			IPType:      m.TPType,
			NetworkInfo: result,
		}, nil
	}

	return &rpc.AllocIPReply{
		Success: false,
		IPType:  m.TPType,
	}, nil
}

func (m *Mock) ReleaseIP(ctx context.Context, in *rpc.ReleaseIPRequest, opts ...grpc.CallOption) (*rpc.ReleaseIPReply, error) {
	//m.IPAM.Release(nil)
	return &rpc.ReleaseIPReply{
		Success: true,
	}, nil
}

func (m *Mock) GetIPInfo(ctx context.Context, in *rpc.GetInfoRequest, opts ...grpc.CallOption) (*rpc.GetInfoReply, error) {
	return &rpc.GetInfoReply{
		IPType: m.TPType,
	}, nil
}

func (m *Mock) RecordEvent(ctx context.Context, in *rpc.EventRequest, opts ...grpc.CallOption) (*rpc.EventReply, error) {
	return nil, nil
}
