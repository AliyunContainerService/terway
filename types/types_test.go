package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIPSet_SetIP(t *testing.T) {
	ipSet := &IPSet{}
	assert.Equal(t, "127.0.0.1", ipSet.SetIP("127.0.0.1").IPv4.String())
	assert.NotNil(t, ipSet.IPv4)
	assert.Equal(t, "127.0.0.1", ipSet.SetIP("127.0.0.x").IPv4.String())
	assert.Nil(t, ipSet.IPv6)
	assert.Equal(t, "fd00::100", ipSet.SetIP("fd00::100").IPv6.String())
	assert.NotNil(t, ipSet.IPv6)
}
