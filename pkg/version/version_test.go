package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdjustVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: unknown,
		},
		{
			name:     "version with alpha",
			input:    "v1.0.0-alpha.1",
			expected: "v1.0.0",
		},
		{
			name:     "version with beta",
			input:    "v1.0.0-beta.2",
			expected: "v1.0.0",
		},
		{
			name:     "version without suffix",
			input:    "v1.0.0",
			expected: "v1.0.0",
		},
		{
			name:     "version with commit hash",
			input:    "v0.0.0-master+d3b0738",
			expected: "v0.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adjustVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdjustCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: unknown,
		},
		{
			name:     "simple command",
			input:    "terway",
			expected: "terway",
		},
		{
			name:     "path command",
			input:    "/usr/local/bin/terway",
			expected: "terway",
		},
		{
			name:     "relative path",
			input:    "./terway",
			expected: "terway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adjustCommand(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdjustCommit(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: unknown,
		},
		{
			name:     "short commit",
			input:    "d3b0738",
			expected: "d3b0738",
		},
		{
			name:     "long commit",
			input:    "d3b0738451234567890",
			expected: "d3b0738",
		},
		{
			name:     "exact 7 chars",
			input:    "1234567",
			expected: "1234567",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adjustCommit(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
