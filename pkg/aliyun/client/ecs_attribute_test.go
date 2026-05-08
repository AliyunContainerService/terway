package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSingleENIQuery(t *testing.T) {
	tests := []struct {
		name     string
		opts     []DescribeNetworkInterfaceOption
		wantID   string
		wantSingle bool
	}{
		{
			name: "single ENI ID, no filters",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
				},
			},
			wantID:   "eni-123",
			wantSingle: true,
		},
		{
			name: "multiple ENI IDs",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-1", "eni-2"},
				},
			},
			wantSingle: false,
		},
		{
			name:       "no options",
			opts:       nil,
			wantSingle: false,
		},
		{
			name: "nil NetworkInterfaceIDs",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{},
			},
			wantSingle: false,
		},
		{
			name: "empty NetworkInterfaceIDs",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{},
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with InstanceID filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					InstanceID:          strToPtr("i-abc"),
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with VPCID filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					VPCID:              strToPtr("vpc-abc"),
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with Tags filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					Tags:               &map[string]string{"k": "v"},
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with Status filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					Status:             strToPtr("InUse"),
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with InstanceType filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					InstanceType:       strToPtr("Secondary"),
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with RawStatus only (no additional filters)",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					RawStatus:          boolToPtr(true),
				},
			},
			wantID:   "eni-123",
			wantSingle: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotSingle := isSingleENIQuery(tt.opts)
			assert.Equal(t, tt.wantSingle, gotSingle)
			if tt.wantSingle {
				assert.Equal(t, tt.wantID, gotID)
			}
		})
	}
}

func strToPtr(s string) *string {
	return &s
}

func boolToPtr(b bool) *bool {
	return &b
}
