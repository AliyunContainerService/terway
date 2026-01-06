package client_test

import (
	"context"
	"testing"

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

	// Test unknown backend to achieve coverage without triggering nil pointer
	t.Run("Unknown backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPI(999))
		_, err := facade.CreateNetworkInterfaceV2(ctx)
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}

func TestAPIFacade_DescribeNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	tests := []struct {
		name       string
		backendAPI client.BackendAPI
		wantErr    bool
	}{
		{"Unknown backend returns not implemented", client.BackendAPI(999), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := client.SetBackendAPI(context.Background(), tt.backendAPI)
			_, err := facade.DescribeNetworkInterfaceV2(ctx)
			if tt.wantErr {
				assert.Error(t, err)
			}
		})
	}
}

func TestAPIFacade_AttachNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	t.Run("Unknown backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPI(999))
		err := facade.AttachNetworkInterfaceV2(ctx)
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}

func TestAPIFacade_DetachNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	t.Run("Unknown backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPI(999))
		err := facade.DetachNetworkInterfaceV2(ctx)
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}

func TestAPIFacade_DeleteNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	t.Run("Unknown backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPI(999))
		err := facade.DeleteNetworkInterfaceV2(ctx, "eni-test")
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}

func TestAPIFacade_AssignPrivateIPAddressV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	t.Run("EFLO HDENI backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPIEFLOHDENI)
		_, err := facade.AssignPrivateIPAddressV2(ctx)
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}

func TestAPIFacade_UnAssignPrivateIPAddressesV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	t.Run("EFLO HDENI backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPIEFLOHDENI)
		err := facade.UnAssignPrivateIPAddressesV2(ctx, "eni-test", []client.IPSet{})
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}

func TestAPIFacade_AssignIpv6AddressesV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	t.Run("EFLO backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPIEFLO)
		_, err := facade.AssignIpv6AddressesV2(ctx)
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}

func TestAPIFacade_UnAssignIpv6AddressesV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	t.Run("EFLO backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPIEFLO)
		err := facade.UnAssignIpv6AddressesV2(ctx, "eni-test", []client.IPSet{})
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}

func TestAPIFacade_WaitForNetworkInterfaceV2(t *testing.T) {
	clientSet := &credential.ClientMgr{}
	facade := client.NewAPIFacade(clientSet, client.LimitConfig{})

	backoff := wait.Backoff{
		Steps:    1,
		Duration: 1,
	}

	t.Run("EFLO HDENI backend returns not implemented", func(t *testing.T) {
		ctx := client.SetBackendAPI(context.Background(), client.BackendAPIEFLOHDENI)
		_, err := facade.WaitForNetworkInterfaceV2(ctx, "eni-test", "Available", backoff, false)
		assert.Error(t, err)
		assert.Equal(t, client.ErrNotImplemented, err)
	})
}
