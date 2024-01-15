package types_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/types"
)

func TestIPSet_SetIP(t *testing.T) {
	ipSet := &types.IPSet{}
	assert.Equal(t, "127.0.0.1", ipSet.SetIP("127.0.0.1").IPv4.String())
	assert.NotNil(t, ipSet.IPv4)
	assert.Equal(t, "127.0.0.1", ipSet.SetIP("127.0.0.x").IPv4.String())
	assert.Nil(t, ipSet.IPv6)
	assert.Equal(t, "fd00::100", ipSet.SetIP("fd00::100").IPv6.String())
	assert.NotNil(t, ipSet.IPv6)
}

func TestIPNetSet_SetIPNet(t *testing.T) {
	ipNetSet := &types.IPNetSet{}
	assert.Equal(t, "127.0.0.1/32", ipNetSet.SetIPNet("127.0.0.1/32").IPv4.String())
	assert.NotNil(t, ipNetSet.IPv4)
	assert.Equal(t, "127.0.0.0/24", ipNetSet.SetIPNet("127.0.0.1/24").IPv4.String())
	assert.NotNil(t, ipNetSet.IPv4)
	assert.Equal(t, "127.0.0.0/24", ipNetSet.SetIPNet("127.0.0.x").IPv4.String(), "no change")
	assert.Nil(t, ipNetSet.IPv6)
	assert.Equal(t, "fd00::/120", ipNetSet.SetIPNet("fd00::/120").IPv6.String())
	assert.NotNil(t, ipNetSet.IPv6)
}

func TestErrorReturnsCorrectErrorMessage(t *testing.T) {
	err := &types.Error{
		Code: types.ErrInternalError,
		Msg:  "An internal error occurred",
	}

	assert.Equal(t, "code: InternalError, msg: An internal error occurred", err.Error())
}

func TestErrorUnwrapReturnsUnderlyingError(t *testing.T) {
	underlyingError := fmt.Errorf("underlying error")
	err := &types.Error{
		Code: types.ErrInternalError,
		Msg:  "An internal error occurred",
		R:    underlyingError,
	}

	assert.Equal(t, underlyingError, err.Unwrap())
}

func TestErrorUnwrapReturnsNilWhenNoUnderlyingError(t *testing.T) {
	err := &types.Error{
		Code: types.ErrInternalError,
		Msg:  "An internal error occurred",
	}

	assert.Nil(t, err.Unwrap())
}
