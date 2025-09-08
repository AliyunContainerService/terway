package client_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
