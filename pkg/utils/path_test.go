package utils

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
		isWindows bool
	}{
		{
			name:     "Unix absolute path on Linux",
			path:     "/usr/local/bin",
			expected: "/usr/local/bin",
			isWindows: false,
		},
		{
			name:     "Unix relative path on Linux",
			path:     "relative/path",
			expected: "relative/path",
			isWindows: false,
		},
		{
			name:     "Windows style path on Linux",
			path:     "C:\\Windows\\System32",
			expected: "C:\\Windows\\System32",
			isWindows: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip Windows-specific tests on non-Windows platforms
			if tt.isWindows && runtime.GOOS != "windows" {
				t.Skip("Skipping Windows-specific test on non-Windows platform")
			}

			result := NormalizePath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizePath_LinuxBehavior(t *testing.T) {
	// Test specific Linux behavior
	if runtime.GOOS == "linux" {
		// On Linux, paths should remain unchanged
		testPaths := []string{
			"/usr/local/bin",
			"relative/path",
			"./current/path",
			"../parent/path",
			"/",
		}

		for _, path := range testPaths {
			result := NormalizePath(path)
			assert.Equal(t, path, result, "Path should remain unchanged on Linux: %s", path)
		}
	}
}

func TestMustGetWindowsSystemDrive_NonWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		// On non-Windows systems, the function should return empty string
		// This tests the early return in mustGetWindowsSystemDrive
		result := mustGetWindowsSystemDrive()
		assert.Equal(t, "", result)
	}
}