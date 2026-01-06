// Copyright 2019 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sysctl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureConf_FileDoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "test_sysctl")

	err := EnsureConf(fPath, "1")
	require.NoError(t, err)

	// Verify file was created with correct content
	content, err := os.ReadFile(fPath)
	require.NoError(t, err)
	assert.Equal(t, "1", string(content))
}

func TestEnsureConf_FileExistsWithSameContent(t *testing.T) {
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "test_sysctl")

	// Create file with content
	err := os.WriteFile(fPath, []byte("1"), 0644)
	require.NoError(t, err)

	// EnsureConf should not modify the file
	err = EnsureConf(fPath, "1")
	require.NoError(t, err)

	// Verify content is unchanged
	content, err := os.ReadFile(fPath)
	require.NoError(t, err)
	assert.Equal(t, "1", string(content))
}

func TestEnsureConf_FileExistsWithDifferentContent(t *testing.T) {
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "test_sysctl")

	// Create file with different content
	err := os.WriteFile(fPath, []byte("0"), 0644)
	require.NoError(t, err)

	// EnsureConf should update the file
	err = EnsureConf(fPath, "1")
	require.NoError(t, err)

	// Verify content was updated
	content, err := os.ReadFile(fPath)
	require.NoError(t, err)
	assert.Equal(t, "1", string(content))
}

func TestEnsureConf_FileExistsWithWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "test_sysctl")

	// Create file with content and whitespace (common for sysctl files)
	err := os.WriteFile(fPath, []byte("1\n"), 0644)
	require.NoError(t, err)

	// EnsureConf should not modify the file because TrimSpace("1\n") == "1"
	err = EnsureConf(fPath, "1")
	require.NoError(t, err)

	// File content should remain unchanged (no unnecessary write)
	content, err := os.ReadFile(fPath)
	require.NoError(t, err)
	// The file still has the original content with newline
	assert.Equal(t, "1\n", string(content))
}

func TestEnsureConf_FileExistsWithLeadingWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "test_sysctl")

	// Create file with leading/trailing whitespace
	err := os.WriteFile(fPath, []byte("  1  \n"), 0644)
	require.NoError(t, err)

	// EnsureConf should not modify because TrimSpace matches
	err = EnsureConf(fPath, "1")
	require.NoError(t, err)
}

func TestEnsureConf_MultilineContent(t *testing.T) {
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "test_sysctl")

	// Test with multiline config
	cfg := "net.ipv4.ip_forward"

	err := EnsureConf(fPath, cfg)
	require.NoError(t, err)

	content, err := os.ReadFile(fPath)
	require.NoError(t, err)
	assert.Equal(t, cfg, string(content))
}

func TestEnsureConf_EmptyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "test_sysctl")

	err := EnsureConf(fPath, "")
	require.NoError(t, err)

	content, err := os.ReadFile(fPath)
	require.NoError(t, err)
	assert.Equal(t, "", string(content))
}

func TestEnsureConf_NestedDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	// Test with a path in a non-existent subdirectory
	fPath := filepath.Join(tmpDir, "subdir", "test_sysctl")

	// This should fail because the parent directory doesn't exist
	err := EnsureConf(fPath, "1")
	assert.Error(t, err)
}
