package aliyun

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	mockclient "github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	"github.com/AliyunContainerService/terway/pkg/aliyun/eni"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types/daemon"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockENIInfoGetter struct {
	eni.ENIInfoGetter
	mac string
}

func (m *mockENIInfoGetter) GetENIs(useCache bool) ([]*daemon.ENI, error) {
	return []*daemon.ENI{
		{
			ID:  "eni-id",
			MAC: m.mac,
		},
	}, nil
}

func (m *mockENIInfoGetter) GetENIPrivateAddressesByMACv2(mac string) ([]netip.Addr, error) {
	if mac != m.mac {
		return nil, fmt.Errorf("mac not match")
	}
	return []netip.Addr{netip.MustParseAddr("192.168.1.1")}, nil
}

func (m *mockENIInfoGetter) GetENIPrivateIPv6AddressesByMACv2(mac string) ([]netip.Addr, error) {
	if mac != m.mac {
		return nil, fmt.Errorf("mac not match")
	}
	return []netip.Addr{netip.MustParseAddr("fd00::1")}, nil
}

func TestAliyun_CreateNetworkInterface(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)
	vpcClient := mockclient.NewVPC(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	primaryIP := "192.168.1.10"
	privateIP1 := "192.168.1.11"
	privateIP2 := "192.168.1.12"
	ipv6Addr1 := "fd00::11"
	ipv6Addr2 := "fd00::12"
	eniID := "eni-id"
	vswID := "vsw-id-1"

	openAPI.On("GetVPC").Return(vpcClient).Maybe()
	openAPI.On("GetECS").Return(ecsClient).Maybe()

	vpcClient.On("DescribeVSwitchByID", mock.Anything, vswID).Return(&vpc.VSwitch{
		VSwitchId:               vswID,
		ZoneId:                  "zone-id",
		AvailableIpAddressCount: 100,
	}, nil).Maybe()

	createResp := &client.NetworkInterface{
		NetworkInterfaceID: eniID,
		MacAddress:         mac,
		PrivateIPAddress:   primaryIP,
		PrivateIPSets: []client.IPSet{
			{IPAddress: privateIP1},
			{IPAddress: privateIP2},
		},
		IPv6Set: []client.IPSet{
			{IPAddress: ipv6Addr1},
			{IPAddress: ipv6Addr2},
		},
		VSwitchID: vswID,
	}
	ecsClient.On("CreateNetworkInterface", mock.Anything, mock.Anything).Return(createResp, nil)
	ecsClient.On("AttachNetworkInterface", mock.Anything, mock.Anything).Return(nil)

	describeResp := []*client.NetworkInterface{
		{
			NetworkInterfaceID: eniID,
			Status:             client.ENIStatusInUse,
		},
	}
	ecsClient.On("DescribeNetworkInterface", mock.Anything, mock.Anything, []string{eniID}, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(describeResp, nil)

	// mock metadata
	metadataBase := "http://100.100.100.200/latest/meta-data"
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/private-ipv4s", metadataBase, mac),
		httpmock.NewStringResponder(200, fmt.Sprintf("[\"%s\",\"%s\",\"%s\"]", primaryIP, privateIP1, privateIP2)))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/", metadataBase),
		httpmock.NewStringResponder(200, mac+"/"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/vswitch-cidr-block", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.0/24"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/gateway", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.1"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/ipv6s", metadataBase, mac),
		httpmock.NewStringResponder(200, fmt.Sprintf("[%s,%s]", ipv6Addr1, ipv6Addr2)))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/vswitch-ipv6-cidr-block", metadataBase, mac),
		httpmock.NewStringResponder(200, "fd00::/64"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/ipv6-gateway", metadataBase, mac),
		httpmock.NewStringResponder(200, "fd00::1"))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

	cfg := &daemon.ENIConfig{
		EnableIPv4:       true,
		EnableIPv6:       true,
		InstanceID:       "instance-id",
		ZoneID:           "zone-id",
		VSwitchOptions:   []string{vswID},
		SecurityGroupIDs: []string{"sg-id"},
		ENITags:          map[string]string{"foo": "bar"},
	}

	vswPool, _ := vswpool.NewSwitchPool(100, "10m")

	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	eni, v4, v6, err := a.CreateNetworkInterface(2, 2, "Secondary")
	assert.NoError(t, err)
	assert.NotNil(t, eni)
	assert.Equal(t, eniID, eni.ID)
	assert.Equal(t, mac, eni.MAC)
	assert.Len(t, v4, 2)
	assert.Len(t, v6, 2)
}

func TestAliyun_AssignNIPv4(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecs := mockclient.NewECS(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	ip1 := "192.168.1.100"
	ip2 := "192.168.1.101"

	openAPI.On("GetECS").Return(ecs)

	ips := []netip.Addr{netip.MustParseAddr(ip1), netip.MustParseAddr(ip2)}
	ecs.On("AssignPrivateIPAddress", mock.Anything, mock.Anything).Return(ips, nil)

	metadataBase := "http://100.100.100.200/latest/meta-data"
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/private-ipv4s", metadataBase, mac),
		httpmock.NewStringResponder(200, fmt.Sprintf("[\"%s\",\"%s\"]", ip1, ip2)))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

	a := NewAliyun(context.Background(), openAPI, nil, nil, &daemon.ENIConfig{})
	assignedIPs, err := a.AssignNIPv4("eni-id", 2, mac)

	assert.NoError(t, err)
	assert.ElementsMatch(t, ips, assignedIPs)
}

func TestAliyun_AssignNIPv6(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecs := mockclient.NewECS(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	ip1 := "fd00::100"
	ip2 := "fd00::101"

	openAPI.On("GetECS").Return(ecs)

	ips := []netip.Addr{netip.MustParseAddr(ip1), netip.MustParseAddr(ip2)}
	ecs.On("AssignIpv6Addresses", mock.Anything, mock.Anything).Return(ips, nil)

	metadataBase := "http://100.100.100.200/latest/meta-data"
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/ipv6s", metadataBase, mac),
		httpmock.NewStringResponder(200, fmt.Sprintf("[%s,%s]", ip1, ip2)))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

	a := NewAliyun(context.Background(), openAPI, nil, nil, &daemon.ENIConfig{})
	assignedIPs, err := a.AssignNIPv6("eni-id", 2, mac)

	assert.NoError(t, err)
	assert.ElementsMatch(t, ips, assignedIPs)
}

func TestAliyun_UnAssignNIPv4(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecs := mockclient.NewECS(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	ips := []netip.Addr{netip.MustParseAddr("192.168.1.100")}

	openAPI.On("GetECS").Return(ecs)
	ecs.On("UnAssignPrivateIPAddresses", mock.Anything, "eni-id", ips).Return(nil)

	metadataBase := "http://100.100.100.200/latest/meta-data"
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/private-ipv4s", metadataBase, mac),
		httpmock.NewStringResponder(200, "[]"))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

	a := NewAliyun(context.Background(), openAPI, nil, nil, &daemon.ENIConfig{})
	err := a.UnAssignNIPv4("eni-id", ips, mac)

	assert.NoError(t, err)
}

func TestAliyun_UnAssignNIPv6(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecs := mockclient.NewECS(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	ips := []netip.Addr{netip.MustParseAddr("fd00::100")}

	openAPI.On("GetECS").Return(ecs)
	ecs.On("UnAssignIpv6Addresses", mock.Anything, "eni-id", ips).Return(nil)

	metadataBase := "http://100.100.100.200/latest/meta-data"
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/ipv6s", metadataBase, mac),
		httpmock.NewStringResponder(404, ""))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

	a := NewAliyun(context.Background(), openAPI, nil, nil, &daemon.ENIConfig{})
	err := a.UnAssignNIPv6("eni-id", ips, mac)

	assert.NoError(t, err)
}

func TestAliyun_DeleteNetworkInterface(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecs := mockclient.NewECS(t)

	openAPI.On("GetECS").Return(ecs)
	ecs.On("DetachNetworkInterface", mock.Anything, "eni-id", "instance-id", "").Return(nil)
	ecs.On("DeleteNetworkInterface", mock.Anything, "eni-id").Return(nil)

	a := NewAliyun(context.Background(), openAPI, nil, nil, &daemon.ENIConfig{InstanceID: "instance-id"})
	err := a.DeleteNetworkInterface("eni-id")

	assert.NoError(t, err)
}

func TestAliyun_LoadNetworkInterface(t *testing.T) {
	mac := "00:11:22:33:44:55"
	getter := &mockENIInfoGetter{mac: mac}

	a := NewAliyun(context.Background(), nil, getter, nil, &daemon.ENIConfig{EnableIPv4: true, EnableIPv6: true})
	v4, v6, err := a.LoadNetworkInterface(mac)

	assert.NoError(t, err)
	assert.Len(t, v4, 1)
	assert.Equal(t, "192.168.1.1", v4[0].String())
	assert.Len(t, v6, 1)
	assert.Equal(t, "fd00::1", v6[0].String())
}

func TestAliyun_GetAttachedNetworkInterface(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecs := mockclient.NewECS(t)

	mac := "00:11:22:33:44:55"
	eniID := "eni-id"
	getter := &mockENIInfoGetter{mac: mac}

	openAPI.On("GetECS").Return(ecs)
	describeResp := []*client.NetworkInterface{
		{
			NetworkInterfaceID:          eniID,
			Type:                        client.ENITypeTrunk,
			NetworkInterfaceTrafficMode: client.ENITrafficModeRDMA,
		},
	}
	ecs.On("DescribeNetworkInterface", mock.Anything, "", []string{eniID}, "", "", "", mock.Anything).Return(describeResp, nil)

	a := NewAliyun(context.Background(), openAPI, getter, nil, &daemon.ENIConfig{
		EniTypeAttr: daemon.FeatTrunk | daemon.FeatERDMA,
	})
	enis, err := a.GetAttachedNetworkInterface("")

	assert.NoError(t, err)
	assert.Len(t, enis, 1)
	assert.Equal(t, eniID, enis[0].ID)
	assert.True(t, enis[0].Trunk)
	assert.True(t, enis[0].ERdma)
}
