package utils

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsWindowsOS(t *testing.T) {
	expected := runtime.GOOS == "windows"
	result := IsWindowsOS()
	assert.Equal(t, expected, result)
}

// Test different scenarios based on the current OS
func TestIsWindowsOS_LinuxEnvironment(t *testing.T) {
	// Since we're running in Linux environment (based on user info), this should be false
	if runtime.GOOS == "linux" {
		assert.False(t, IsWindowsOS())
	}
}
