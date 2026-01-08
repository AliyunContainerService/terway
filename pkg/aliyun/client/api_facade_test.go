package client_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
)

func TestNewAPIFacade(t *testing.T) {
	// Create a mock client set
	clientSet := &credential.ClientMgr{}
	limitConfig := client.LimitConfig{
		"ECS":  client.Limit{QPS: 10, Burst: 20},
		"VPC":  client.Limit{QPS: 10, Burst: 20},
		"EFLO": client.Limit{QPS: 10, Burst: 20},
	}

	facade := client.NewAPIFacade(clientSet, limitConfig)

	assert.NotNil(t, facade)
	assert.NotNil(t, facade.GetECS())
	assert.NotNil(t, facade.GetVPC())
	assert.NotNil(t, facade.GetEFLO())
	assert.NotNil(t, facade.GetEFLOController())
}

func TestAPIFacade_GetServices(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	limitConfig := client.LimitConfig{}
	facade := client.NewAPIFacade(clientSet, limitConfig)

	assert.NotNil(t, facade.GetECS())
	assert.NotNil(t, facade.GetVPC())
	assert.NotNil(t, facade.GetEFLO())
	assert.NotNil(t, facade.GetEFLOController())
}

func TestAPIFacade_CreateNetworkInterface(t *testing.T) {
	// Since we can't access unexported fields from a different package,
	// we'll test the facade through its public interface
	clientSet := &credential.ClientMgr{}
	limitConfig := client.LimitConfig{}
	facade := client.NewAPIFacade(clientSet, limitConfig)

	// Test that the facade can be created and has the expected services
	assert.NotNil(t, facade.GetECS())
	assert.NotNil(t, facade.GetVPC())
	assert.NotNil(t, facade.GetEFLO())
	assert.NotNil(t, facade.GetEFLOController())

	// Test that the facade implements the ENI interface
	var _ client.ENI = facade
}

// ==============================================================================
// V2 Methods Coverage Tests (Lines 50-156)
// ==============================================================================

func TestAPIFacade_CreateNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	limitConfig := client.LimitConfig{}
	facade := client.NewAPIFacade(clientSet, limitConfig)

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.CreateNetworkInterface",
			backendAPI: client.BackendAPIECS,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockENI := &client.NetworkInterface{
					NetworkInterfaceID: "eni-ecs-001",
					Status:             "Available",
				}
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "CreateNetworkInterface",
					func(_ client.ECS, ctx context.Context, opts ...client.CreateNetworkInterfaceOption) (*client.NetworkInterface, error) {
						return mockENI, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend calls efloService.CreateElasticNetworkInterfaceV2",
			backendAPI: client.BackendAPIEFLO,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockENI := &client.NetworkInterface{
					NetworkInterfaceID: "leni-eflo-001",
					Status:             "Available",
				}
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "CreateElasticNetworkInterfaceV2",
					func(_ client.EFLO, ctx context.Context, opts ...client.CreateNetworkInterfaceOption) (*client.NetworkInterface, error) {
						return mockENI, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO HDENI backend calls efloService.CreateHDENI",
			backendAPI: client.BackendAPIEFLOHDENI,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockENI := &client.NetworkInterface{
					NetworkInterfaceID: "hdeni-001",
					Status:             "Available",
				}
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "CreateHDENI",
					func(_ client.EFLO, ctx context.Context, opts ...client.CreateNetworkInterfaceOption) (*client.NetworkInterface, error) {
						return mockENI, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			result, err := facade.CreateNetworkInterfaceV2(ctx)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestAPIFacade_DescribeNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.DescribeNetworkInterface2",
			backendAPI: client.BackendAPIECS,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockENIs := []*client.NetworkInterface{
					{NetworkInterfaceID: "eni-ecs-001", Status: "InUse"},
				}
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "DescribeNetworkInterface2",
					func(_ client.ECS, ctx context.Context, opts ...client.DescribeNetworkInterfaceOption) ([]*client.NetworkInterface, error) {
						return mockENIs, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend calls efloService.DescribeLeniNetworkInterface",
			backendAPI: client.BackendAPIEFLO,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockENIs := []*client.NetworkInterface{
					{NetworkInterfaceID: "leni-eflo-001", Status: "Available"},
				}
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "DescribeLeniNetworkInterface",
					func(_ client.EFLO, ctx context.Context, opts ...client.DescribeNetworkInterfaceOption) ([]*client.NetworkInterface, error) {
						return mockENIs, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO HDENI backend calls efloService.DescribeHDENI",
			backendAPI: client.BackendAPIEFLOHDENI,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockENIs := []*client.NetworkInterface{
					{NetworkInterfaceID: "hdeni-001", Status: "InUse"},
				}
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "DescribeHDENI",
					func(_ client.EFLO, ctx context.Context, opts ...client.DescribeNetworkInterfaceOption) ([]*client.NetworkInterface, error) {
						return mockENIs, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			result, err := facade.DescribeNetworkInterfaceV2(ctx)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.NotEmpty(t, result)
			}
		})
	}
}

func TestAPIFacade_AttachNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.AttachNetworkInterface",
			backendAPI: client.BackendAPIECS,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "AttachNetworkInterface",
					func(_ client.ECS, ctx context.Context, opts ...client.AttachNetworkInterfaceOption) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend calls efloService.AttachLeni",
			backendAPI: client.BackendAPIEFLO,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "AttachLeni",
					func(_ client.EFLO, ctx context.Context, opts ...client.AttachNetworkInterfaceOption) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO HDENI backend calls efloService.AttachHDENI",
			backendAPI: client.BackendAPIEFLOHDENI,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "AttachHDENI",
					func(_ client.EFLO, ctx context.Context, opts ...client.AttachNetworkInterfaceOption) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			err := facade.AttachNetworkInterfaceV2(ctx)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAPIFacade_DetachNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.DetachNetworkInterface2",
			backendAPI: client.BackendAPIECS,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "DetachNetworkInterface2",
					func(_ client.ECS, ctx context.Context, opts ...client.DetachNetworkInterfaceOption) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend calls efloService.DetachLeni",
			backendAPI: client.BackendAPIEFLO,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "DetachLeni",
					func(_ client.EFLO, ctx context.Context, opts ...client.DetachNetworkInterfaceOption) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO HDENI backend calls efloService.DetachHDENI",
			backendAPI: client.BackendAPIEFLOHDENI,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "DetachHDENI",
					func(_ client.EFLO, ctx context.Context, opts ...client.DetachNetworkInterfaceOption) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			err := facade.DetachNetworkInterfaceV2(ctx)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAPIFacade_DeleteNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		eniID         string
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.DeleteNetworkInterface",
			backendAPI: client.BackendAPIECS,
			eniID:      "eni-ecs-001",
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "DeleteNetworkInterface",
					func(_ client.ECS, ctx context.Context, eniID string) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend calls efloService.DeleteElasticNetworkInterface",
			backendAPI: client.BackendAPIEFLO,
			eniID:      "leni-eflo-001",
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "DeleteElasticNetworkInterface",
					func(_ client.EFLO, ctx context.Context, eniID string) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO HDENI backend calls efloService.DeleteHDENI",
			backendAPI: client.BackendAPIEFLOHDENI,
			eniID:      "hdeni-001",
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "DeleteHDENI",
					func(_ client.EFLO, ctx context.Context, eniID string) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			eniID:      "eni-test",
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			err := facade.DeleteNetworkInterfaceV2(ctx, tt.eniID)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAPIFacade_AssignPrivateIPAddressV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.AssignPrivateIPAddress2",
			backendAPI: client.BackendAPIECS,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockIPs := []client.IPSet{
					{IPAddress: "10.0.0.1", Primary: true},
					{IPAddress: "10.0.0.2", Primary: false},
				}
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "AssignPrivateIPAddress2",
					func(_ client.ECS, ctx context.Context, opts ...client.AssignPrivateIPAddressOption) ([]client.IPSet, error) {
						return mockIPs, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend calls efloService.AssignLeniPrivateIPAddress2",
			backendAPI: client.BackendAPIEFLO,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockIPs := []client.IPSet{
					{IPAddress: "10.0.0.100", Primary: false, IPName: "ip-test-001"},
				}
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "AssignLeniPrivateIPAddress2",
					func(_ client.EFLO, ctx context.Context, opts ...client.AssignPrivateIPAddressOption) ([]client.IPSet, error) {
						return mockIPs, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO HDENI backend returns not implemented",
			backendAPI: client.BackendAPIEFLOHDENI,
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			result, err := facade.AssignPrivateIPAddressV2(ctx)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestAPIFacade_UnAssignPrivateIPAddressesV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		eniID         string
		ips           []client.IPSet
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.UnAssignPrivateIPAddresses2",
			backendAPI: client.BackendAPIECS,
			eniID:      "eni-ecs-001",
			ips:        []client.IPSet{{IPAddress: "10.0.0.1"}},
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "UnAssignPrivateIPAddresses2",
					func(_ client.ECS, ctx context.Context, eniID string, ips []client.IPSet) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend calls efloService.UnAssignLeniPrivateIPAddresses2",
			backendAPI: client.BackendAPIEFLO,
			eniID:      "leni-eflo-001",
			ips:        []client.IPSet{{IPAddress: "10.0.0.100", IPName: "ip-test-001"}},
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "UnAssignLeniPrivateIPAddresses2",
					func(_ client.EFLO, ctx context.Context, eniID string, ips []client.IPSet) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO HDENI backend returns not implemented",
			backendAPI: client.BackendAPIEFLOHDENI,
			eniID:      "hdeni-001",
			ips:        []client.IPSet{},
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			eniID:      "eni-test",
			ips:        []client.IPSet{},
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			err := facade.UnAssignPrivateIPAddressesV2(ctx, tt.eniID, tt.ips)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAPIFacade_AssignIpv6AddressesV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.AssignIpv6Addresses2",
			backendAPI: client.BackendAPIECS,
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockIPs := []client.IPSet{
					{IPAddress: "2001:db8::1", Primary: false},
				}
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "AssignIpv6Addresses2",
					func(_ client.ECS, ctx context.Context, opts ...client.AssignIPv6AddressesOption) ([]client.IPSet, error) {
						return mockIPs, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend returns not implemented",
			backendAPI: client.BackendAPIEFLO,
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
		{
			name:       "EFLO HDENI backend returns not implemented",
			backendAPI: client.BackendAPIEFLOHDENI,
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			result, err := facade.AssignIpv6AddressesV2(ctx)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestAPIFacade_UnAssignIpv6AddressesV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		eniID         string
		ips           []client.IPSet
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.UnAssignIpv6Addresses2",
			backendAPI: client.BackendAPIECS,
			eniID:      "eni-ecs-001",
			ips:        []client.IPSet{{IPAddress: "2001:db8::1"}},
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "UnAssignIpv6Addresses2",
					func(_ client.ECS, ctx context.Context, eniID string, ips []client.IPSet) error {
						return nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend returns not implemented",
			backendAPI: client.BackendAPIEFLO,
			eniID:      "leni-eflo-001",
			ips:        []client.IPSet{},
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
		{
			name:       "EFLO HDENI backend returns not implemented",
			backendAPI: client.BackendAPIEFLOHDENI,
			eniID:      "hdeni-001",
			ips:        []client.IPSet{},
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			eniID:      "eni-test",
			ips:        []client.IPSet{},
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			err := facade.UnAssignIpv6AddressesV2(ctx, tt.eniID, tt.ips)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAPIFacade_WaitForNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	backoff := wait.Backoff{
		Steps:    1,
		Duration: 1,
	}

	tests := []struct {
		name          string
		backendAPI    client.BackendAPI
		eniID         string
		status        string
		setupMocks    func(*client.APIFacade) *gomonkey.Patches
		expectedError bool
		errorIs       error
	}{
		{
			name:       "ECS backend calls ecsService.WaitForNetworkInterface",
			backendAPI: client.BackendAPIECS,
			eniID:      "eni-ecs-001",
			status:     "InUse",
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockENI := &client.NetworkInterface{
					NetworkInterfaceID: "eni-ecs-001",
					Status:             "InUse",
				}
				ecsService := facade.GetECS()
				patches.ApplyMethod(reflect.TypeOf(ecsService), "WaitForNetworkInterface",
					func(_ client.ECS, ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*client.NetworkInterface, error) {
						return mockENI, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO backend calls efloService.WaitForLeniNetworkInterface",
			backendAPI: client.BackendAPIEFLO,
			eniID:      "leni-eflo-001",
			status:     "Available",
			setupMocks: func(facade *client.APIFacade) *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockENI := &client.NetworkInterface{
					NetworkInterfaceID: "leni-eflo-001",
					Status:             "Available",
				}
				efloService := facade.GetEFLO()
				patches.ApplyMethod(reflect.TypeOf(efloService), "WaitForLeniNetworkInterface",
					func(_ client.EFLO, ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*client.NetworkInterface, error) {
						return mockENI, nil
					})
				return patches
			},
			expectedError: false,
		},
		{
			name:       "EFLO HDENI backend returns not implemented",
			backendAPI: client.BackendAPIEFLOHDENI,
			eniID:      "hdeni-001",
			status:     "InUse",
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
		{
			name:       "Unknown backend returns not implemented",
			backendAPI: client.BackendAPI(999),
			eniID:      "eni-test",
			status:     "Available",
			setupMocks: func(*client.APIFacade) *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
			errorIs:       client.ErrNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.setupMocks(facade)
			defer patches.Reset()

			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			result, err := facade.WaitForNetworkInterfaceV2(ctx, tt.eniID, tt.status, backoff, false)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.eniID, result.NetworkInterfaceID)
			}
		})
	}
}
