package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainsAll(t *testing.T) {
	tests := []struct {
		name     string
		subset   map[string]int
		superset map[string]int
		expected bool
	}{
		{
			name:     "Empty subset",
			subset:   map[string]int{},
			superset: map[string]int{"a": 1, "b": 2},
			expected: true,
		},
		{
			name:     "Subset is equal to superset",
			subset:   map[string]int{"a": 1, "b": 2},
			superset: map[string]int{"a": 1, "b": 2},
			expected: true,
		},
		{
			name:     "Subset is contained in superset",
			subset:   map[string]int{"a": 1},
			superset: map[string]int{"a": 1, "b": 2},
			expected: true,
		},
		{
			name:     "Subset is not contained in superset",
			subset:   map[string]int{"a": 1, "c": 3},
			superset: map[string]int{"a": 1, "b": 2},
			expected: false,
		},
		{
			name:     "Subset has different values",
			subset:   map[string]int{"a": 2},
			superset: map[string]int{"a": 1, "b": 2},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContainsAll(tt.superset, tt.subset)
			assert.Equal(t, tt.expected, result)
		})
	}
}
