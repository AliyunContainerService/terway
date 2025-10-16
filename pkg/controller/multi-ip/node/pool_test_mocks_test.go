package node

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/util/wait"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
)

// MockAPIHelper provides a fluent API for setting up common mock patterns
// for Aliyun API calls in tests. It wraps the mock objects and provides
// convenient methods to configure expected behaviors.
//
// Example usage:
//
//	mockHelper := NewMockAPIHelper().
//	    SetupDescribeENI("i-test", []*aliyunClient.NetworkInterface{...}).
//	    SetupCreateENI("eni-1", aliyunClient.ENITypeSecondary).
//	    SetupVSwitch("vsw-1", &vpc.VSwitch{...})
//
//	openAPI, vpcClient, ecsClient := mockHelper.GetMocks()
type MockAPIHelper struct {
	openAPI   *mocks.OpenAPI
	vpcClient *mocks.VPC
	ecsClient *mocks.ECS
}

// NewMockAPIHelper creates a new MockAPIHelper.
// The returned helper has fresh mock objects ready for configuration.
func NewMockAPIHelper() *MockAPIHelper {
	return &MockAPIHelper{}
}

// TestingTWithCleanup is an interface that combines mock.TestingT with Cleanup method.
// This is what GinkgoT() and *testing.T implement.
type TestingTWithCleanup interface {
	mock.TestingT
	Cleanup(func())
}

// NewMockAPIHelperWithT creates a new MockAPIHelper with a test interface.
// This is useful when you need to use Ginkgo's GinkgoT() or a testing.T.
func NewMockAPIHelperWithT(t TestingTWithCleanup) *MockAPIHelper {
	helper := &MockAPIHelper{}
	helper.initMocksWithT(t)
	return helper
}

// initMocksWithT initializes the mock objects with a test interface.
func (m *MockAPIHelper) initMocksWithT(t TestingTWithCleanup) {
	m.openAPI = mocks.NewOpenAPI(t)
	m.vpcClient = mocks.NewVPC(t)
	m.ecsClient = mocks.NewECS(t)

	// Setup basic expectations
	m.openAPI.On("GetVPC").Return(m.vpcClient).Maybe()
	m.openAPI.On("GetECS").Return(m.ecsClient).Maybe()
}

// initMocks initializes the mock objects without a test interface.
func (m *MockAPIHelper) initMocks() {
	m.openAPI = &mocks.OpenAPI{}
	m.vpcClient = &mocks.VPC{}
	m.ecsClient = &mocks.ECS{}

	// Setup basic expectations
	m.openAPI.On("GetVPC").Return(m.vpcClient).Maybe()
	m.openAPI.On("GetECS").Return(m.ecsClient).Maybe()
}

// GetMocks returns the underlying mock objects for direct manipulation if needed.
func (m *MockAPIHelper) GetMocks() (*mocks.OpenAPI, *mocks.VPC, *mocks.ECS) {
	if m.openAPI == nil {
		m.initMocks()
	}
	return m.openAPI, m.vpcClient, m.ecsClient
}

// SetupDescribeENI configures a mock response for DescribeNetworkInterfaceV2.
func (m *MockAPIHelper) SetupDescribeENI(instanceID string, enis []*aliyunClient.NetworkInterface) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
		InstanceID: &instanceID,
	}).Return(enis, nil).Maybe()
	return m
}

// SetupDescribeENIWithOptions configures a mock response for DescribeNetworkInterfaceV2 with custom options.
func (m *MockAPIHelper) SetupDescribeENIWithOptions(opts *aliyunClient.DescribeNetworkInterfaceOptions, enis []*aliyunClient.NetworkInterface, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, opts).Return(enis, err).Maybe()
	return m
}

// MockENIOption is a function that configures ENI creation parameters.
type MockENIOption func(*aliyunClient.NetworkInterface)

// WithIPv4 adds an IPv4 address to the mock ENI.
func WithIPv4(ip string) MockENIOption {
	return func(eni *aliyunClient.NetworkInterface) {
		eni.PrivateIPSets = append(eni.PrivateIPSets, aliyunClient.IPSet{
			IPAddress: ip,
			Primary:   len(eni.PrivateIPSets) == 0,
		})
		if eni.PrivateIPAddress == "" {
			eni.PrivateIPAddress = ip
		}
	}
}

// WithIPv6 adds an IPv6 address to the mock ENI.
func WithIPv6(ip string) MockENIOption {
	return func(eni *aliyunClient.NetworkInterface) {
		eni.IPv6Set = append(eni.IPv6Set, aliyunClient.IPSet{
			IPAddress: ip,
			Primary:   len(eni.IPv6Set) == 0,
		})
	}
}

// WithMacAddress sets the MAC address for the mock ENI.
func WithMacAddress(mac string) MockENIOption {
	return func(eni *aliyunClient.NetworkInterface) {
		eni.MacAddress = mac
	}
}

// WithVSwitchID sets the vSwitch ID for the mock ENI.
func WithVSwitchID(vswID string) MockENIOption {
	return func(eni *aliyunClient.NetworkInterface) {
		eni.VSwitchID = vswID
	}
}

// WithZoneID sets the zone ID for the mock ENI.
func WithZoneID(zoneID string) MockENIOption {
	return func(eni *aliyunClient.NetworkInterface) {
		eni.ZoneID = zoneID
	}
}

// WithStatus sets the status for the mock ENI.
func WithStatus(status string) MockENIOption {
	return func(eni *aliyunClient.NetworkInterface) {
		eni.Status = status
	}
}

// WithTrafficMode sets the traffic mode for the mock ENI.
func WithTrafficMode(mode string) MockENIOption {
	return func(eni *aliyunClient.NetworkInterface) {
		eni.NetworkInterfaceTrafficMode = mode
	}
}

// SetupCreateENI configures a mock for CreateNetworkInterfaceV2.
func (m *MockAPIHelper) SetupCreateENI(eniID, eniType string, opts ...MockENIOption) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}

	eni := &aliyunClient.NetworkInterface{
		NetworkInterfaceID:          eniID,
		Type:                        eniType,
		NetworkInterfaceTrafficMode: "Standard",
		Status:                      aliyunClient.ENIStatusInUse,
	}

	// Apply options
	for _, opt := range opts {
		opt(eni)
	}

	m.openAPI.On("CreateNetworkInterfaceV2", mock.Anything, mock.Anything, mock.Anything).
		Return(eni, nil).Maybe()
	return m
}

// SetupCreateENIWithError configures a mock for CreateNetworkInterfaceV2 that returns an error.
func (m *MockAPIHelper) SetupCreateENIWithError(err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("CreateNetworkInterfaceV2", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, err).Maybe()
	return m
}

// SetupDeleteENI configures a mock for DeleteNetworkInterfaceV2.
func (m *MockAPIHelper) SetupDeleteENI(eniID string, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("DeleteNetworkInterfaceV2", mock.Anything, eniID).Return(err).Maybe()
	return m
}

// SetupAttachENI configures a mock for AttachNetworkInterface.
func (m *MockAPIHelper) SetupAttachENI(eniID, instanceID string, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.ecsClient.On("AttachNetworkInterface", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.AttachNetworkInterfaceOptions) bool {
		return opts.NetworkInterfaceID != nil && *opts.NetworkInterfaceID == eniID &&
			opts.InstanceID != nil && *opts.InstanceID == instanceID
	})).Return(err).Maybe()
	return m
}

// SetupDetachENI configures a mock for DetachNetworkInterface.
func (m *MockAPIHelper) SetupDetachENI(eniID, instanceID string, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.ecsClient.On("DetachNetworkInterface", mock.Anything, eniID, instanceID, mock.Anything).Return(err).Maybe()
	return m
}

// SetupAssignIP configures a mock for AssignPrivateIPAddressV2.
func (m *MockAPIHelper) SetupAssignIP(eniID string, ips []aliyunClient.IPSet, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("AssignPrivateIPAddressV2", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.AssignPrivateIPAddressOptions) bool {
		return opts.NetworkInterfaceOptions != nil && opts.NetworkInterfaceOptions.NetworkInterfaceID == eniID
	})).Return(ips, err).Maybe()
	return m
}

// SetupUnassignIP configures a mock for UnAssignPrivateIPAddressesV2.
func (m *MockAPIHelper) SetupUnassignIP(eniID string, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("UnAssignPrivateIPAddressesV2", mock.Anything, eniID, mock.Anything).Return(err).Maybe()
	return m
}

// SetupAssignIPv6 configures a mock for AssignIpv6AddressesV2.
func (m *MockAPIHelper) SetupAssignIPv6(eniID string, ips []aliyunClient.IPSet, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("AssignIpv6AddressesV2", mock.Anything, mock.MatchedBy(func(opts *aliyunClient.AssignIPv6AddressesOptions) bool {
		return opts.NetworkInterfaceOptions != nil && opts.NetworkInterfaceOptions.NetworkInterfaceID == eniID
	})).Return(ips, err).Maybe()
	return m
}

// SetupUnassignIPv6 configures a mock for UnAssignIpv6AddressesV2.
func (m *MockAPIHelper) SetupUnassignIPv6(eniID string, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("UnAssignIpv6AddressesV2", mock.Anything, eniID, mock.Anything).Return(err).Maybe()
	return m
}

// SetupVSwitch configures a mock for DescribeVSwitchByID.
func (m *MockAPIHelper) SetupVSwitch(vswID string, vsw *vpc.VSwitch) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.vpcClient.On("DescribeVSwitchByID", mock.Anything, vswID).Return(vsw, nil).Maybe()
	return m
}

// SetupVSwitchWithError configures a mock for DescribeVSwitchByID that returns an error.
func (m *MockAPIHelper) SetupVSwitchWithError(vswID string, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.vpcClient.On("DescribeVSwitchByID", mock.Anything, vswID).Return(nil, err).Maybe()
	return m
}

// SetupWaitForENI configures a mock for WaitForNetworkInterfaceV2.
func (m *MockAPIHelper) SetupWaitForENI(eniID, status string, result *aliyunClient.NetworkInterface, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("WaitForNetworkInterfaceV2", mock.Anything, eniID, status, mock.Anything, mock.Anything).
		Return(result, err).Maybe()
	return m
}

// SetupWaitForENIWithBackoff configures a mock for WaitForNetworkInterfaceV2 with specific backoff.
func (m *MockAPIHelper) SetupWaitForENIWithBackoff(eniID, status string, bo wait.Backoff, result *aliyunClient.NetworkInterface, err error) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("WaitForNetworkInterfaceV2", mock.Anything, eniID, status, bo, mock.Anything).
		Return(result, err).Maybe()
	return m
}

// ExpectCreateENICalled sets an expectation that CreateNetworkInterfaceV2 will be called a specific number of times.
func (m *MockAPIHelper) ExpectCreateENICalled(times int) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("CreateNetworkInterfaceV2", mock.Anything, mock.Anything, mock.Anything).
		Return(&aliyunClient.NetworkInterface{}, nil).Times(times)
	return m
}

// ExpectDeleteENICalled sets an expectation that DeleteNetworkInterfaceV2 will be called a specific number of times.
func (m *MockAPIHelper) ExpectDeleteENICalled(times int) *MockAPIHelper {
	if m.openAPI == nil {
		m.initMocks()
	}
	m.openAPI.On("DeleteNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return(nil).Times(times)
	return m
}

// NewStandardVSwitch creates a standard vSwitch object for testing.
func NewStandardVSwitch(vswID, zoneID, cidr, ipv6CIDR string, availableIPs int64) *vpc.VSwitch {
	return &vpc.VSwitch{
		VSwitchId:               vswID,
		ZoneId:                  zoneID,
		CidrBlock:               cidr,
		Ipv6CidrBlock:           ipv6CIDR,
		AvailableIpAddressCount: availableIPs,
	}
}

// BuildMockENI is a helper to build a complete mock ENI object for API responses.
func BuildMockENI(eniID, eniType, status, vswID, zoneID string, ipv4s, ipv6s []string) *aliyunClient.NetworkInterface {
	eni := &aliyunClient.NetworkInterface{
		NetworkInterfaceID:          eniID,
		Type:                        eniType,
		Status:                      status,
		VSwitchID:                   vswID,
		ZoneID:                      zoneID,
		NetworkInterfaceTrafficMode: "Standard",
		PrivateIPSets:               make([]aliyunClient.IPSet, 0),
		IPv6Set:                     make([]aliyunClient.IPSet, 0),
	}

	// Add IPv4 addresses
	for i, ip := range ipv4s {
		ipSet := aliyunClient.IPSet{
			IPAddress: ip,
			Primary:   i == 0,
			IPName:    "ip-" + eniID + "-" + ip,
		}
		eni.PrivateIPSets = append(eni.PrivateIPSets, ipSet)
		if i == 0 {
			eni.PrivateIPAddress = ip
		}
	}

	// Add IPv6 addresses
	for i, ip := range ipv6s {
		ipSet := aliyunClient.IPSet{
			IPAddress: ip,
			Primary:   i == 0,
			IPName:    "ipv6-" + eniID + "-" + ip,
		}
		eni.IPv6Set = append(eni.IPv6Set, ipSet)
	}

	return eni
}

// AssertMockExpectations verifies that all mock expectations were met.
// This is a convenience method for calling AssertExpectations on all mocks.
func (m *MockAPIHelper) AssertMockExpectations(t mock.TestingT) {
	if m.openAPI != nil {
		m.openAPI.AssertExpectations(t)
	}
	if m.vpcClient != nil {
		m.vpcClient.AssertExpectations(t)
	}
	if m.ecsClient != nil {
		m.ecsClient.AssertExpectations(t)
	}
}

// SetupStandardENIFlow is a convenience method that sets up a complete standard ENI creation flow.
// This includes: create ENI -> attach -> wait for InUse status.
func (m *MockAPIHelper) SetupStandardENIFlow(eniID, instanceID, vswID, zoneID string, ipv4s []string) *MockAPIHelper {
	// Create ENI
	eni := BuildMockENI(eniID, aliyunClient.ENITypeSecondary, aliyunClient.ENIStatusInUse, vswID, zoneID, ipv4s, nil)
	m.SetupCreateENI(eniID, aliyunClient.ENITypeSecondary, WithVSwitchID(vswID), WithZoneID(zoneID))

	// Attach ENI
	m.SetupAttachENI(eniID, instanceID, nil)

	// Wait for ENI
	m.SetupWaitForENI(eniID, aliyunClient.ENIStatusInUse, eni, nil)

	return m
}

// SetupStandardVSwitch is a convenience method that sets up a standard vSwitch.
func (m *MockAPIHelper) SetupStandardVSwitch(vswID, zoneID string) *MockAPIHelper {
	vsw := NewStandardVSwitch(vswID, zoneID, "172.16.0.0/16", "fd00::/64", 100)
	m.SetupVSwitch(vswID, vsw)
	return m
}
