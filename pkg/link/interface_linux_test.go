package link

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetDeviceNumber(t *testing.T) {
	id, err := GetDeviceNumber("00")
	assert.True(t, errors.Is(err, ErrNotFound))
	assert.Equal(t, int32(0), id)
}
