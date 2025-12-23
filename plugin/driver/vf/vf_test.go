package vf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Run("valid config eni controller", func(t *testing.T) {
		config := []byte(`[{"bdf": "0000:1:1.6","mac": "cc:48:15:ff:00:09","vf_id": 9,"pf_id": 2}]`)
		configs, err := parse(defaultNUSAConfigPath, config)
		require.NoError(t, err)
		assert.Len(t, configs.EniVFs, 1)
		assert.Equal(t, "0000:1:1.6", configs.EniVFs[0].BDF)
		assert.Equal(t, 9, configs.EniVFs[0].VfID)
		assert.Equal(t, 2, configs.EniVFs[0].PfID)
	})
	t.Run("invalid config eni controller", func(t *testing.T) {
		config := []byte(`{"eniVFs": [{ "pf_id": 0,"vf_id": 0,"bdf": "0000:1:2.5"}]}`)
		_, err := parse(defaultNUSAConfigPath, config)
		require.NotNil(t, err)
	})
	t.Run("valid config vpc eni agent", func(t *testing.T) {
		config := []byte(`{"eniVFs": [{"pf_id": 1,"vf_id": 9,"bdf": "0000:1:1.6"}]}`)
		configs, err := parse("/var/run/hc-eni-host/vf-topo-vpc", config)
		require.NoError(t, err)
		assert.Len(t, configs.EniVFs, 1)
		assert.Equal(t, "0000:1:1.6", configs.EniVFs[0].BDF)
		assert.Equal(t, 9, configs.EniVFs[0].VfID)
		assert.Equal(t, 1, configs.EniVFs[0].PfID)
	})
	t.Run("invalid config vpc eni agent", func(t *testing.T) {
		config := []byte(`[{"bdf": "0000:1:1.6","mac": "cc:48:15:ff:00:09","vf_id": 9,"pf_id": 2}]`)
		_, err := parse("/var/run/hc-eni-host/vf-topo-vpc", config)
		require.NotNil(t, err)
	})
}

// TestGetBDFbyVFID tests the GetBDFbyVFID function
func TestGetBDFbyVFID(t *testing.T) {
	t.Run("valid config with matching vfID", func(t *testing.T) {
		// Create a temporary directory and config file
		tempDir := t.TempDir()
		configPath := filepath.Join(tempDir, "vf-config.json")

		// Write test config data
		configData := `{"eniVFs": [{"pf_id": 1,"vf_id": 9,"bdf": "0000:1:1.6"}]}`
		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Test the function
		bdf, err := GetBDFbyVFID(configPath, 9)
		assert.NoError(t, err)
		assert.Equal(t, "0000:1:1.6", bdf)
	})

	t.Run("valid config with non-matching vfID", func(t *testing.T) {
		// Create a temporary directory and config file
		tempDir := t.TempDir()
		configPath := filepath.Join(tempDir, "vf-config.json")

		// Write test config data
		configData := `{"eniVFs": [{"pf_id": 1,"vf_id": 9,"bdf": "0000:1:1.6"}]}`
		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Test the function with non-matching vfID
		_, err = GetBDFbyVFID(configPath, 10)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found specified vfID 10")
	})

	t.Run("invalid config file", func(t *testing.T) {
		// Create a temporary directory and config file
		tempDir := t.TempDir()
		configPath := filepath.Join(tempDir, "vf-config.json")

		// Write invalid config data
		configData := `{"eniVFs": [{"pf_id": 1,"vf_id": 9,"bdf": "0000:1:1.6"}`
		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Test the function
		_, err = GetBDFbyVFID(configPath, 9)
		assert.Error(t, err)
	})

	t.Run("non-existent config file", func(t *testing.T) {
		// Test the function with non-existent config file
		_, err := GetBDFbyVFID("/non/existent/path", 9)
		assert.Error(t, err)
	})

	t.Run("empty path with defaultNUSAConfigPath existing", func(t *testing.T) {
		// Create a temporary directory and config file
		tempDir := t.TempDir()

		// Save original value
		originalPath := defaultNUSAConfigPath
		// Temporarily change the default path to our temp directory
		defaultNUSAConfigPath = filepath.Join(tempDir, "eni_topo")

		// Make sure the default path doesn't exist
		_, err := os.Stat(defaultNUSAConfigPath)
		if !os.IsNotExist(err) {
			os.Remove(defaultNUSAConfigPath)
		}

		// Write test config data to the default path
		configData := `[{"pf_id": 1,"vf_id": 9,"bdf": "0000:1:1.6"}]`
		err = os.WriteFile(defaultNUSAConfigPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Test the function with empty path
		bdf, err := GetBDFbyVFID("", 9)
		assert.NoError(t, err)
		assert.Equal(t, "0000:1:1.6", bdf)

		// Restore original value
		defaultNUSAConfigPath = originalPath
	})

	t.Run("empty path with HcENIHostConfigPath existing", func(t *testing.T) {
		// Create a temporary directory and config file
		tempDir := t.TempDir()

		// Save original values
		originalNUSAPath := defaultNUSAConfigPath
		originalHCPath := HcENIHostConfigPath

		// Temporarily change the paths to our temp directory
		defaultNUSAConfigPath = filepath.Join(tempDir, "eni_topo")
		HcENIHostConfigPath = filepath.Join(tempDir, "vf-topo-vpc")

		// Make sure the defaultNUSAConfigPath doesn't exist
		_, err := os.Stat(defaultNUSAConfigPath)
		if !os.IsNotExist(err) {
			os.Remove(defaultNUSAConfigPath)
		}

		// Write test config data to the HcENIHostConfigPath
		configData := `{"eniVFs": [{"pf_id": 1,"vf_id": 9,"bdf": "0000:1:1.6"}]}`
		err = os.WriteFile(HcENIHostConfigPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Test the function with empty path
		bdf, err := GetBDFbyVFID("", 9)
		assert.NoError(t, err)
		assert.Equal(t, "0000:1:1.6", bdf)

		// Restore original values
		defaultNUSAConfigPath = originalNUSAPath
		HcENIHostConfigPath = originalHCPath
	})

	t.Run("empty path with neither config file existing", func(t *testing.T) {
		// Save original values
		originalNUSAPath := defaultNUSAConfigPath
		originalHCPath := HcENIHostConfigPath

		// Temporarily change the paths to non-existent files
		tempDir := t.TempDir()
		defaultNUSAConfigPath = filepath.Join(tempDir, "non-existent-eni_topo")
		HcENIHostConfigPath = filepath.Join(tempDir, "non-existent-vf-topo-vpc")

		// Test the function with empty path
		_, err := GetBDFbyVFID("", 9)
		assert.Error(t, err)

		// Restore original values
		defaultNUSAConfigPath = originalNUSAPath
		HcENIHostConfigPath = originalHCPath
	})

	t.Run("multiple VFs with matching vfID", func(t *testing.T) {
		// Create a temporary directory and config file
		tempDir := t.TempDir()
		configPath := filepath.Join(tempDir, "vf-config.json")

		// Write test config data with multiple VFs
		configData := `{"eniVFs": [{"pf_id": 1,"vf_id": 8,"bdf": "0000:1:1.5"}, {"pf_id": 1,"vf_id": 9,"bdf": "0000:1:1.6"}, {"pf_id": 1,"vf_id": 10,"bdf": "0000:1:1.7"}]}`
		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Test the function
		bdf, err := GetBDFbyVFID(configPath, 9)
		assert.NoError(t, err)
		assert.Equal(t, "0000:1:1.6", bdf)
	})

	t.Run("eni controller config format", func(t *testing.T) {
		// Create a temporary directory and config file
		tempDir := t.TempDir()
		configPath := filepath.Join(tempDir, "eni-controller-config.json")

		// Write test config data in eni controller format
		configData := `[{"pf_id": 1,"vf_id": 9,"bdf": "0000:1:1.6"}]`
		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Temporarily change the default path to test eni controller format
		originalPath := defaultNUSAConfigPath
		defaultNUSAConfigPath = configPath

		// Test the function
		bdf, err := GetBDFbyVFID(configPath, 9)
		assert.NoError(t, err)
		assert.Equal(t, "0000:1:1.6", bdf)

		// Restore original value
		defaultNUSAConfigPath = originalPath
	})
}

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
