package client

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
)

func TestNewAPIFacade(t *testing.T) {
	clientSet := &credential.ClientSet{}
	limitConfig := LimitConfig{
		ECS:  RateLimitConfig{QPS: 10, Burst: 20},
		VPC:  RateLimitConfig{QPS: 10, Burst: 20},
		EFLO: RateLimitConfig{QPS: 10, Burst: 20},
	}

	facade := NewAPIFacade(clientSet, limitConfig)

	assert.NotNil(t, facade)
	assert.NotNil(t, facade.ecsService)
	assert.NotNil(t, facade.vpcService)
	assert.NotNil(t, facade.efloService)
	assert.NotNil(t, facade.efloControlService)
}

func TestAPIFacade_GetServices(t *testing.T) {
	clientSet := &credential.ClientSet{}
	limitConfig := LimitConfig{}
	facade := NewAPIFacade(clientSet, limitConfig)

	assert.NotNil(t, facade.GetECS())
	assert.NotNil(t, facade.GetVPC())
	assert.NotNil(t, facade.GetEFLO())
	assert.NotNil(t, facade.GetEFLOController())
}

func TestAPIFacade_CreateNetworkInterface(t *testing.T) {
	mockECS := mocks.NewECS(t)
	facade := &APIFacade{
		ecsService: mockECS,
	}

	ctx := context.Background()
	expectedNI := &NetworkInterface{
		NetworkInterfaceID: "eni-test",
		Status:             "Available",
	}

	mockECS.On("CreateNetworkInterface", ctx, mock.AnythingOfType("CreateNetworkInterfaceOption")).
		Return(expectedNI, nil)

	ni, err := facade.CreateNetworkInterface(ctx)

	assert.NoError(t, err)
	assert.Equal(t, expectedNI, ni)
	mockECS.AssertExpectations(t)
}

func TestAPIFacade_DeleteNetworkInterface(t *testing.T) {
	mockECS := mocks.NewECS(t)
	facade := &APIFacade{
		ecsService: mockECS,
	}

	ctx := context.Background()
	eniID := "eni-test"

	mockECS.On("DeleteNetworkInterface", ctx, eniID).Return(nil)

	err := facade.DeleteNetworkInterface(ctx, eniID)

	assert.NoError(t, err)
	mockECS.AssertExpectations(t)
}

func TestAPIFacade_DescribeNetworkInterface(t *testing.T) {
	mockECS := mocks.NewECS(t)
	facade := &APIFacade{
		ecsService: mockECS,
	}

	ctx := context.Background()
	expectedNIs := []*NetworkInterface{
		{
			NetworkInterfaceID: "eni-test1",
			Status:             "Available",
		},
		{
			NetworkInterfaceID: "eni-test2",
			Status:             "InUse",
		},
	}

	mockECS.On("DescribeNetworkInterface", ctx, mock.AnythingOfType("DescribeNetworkInterfaceOption")).
		Return(expectedNIs, nil)

	nis, err := facade.DescribeNetworkInterface(ctx)

	assert.NoError(t, err)
	assert.Equal(t, expectedNIs, nis)
	mockECS.AssertExpectations(t)
}

func TestAPIFacade_AttachNetworkInterface(t *testing.T) {
	mockECS := mocks.NewECS(t)
	facade := &APIFacade{
		ecsService: mockECS,
	}

	ctx := context.Background()

	mockECS.On("AttachNetworkInterface", ctx, mock.AnythingOfType("AttachNetworkInterfaceOption")).
		Return(nil)

	err := facade.AttachNetworkInterface(ctx)

	assert.NoError(t, err)
	mockECS.AssertExpectations(t)
}

func TestAPIFacade_DetachNetworkInterface(t *testing.T) {
	mockECS := mocks.NewECS(t)
	facade := &APIFacade{
		ecsService: mockECS,
	}

	ctx := context.Background()

	mockECS.On("DetachNetworkInterface", ctx, mock.AnythingOfType("DetachNetworkInterfaceOption")).
		Return(nil)

	err := facade.DetachNetworkInterface(ctx)

	assert.NoError(t, err)
	mockECS.AssertExpectations(t)
}

func TestAPIFacade_AssignPrivateIPAddress(t *testing.T) {
	mockECS := mocks.NewECS(t)
	facade := &APIFacade{
		ecsService: mockECS,
	}

	ctx := context.Background()
	expectedIPs := []netip.Addr{netip.MustParseAddr("10.0.0.1")}

	mockECS.On("AssignPrivateIPAddress", ctx, mock.AnythingOfType("AssignPrivateIPAddressOption")).
		Return(expectedIPs, nil)

	ips, err := facade.AssignPrivateIPAddress(ctx)

	assert.NoError(t, err)
	assert.Equal(t, expectedIPs, ips)
	mockECS.AssertExpectations(t)
}

func TestAPIFacade_UnAssignPrivateIPAddress(t *testing.T) {
	mockECS := mocks.NewECS(t)
	facade := &APIFacade{
		ecsService: mockECS,
	}

	ctx := context.Background()

	mockECS.On("UnAssignPrivateIPAddress", ctx, mock.AnythingOfType("UnAssignPrivateIPAddressOption")).
		Return(nil)

	err := facade.UnAssignPrivateIPAddress(ctx)

	assert.NoError(t, err)
	mockECS.AssertExpectations(t)
}

func TestAPIFacade_ModifyNetworkInterfaceAttribute(t *testing.T) {
	mockECS := mocks.NewECS(t)
	facade := &APIFacade{
		ecsService: mockECS,
	}

	ctx := context.Background()

	mockECS.On("ModifyNetworkInterfaceAttribute", ctx, mock.AnythingOfType("ModifyNetworkInterfaceAttributeOption")).
		Return(nil)

	err := facade.ModifyNetworkInterfaceAttribute(ctx)

	assert.NoError(t, err)
	mockECS.AssertExpectations(t)
}

func TestAPIFacade_CreateVSwitch(t *testing.T) {
	mockVPC := mocks.NewVPC(t)
	facade := &APIFacade{
		vpcService: mockVPC,
	}

	ctx := context.Background()
	expectedVSwitchID := "vsw-test"

	mockVPC.On("CreateVSwitch", ctx, mock.AnythingOfType("CreateVSwitchOption")).
		Return(expectedVSwitchID, nil)

	vswID, err := facade.CreateVSwitch(ctx)

	assert.NoError(t, err)
	assert.Equal(t, expectedVSwitchID, vswID)
	mockVPC.AssertExpectations(t)
}

func TestAPIFacade_DescribeVSwitchByID(t *testing.T) {
	mockVPC := mocks.NewVPC(t)
	facade := &APIFacade{
		vpcService: mockVPC,
	}

	ctx := context.Background()
	vswID := "vsw-test"
	expectedVSw := &VSwitch{
		VSwitchID: vswID,
		Status:    "Available",
	}

	mockVPC.On("DescribeVSwitchByID", ctx, vswID).Return(expectedVSw, nil)

	vsw, err := facade.DescribeVSwitchByID(ctx, vswID)

	assert.NoError(t, err)
	assert.Equal(t, expectedVSw, vsw)
	mockVPC.AssertExpectations(t)
}

func TestAPIFacade_DescribeVPC(t *testing.T) {
	mockVPC := mocks.NewVPC(t)
	facade := &APIFacade{
		vpcService: mockVPC,
	}

	ctx := context.Background()
	vpcID := "vpc-test"
	expectedVPC := &VPC{
		VPCID:  vpcID,
		Status: "Available",
	}

	mockVPC.On("DescribeVPC", ctx, vpcID).Return(expectedVPC, nil)

	vpc, err := facade.DescribeVPC(ctx, vpcID)

	assert.NoError(t, err)
	assert.Equal(t, expectedVPC, vpc)
	mockVPC.AssertExpectations(t)
}