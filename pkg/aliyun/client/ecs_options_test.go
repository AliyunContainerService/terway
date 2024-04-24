package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// MockIdempotentKeyGen is a mock implementation of IdempotentKeyGen interface
type MockIdempotentKeyGen struct {
	generatedKeys map[string]string
}

func (m *MockIdempotentKeyGen) GenerateKey(argsHash string) string {
	if _, ok := m.generatedKeys[argsHash]; !ok {
		m.generatedKeys[argsHash] = "mockToken"
	}
	return m.generatedKeys[argsHash]
}

func (m *MockIdempotentKeyGen) PutBack(argsHash string, clientToken string) {
	delete(m.generatedKeys, argsHash)
}

// TestCreateNetworkInterfaceOptions_Finish tests the Finish function of CreateNetworkInterfaceOptions
func TestCreateNetworkInterfaceOptions_Finish(t *testing.T) {
	// Prepare the test data
	niOptions := &NetworkInterfaceOptions{
		VSwitchID:        "vsw-xxxxxx",
		SecurityGroupIDs: []string{"sg-xxxxxx"},
		ResourceGroupID:  "rg-xxxxxx",
		Tags:             map[string]string{"key1": "value1", "key2": "value2"},
		Trunk:            true,
		ERDMA:            true,
		IPCount:          2,
		IPv6Count:        1,
	}

	c := &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: niOptions,
	}

	// Execute the function to be tested
	req, cleanup, err := c.Finish(&MockIdempotentKeyGen{generatedKeys: map[string]string{}})

	// Verify the result
	assert.NoError(t, err)
	assert.NotNil(t, req)
	assert.NotNil(t, cleanup)

	assert.Equal(t, niOptions.VSwitchID, req.VSwitchId)
	assert.Equal(t, ENITypeTrunk, req.InstanceType)
	assert.Equal(t, ENITrafficModeRDMA, req.NetworkInterfaceTrafficMode)
	assert.Equal(t, 1, len(*req.SecurityGroupIds))
	assert.Equal(t, niOptions.ResourceGroupID, req.ResourceGroupId)
	assert.Equal(t, eniDescription, req.Description)
	assert.Equal(t, "mockToken", req.ClientToken)
	assert.NotNil(t, c.Backoff)

	// Cleanup
	cleanup()
}
