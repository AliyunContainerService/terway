package vf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetPFBDF tests the getPFBDF function with mocked sysfs structure
func TestGetPFBDF(t *testing.T) {
	t.Run("invalid BDF format", func(t *testing.T) {
		// Test with invalid BDF that doesn't exist in sysfs
		result := getPFBDF("invalid-bdf")
		assert.Empty(t, result, "Should return empty string for invalid BDF")
	})

	t.Run("non-existent device", func(t *testing.T) {
		// Test with a BDF that doesn't exist in sysfs
		result := getPFBDF("0000:ff:ff.f")
		assert.Empty(t, result, "Should return empty string for non-existent device")
	})
}

// TestGetPFBDFWithBasePath tests the getPFBDFWithBasePath function with temporary sysfs structure
func TestGetPFBDFWithBasePath(t *testing.T) {
	// Create a temporary directory structure to simulate sysfs
	tempDir := t.TempDir()
	vfBDF := "0000:01:10.1"
	pfBDF := "0000:01:10.0"

	// Create VF device directory
	vfDeviceDir := filepath.Join(tempDir, vfBDF)
	err := os.MkdirAll(vfDeviceDir, 0755)
	require.NoError(t, err)

	// Create PF device directory
	pfDeviceDir := filepath.Join(tempDir, pfBDF)
	err = os.MkdirAll(pfDeviceDir, 0755)
	require.NoError(t, err)

	// Create physfn symlink pointing to PF
	physfnPath := filepath.Join(vfDeviceDir, "physfn")
	err = os.Symlink("../"+pfBDF, physfnPath)
	require.NoError(t, err)

	t.Run("valid VF with physfn symlink", func(t *testing.T) {
		// Test the actual function with custom base path
		result := getPFBDFWithBasePath(vfBDF, tempDir)
		assert.Equal(t, pfBDF, result)
	})

	t.Run("device without physfn symlink", func(t *testing.T) {
		nonVFBDF := "0000:02:00.0"
		nonVFDeviceDir := filepath.Join(tempDir, nonVFBDF)
		err := os.MkdirAll(nonVFDeviceDir, 0755)
		require.NoError(t, err)

		result := getPFBDFWithBasePath(nonVFBDF, tempDir)
		assert.Empty(t, result)
	})
}

// TestCheckSRIOVAutoprobe tests the checkSRIOVAutoprobe function
func TestCheckSRIOVAutoprobe(t *testing.T) {
	t.Run("non-existent PF device", func(t *testing.T) {
		// Test with a PF BDF that doesn't exist in sysfs
		enabled, err := checkSRIOVAutoprobe("0000:ff:ff.0")
		assert.Error(t, err, "Should return error for non-existent PF device")
		assert.False(t, enabled, "Should return false when error occurs")
	})

	t.Run("invalid BDF format", func(t *testing.T) {
		// Test with invalid BDF format
		enabled, err := checkSRIOVAutoprobe("invalid-bdf")
		assert.Error(t, err, "Should return error for invalid BDF")
		assert.False(t, enabled, "Should return false when error occurs")
	})
}

// TestCheckSRIOVAutoprobeWithBasePath tests the checkSRIOVAutoprobeWithBasePath function with temporary structure
func TestCheckSRIOVAutoprobeWithBasePath(t *testing.T) {
	// Create a temporary directory structure to simulate sysfs
	tempDir := t.TempDir()
	pfBDF := "0000:01:10.0"

	// Create PF device directory
	pfDeviceDir := filepath.Join(tempDir, pfBDF)
	err := os.MkdirAll(pfDeviceDir, 0755)
	require.NoError(t, err)

	t.Run("autoprobe enabled", func(t *testing.T) {
		// Create sriov_drivers_autoprobe file with value "1"
		autoprobePath := filepath.Join(pfDeviceDir, "sriov_drivers_autoprobe")
		err := os.WriteFile(autoprobePath, []byte("1\n"), 0644)
		require.NoError(t, err)

		// Test the actual function with custom base path
		enabled, err := checkSRIOVAutoprobeWithBasePath(pfBDF, tempDir)
		assert.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("autoprobe disabled", func(t *testing.T) {
		// Create sriov_drivers_autoprobe file with value "0"
		autoprobePath := filepath.Join(pfDeviceDir, "sriov_drivers_autoprobe")
		err := os.WriteFile(autoprobePath, []byte("0"), 0644)
		require.NoError(t, err)

		enabled, err := checkSRIOVAutoprobeWithBasePath(pfBDF, tempDir)
		assert.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("file does not exist", func(t *testing.T) {
		nonExistentPF := "0000:02:00.0"

		enabled, err := checkSRIOVAutoprobeWithBasePath(nonExistentPF, tempDir)
		assert.Error(t, err)
		assert.False(t, enabled)
	})
}

// TestSetDriverOverride tests the setDriverOverride function
func TestSetDriverOverride(t *testing.T) {
	t.Run("non-existent VF device", func(t *testing.T) {
		// Test with a VF BDF that doesn't exist in sysfs
		err := setDriverOverride("0000:ff:ff.1", "virtio-pci")
		assert.Error(t, err, "Should return error for non-existent VF device")
	})

	t.Run("invalid BDF format", func(t *testing.T) {
		// Test with invalid BDF format
		err := setDriverOverride("invalid-bdf", "virtio-pci")
		assert.Error(t, err, "Should return error for invalid BDF")
	})
}

// TestSetDriverOverrideWithBasePath tests the setDriverOverrideWithBasePath function with temporary structure
func TestSetDriverOverrideWithBasePath(t *testing.T) {
	// Create a temporary directory structure to simulate sysfs
	tempDir := t.TempDir()
	vfBDF := "0000:01:10.1"
	driverName := "virtio-pci"

	// Create VF device directory
	vfDeviceDir := filepath.Join(tempDir, vfBDF)
	err := os.MkdirAll(vfDeviceDir, 0755)
	require.NoError(t, err)

	t.Run("successful driver override", func(t *testing.T) {
		// Test the actual function with custom base path
		err := setDriverOverrideWithBasePath(vfBDF, driverName, tempDir)
		assert.NoError(t, err)

		// Verify the file was created with correct content
		overridePath := filepath.Join(vfDeviceDir, "driver_override")
		content, err := os.ReadFile(overridePath)
		assert.NoError(t, err)
		assert.Equal(t, driverName, string(content))
	})

	t.Run("directory does not exist", func(t *testing.T) {
		nonExistentVF := "0000:03:00.1"

		err := setDriverOverrideWithBasePath(nonExistentVF, driverName, tempDir)
		assert.Error(t, err)
	})

	t.Run("empty driver name", func(t *testing.T) {
		err := setDriverOverrideWithBasePath(vfBDF, "", tempDir)
		assert.NoError(t, err)

		// Verify the file was created with empty content
		overridePath := filepath.Join(vfDeviceDir, "driver_override")
		content, err := os.ReadFile(overridePath)
		assert.NoError(t, err)
		assert.Empty(t, string(content))
	})
}
