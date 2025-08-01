package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_GetNetInterfaces(t *testing.T) {
	_, err := getNetInterfaces()
	assert.NoError(t, err)
}

func Test_GetInterfaceByMAC(t *testing.T) {
	_, err := getInterfaceByMAC("aa")
	assert.Error(t, err)
}
