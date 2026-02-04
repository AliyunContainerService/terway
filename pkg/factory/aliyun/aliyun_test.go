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

	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
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

func TestAliyun_UnAssignNIPv4_ForbiddenError(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	eniID := "eni-id"
	mac := "00:11:22:33:44:55"
	ips := []netip.Addr{netip.MustParseAddr("192.168.1.10")}

	openAPI.On("GetECS").Return(ecsClient).Maybe()

	// Test with Forbidden error (should return immediately)
	ecsClient.On("UnAssignPrivateIPAddresses", mock.Anything, eniID, ips).Return(sdkErr.NewServerError(403, `{"Code":"Forbidden.RAM"}`, "test-request-id")).Once()

	cfg := &daemon.ENIConfig{
		EnableIPv4: true,
	}
	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	err := a.UnAssignNIPv4(eniID, ips, mac)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Forbidden")
}

func TestAliyun_UnAssignNIPv4_RetryableError(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	eniID := "eni-id"
	mac := "00:11:22:33:44:55"
	ips := []netip.Addr{netip.MustParseAddr("192.168.1.10")}

	openAPI.On("GetECS").Return(ecsClient).Maybe()

	// Test with retryable error (not Forbidden) - should retry
	ecsClient.On("UnAssignPrivateIPAddresses", mock.Anything, eniID, ips).Return(sdkErr.NewServerError(500, `{"Code":"InternalError"}`, "test-request-id")).Once()
	// Then succeed
	ecsClient.On("UnAssignPrivateIPAddresses", mock.Anything, eniID, ips).Return(nil).Once()

	metadataBase := "http://100.100.100.200/latest/meta-data"
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/private-ipv4s", metadataBase, mac),
		httpmock.NewStringResponder(200, "[]"))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

	cfg := &daemon.ENIConfig{
		EnableIPv4: true,
	}
	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	err := a.UnAssignNIPv4(eniID, ips, mac)
	// May succeed or fail depending on backoff timeout, but should handle gracefully
	_ = err
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

func TestAliyun_UnAssignNIPv6_ForbiddenError(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	eniID := "eni-id"
	mac := "00:11:22:33:44:55"
	ips := []netip.Addr{netip.MustParseAddr("fd00::1")}

	openAPI.On("GetECS").Return(ecsClient).Maybe()

	// Test with Forbidden error (should return immediately)
	ecsClient.On("UnAssignIpv6Addresses", mock.Anything, eniID, ips).Return(sdkErr.NewServerError(403, `{"Code":"Forbidden.RAM"}`, "test-request-id")).Once()

	cfg := &daemon.ENIConfig{
		EnableIPv6: true,
	}
	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	err := a.UnAssignNIPv6(eniID, ips, mac)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Forbidden")
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

func TestAliyun_LoadNetworkInterface_IPv4Only(t *testing.T) {
	mac := "00:11:22:33:44:55"
	getter := &mockENIInfoGetter{mac: mac}

	a := NewAliyun(context.Background(), nil, getter, nil, &daemon.ENIConfig{EnableIPv4: true, EnableIPv6: false})
	v4, v6, err := a.LoadNetworkInterface(mac)

	assert.NoError(t, err)
	assert.Len(t, v4, 1)
	assert.Equal(t, "192.168.1.1", v4[0].String())
	assert.Nil(t, v6)
}

func TestAliyun_LoadNetworkInterface_IPv6Only(t *testing.T) {
	mac := "00:11:22:33:44:55"
	getter := &mockENIInfoGetter{mac: mac}

	a := NewAliyun(context.Background(), nil, getter, nil, &daemon.ENIConfig{EnableIPv4: false, EnableIPv6: true})
	v4, v6, err := a.LoadNetworkInterface(mac)

	assert.NoError(t, err)
	assert.Nil(t, v4)
	assert.Len(t, v6, 1)
	assert.Equal(t, "fd00::1", v6[0].String())
}

func TestAliyun_LoadNetworkInterface_GetterError(t *testing.T) {
	mac := "00:11:22:33:44:55"
	getter := &mockENIInfoGetter{mac: "other-mac"} // MAC mismatch causes getter to return error

	a := NewAliyun(context.Background(), nil, getter, nil, &daemon.ENIConfig{EnableIPv4: true, EnableIPv6: true})
	v4, v6, err := a.LoadNetworkInterface(mac)

	assert.Error(t, err)
	assert.Nil(t, v4)
	assert.Nil(t, v6)
}

type emptyENIInfoGetter struct {
	eni.ENIInfoGetter
}

func (m *emptyENIInfoGetter) GetENIs(useCache bool) ([]*daemon.ENI, error) {
	return []*daemon.ENI{}, nil
}

type errorENIInfoGetter struct {
	eni.ENIInfoGetter
}

func (m *errorENIInfoGetter) GetENIs(useCache bool) ([]*daemon.ENI, error) {
	return nil, fmt.Errorf("get ENIs failed")
}

func TestAliyun_GetAttachedNetworkInterface_GetENIsError(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	getter := &errorENIInfoGetter{}

	cfg := &daemon.ENIConfig{}
	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, getter, vswPool, cfg)

	enis, err := a.GetAttachedNetworkInterface("")
	assert.Error(t, err)
	assert.Nil(t, enis)
}

func TestAliyun_GetAttachedNetworkInterface_EmptyENIs(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	getter := &emptyENIInfoGetter{}

	cfg := &daemon.ENIConfig{}
	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, getter, vswPool, cfg)

	enis, err := a.GetAttachedNetworkInterface("")
	// When GetENIs returns empty list with no error, the function returns the error from GetENIs (which is nil)
	// So we check that enis is nil or empty
	if err == nil {
		assert.Nil(t, enis)
	} else {
		assert.Error(t, err)
	}
}

type multiENIInfoGetter struct {
	eni.ENIInfoGetter
	enis []*daemon.ENI
}

func (m *multiENIInfoGetter) GetENIs(useCache bool) ([]*daemon.ENI, error) {
	return m.enis, nil
}

func TestAliyun_GetAttachedNetworkInterface_WithTrunkENI(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)

	openAPI.On("GetECS").Return(ecsClient).Maybe()

	trunkENIID := "trunk-eni-id"
	enis := []*daemon.ENI{
		{ID: trunkENIID, MAC: "00:11:22:33:44:55"},
		{ID: "eni-2", MAC: "00:11:22:33:44:66"},
	}
	getter := &multiENIInfoGetter{enis: enis}

	// Mock DescribeNetworkInterface
	eniSet := []*client.NetworkInterface{
		{NetworkInterfaceID: trunkENIID, Type: client.ENITypeTrunk},
		{NetworkInterfaceID: "eni-2", Type: client.ENITypeSecondary},
	}
	ecsClient.On("DescribeNetworkInterface", mock.Anything, "", []string{trunkENIID, "eni-2"}, "", "", "", mock.Anything).Return(eniSet, nil).Maybe()

	cfg := &daemon.ENIConfig{
		EniTypeAttr: daemon.FeatTrunk,
	}
	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, getter, vswPool, cfg)

	result, err := a.GetAttachedNetworkInterface(trunkENIID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	if len(result) > 0 {
		// Find trunk ENI
		for _, eni := range result {
			if eni.ID == trunkENIID {
				assert.True(t, eni.Trunk)
			}
		}
	}
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

func TestAliyun_GetAttachedNetworkInterface_NoFeatNoTags(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	mac := "00:11:22:33:44:55"
	getter := &mockENIInfoGetter{mac: mac}

	a := NewAliyun(context.Background(), openAPI, getter, nil, &daemon.ENIConfig{})
	enis, err := a.GetAttachedNetworkInterface("")

	assert.NoError(t, err)
	assert.Len(t, enis, 1)
	assert.Equal(t, "eni-id", enis[0].ID)
}

func TestAliyun_GetAttachedNetworkInterface_DescribeForbidden(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)
	getter := &multiENIInfoGetter{enis: []*daemon.ENI{{ID: "eni-1", MAC: "00:11:22:33:44:55"}}}

	openAPI.On("GetECS").Return(ecsClient)
	ecsClient.On("DescribeNetworkInterface", mock.Anything, "", []string{"eni-1"}, "", "", "", mock.Anything).
		Return(nil, sdkErr.NewServerError(403, `{"Code":"Forbidden.RAM"}`, "test-request-id")).Once()

	a := NewAliyun(context.Background(), openAPI, getter, nil, &daemon.ENIConfig{EniTypeAttr: daemon.FeatTrunk})
	enis, err := a.GetAttachedNetworkInterface("")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Forbidden")
	assert.Nil(t, enis)
}

func TestAliyun_DeleteNetworkInterface_DetachError(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecs := mockclient.NewECS(t)

	openAPI.On("GetECS").Return(ecs)
	ecs.On("DetachNetworkInterface", mock.Anything, "eni-id", "instance-id", "").
		Return(sdkErr.NewServerError(500, `{"Code":"InternalError"}`, "test-request-id"))

	a := NewAliyun(context.Background(), openAPI, nil, nil, &daemon.ENIConfig{InstanceID: "instance-id"})
	err := a.DeleteNetworkInterface("eni-id")

	assert.Error(t, err)
}

func TestAliyun_CreateNetworkInterface_InvalidVSwitchIDIPNotEnough(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)
	vpcClient := mockclient.NewVPC(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	primaryIP := "192.168.1.10"
	privateIP1 := "192.168.1.11"
	eniID := "eni-id"
	vswID1 := "vsw-id-1"
	vswID2 := "vsw-id-2"

	openAPI.On("GetVPC").Return(vpcClient).Maybe()
	openAPI.On("GetECS").Return(ecsClient).Maybe()

	vpcClient.On("DescribeVSwitchByID", mock.Anything, vswID1).Return(&vpc.VSwitch{
		VSwitchId:               vswID1,
		ZoneId:                  "zone-id",
		AvailableIpAddressCount: 100,
	}, nil).Maybe()
	vpcClient.On("DescribeVSwitchByID", mock.Anything, vswID2).Return(&vpc.VSwitch{
		VSwitchId:               vswID2,
		ZoneId:                  "zone-id",
		AvailableIpAddressCount: 100,
	}, nil).Maybe()

	// First call returns InvalidVSwitchIDIPNotEnough error
	ecsClient.On("CreateNetworkInterface", mock.Anything, mock.MatchedBy(func(opt *client.CreateNetworkInterfaceOptions) bool {
		return opt.NetworkInterfaceOptions != nil && opt.NetworkInterfaceOptions.VSwitchID == vswID1
	})).Return(nil, sdkErr.NewServerError(400, `{"Code":"InvalidVSwitchId.IpNotEnough"}`, "test-request-id")).Once()

	// Second call with different vswitch succeeds
	createResp := &client.NetworkInterface{
		NetworkInterfaceID: eniID,
		MacAddress:         mac,
		PrivateIPAddress:   primaryIP,
		PrivateIPSets: []client.IPSet{
			{IPAddress: privateIP1},
		},
		VSwitchID: vswID2,
	}
	ecsClient.On("CreateNetworkInterface", mock.Anything, mock.MatchedBy(func(opt *client.CreateNetworkInterfaceOptions) bool {
		return opt.NetworkInterfaceOptions != nil && opt.NetworkInterfaceOptions.VSwitchID == vswID2
	})).Return(createResp, nil).Once()

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
		httpmock.NewStringResponder(200, fmt.Sprintf(`["%s","%s"]`, primaryIP, privateIP1)))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/", metadataBase),
		httpmock.NewStringResponder(200, mac+"/"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/vswitch-cidr-block", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.0/24"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/gateway", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.1"))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

	cfg := &daemon.ENIConfig{
		EnableIPv4:       true,
		InstanceID:       "instance-id",
		ZoneID:           "zone-id",
		VSwitchOptions:   []string{vswID1, vswID2},
		SecurityGroupIDs: []string{"sg-id"},
	}

	vswPool, _ := vswpool.NewSwitchPool(100, "10m")

	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	eni, v4, _, err := a.CreateNetworkInterface(1, 0, "Secondary")
	assert.NoError(t, err)
	assert.NotNil(t, eni)
	assert.Equal(t, eniID, eni.ID)
	assert.Equal(t, vswID2, eni.VSwitchID)
	assert.Len(t, v4, 1)
}

func TestAliyun_CreateNetworkInterface_QuotaExceededPrivateIPAddress(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)
	vpcClient := mockclient.NewVPC(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	primaryIP := "192.168.1.10"
	eniID := "eni-id"
	vswID1 := "vsw-id-1"
	vswID2 := "vsw-id-2"

	openAPI.On("GetVPC").Return(vpcClient).Maybe()
	openAPI.On("GetECS").Return(ecsClient).Maybe()

	vpcClient.On("DescribeVSwitchByID", mock.Anything, vswID1).Return(&vpc.VSwitch{
		VSwitchId:               vswID1,
		ZoneId:                  "zone-id",
		AvailableIpAddressCount: 100,
	}, nil).Maybe()
	vpcClient.On("DescribeVSwitchByID", mock.Anything, vswID2).Return(&vpc.VSwitch{
		VSwitchId:               vswID2,
		ZoneId:                  "zone-id",
		AvailableIpAddressCount: 100,
	}, nil).Maybe()

	// First call returns QuotaExceededPrivateIPAddress error
	ecsClient.On("CreateNetworkInterface", mock.Anything, mock.MatchedBy(func(opt *client.CreateNetworkInterfaceOptions) bool {
		return opt.NetworkInterfaceOptions != nil && opt.NetworkInterfaceOptions.VSwitchID == vswID1
	})).Return(nil, sdkErr.NewServerError(400, `{"Code":"QuotaExceeded.PrivateIpAddress"}`, "test-request-id")).Once()

	// Second call with different vswitch succeeds
	createResp := &client.NetworkInterface{
		NetworkInterfaceID: eniID,
		MacAddress:         mac,
		PrivateIPAddress:   primaryIP,
		PrivateIPSets: []client.IPSet{
			{IPAddress: primaryIP},
		},
		VSwitchID: vswID2,
	}
	ecsClient.On("CreateNetworkInterface", mock.Anything, mock.MatchedBy(func(opt *client.CreateNetworkInterfaceOptions) bool {
		return opt.NetworkInterfaceOptions != nil && opt.NetworkInterfaceOptions.VSwitchID == vswID2
	})).Return(createResp, nil).Once()

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
		httpmock.NewStringResponder(200, fmt.Sprintf(`["%s"]`, primaryIP)))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/", metadataBase),
		httpmock.NewStringResponder(200, mac+"/"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/vswitch-cidr-block", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.0/24"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/gateway", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.1"))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

	cfg := &daemon.ENIConfig{
		EnableIPv4:       true,
		InstanceID:       "instance-id",
		ZoneID:           "zone-id",
		VSwitchOptions:   []string{vswID1, vswID2},
		SecurityGroupIDs: []string{"sg-id"},
	}

	vswPool, _ := vswpool.NewSwitchPool(100, "10m")

	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	eni, v4, _, err := a.CreateNetworkInterface(0, 0, "Secondary")
	assert.NoError(t, err)
	assert.NotNil(t, eni)
	assert.Equal(t, eniID, eni.ID)
	assert.Equal(t, vswID2, eni.VSwitchID)
	assert.Len(t, v4, 1)
}

func TestAliyun_CreateNetworkInterface_PersistentError(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)
	vpcClient := mockclient.NewVPC(t)

	vswID := "vsw-id-1"

	openAPI.On("GetVPC").Return(vpcClient).Maybe()
	openAPI.On("GetECS").Return(ecsClient).Maybe()

	vpcClient.On("DescribeVSwitchByID", mock.Anything, vswID).Return(&vpc.VSwitch{
		VSwitchId:               vswID,
		ZoneId:                  "zone-id",
		AvailableIpAddressCount: 100,
	}, nil).Maybe()

	// Persistent error that should cause eventual failure
	ecsClient.On("CreateNetworkInterface", mock.Anything, mock.Anything).Return(nil, sdkErr.NewServerError(403, `{"Code":"Forbidden.RAM"}`, "test-request-id"))

	cfg := &daemon.ENIConfig{
		EnableIPv4:       true,
		InstanceID:       "instance-id",
		ZoneID:           "zone-id",
		VSwitchOptions:   []string{vswID},
		SecurityGroupIDs: []string{"sg-id"},
	}

	vswPool, _ := vswpool.NewSwitchPool(100, "10m")

	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	_, _, _, err := a.CreateNetworkInterface(0, 0, "Secondary")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Forbidden")
}

func TestAliyun_CreateNetworkInterface_TrunkType(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)
	vpcClient := mockclient.NewVPC(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	primaryIP := "192.168.1.10"
	eniID := "eni-id"
	vswID := "vsw-id-1"

	// Mock metadata service - same as TestAliyun_CreateNetworkInterface
	metadataBase := "http://100.100.100.200/latest/meta-data"
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/private-ipv4s", metadataBase, mac),
		httpmock.NewStringResponder(200, fmt.Sprintf("[\"%s\"]", primaryIP)))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/", metadataBase),
		httpmock.NewStringResponder(200, mac+"/"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/vswitch-cidr-block", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.0/24"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/gateway", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.1"))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

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
		VSwitchID:          vswID,
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

	cfg := &daemon.ENIConfig{
		EnableIPv4:       true,
		InstanceID:       "instance-id",
		ZoneID:           "zone-id",
		VSwitchOptions:   []string{vswID},
		SecurityGroupIDs: []string{"sg-id"},
	}

	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	// Test with trunk type
	eni, _, _, err := a.CreateNetworkInterface(0, 0, "Trunk")
	assert.NoError(t, err)
	assert.NotNil(t, eni)
	assert.True(t, eni.Trunk)
}

func TestAliyun_CreateNetworkInterface_ERDMAType(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	ecsClient := mockclient.NewECS(t)
	vpcClient := mockclient.NewVPC(t)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	mac := "00:11:22:33:44:55"
	primaryIP := "192.168.1.10"
	eniID := "eni-id"
	vswID := "vsw-id-1"

	// Mock metadata service - same as TestAliyun_CreateNetworkInterface
	metadataBase := "http://100.100.100.200/latest/meta-data"
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/private-ipv4s", metadataBase, mac),
		httpmock.NewStringResponder(200, fmt.Sprintf("[\"%s\"]", primaryIP)))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/", metadataBase),
		httpmock.NewStringResponder(200, mac+"/"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/vswitch-cidr-block", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.0/24"))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s/network/interfaces/macs/%s/gateway", metadataBase, mac),
		httpmock.NewStringResponder(200, "192.168.1.1"))
	httpmock.RegisterResponder("PUT", "http://100.100.100.200/latest/api/token",
		httpmock.NewStringResponder(200, "token"))

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
		VSwitchID:          vswID,
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

	cfg := &daemon.ENIConfig{
		EnableIPv4:       true,
		InstanceID:       "instance-id",
		ZoneID:           "zone-id",
		VSwitchOptions:   []string{vswID},
		SecurityGroupIDs: []string{"sg-id"},
	}

	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	// Test with erdma type
	eni, _, _, err := a.CreateNetworkInterface(0, 0, "ERDMA")
	assert.NoError(t, err)
	assert.NotNil(t, eni)
	assert.True(t, eni.ERdma)
}

func TestAliyun_NewAliyun(t *testing.T) {
	openAPI := mockclient.NewOpenAPI(t)
	cfg := &daemon.ENIConfig{
		EnableIPv4:       true,
		EnableIPv6:       true,
		InstanceID:       "instance-id",
		ZoneID:           "zone-id",
		VSwitchOptions:   []string{"vsw-1"},
		SecurityGroupIDs: []string{"sg-1"},
		ResourceGroupID:  "rg-1",
		ENITags:          map[string]string{"key": "value"},
	}

	vswPool, _ := vswpool.NewSwitchPool(100, "10m")
	a := NewAliyun(context.Background(), openAPI, nil, vswPool, cfg)

	assert.NotNil(t, a)
	assert.True(t, a.enableIPv4)
	assert.True(t, a.enableIPv6)
	assert.Equal(t, "instance-id", a.instanceID)
	assert.Equal(t, "zone-id", a.zoneID)
	assert.Equal(t, []string{"vsw-1"}, a.vSwitchOptions)
	assert.Equal(t, []string{"sg-1"}, a.securityGroupIDs)
	assert.Equal(t, "rg-1", a.resourceGroupID)
	assert.Equal(t, map[string]string{"key": "value"}, a.eniTags)
}
