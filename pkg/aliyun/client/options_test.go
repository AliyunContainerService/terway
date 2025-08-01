package client

import (
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

// MockIdempotentKeyGen is a mock implementation of IdempotentKeyGen interface
type MockIdempotentKeyGen struct {
	generatedKeys map[string]string
}

func (m *MockIdempotentKeyGen) GenerateKey(argsHash string) string {
	if _, ok := m.generatedKeys[argsHash]; !ok {
		m.generatedKeys[argsHash] = "mockToken"
	}
	return m.generatedKeys[argsHash]
}

func (m *MockIdempotentKeyGen) PutBack(argsHash string, clientToken string) {
	delete(m.generatedKeys, argsHash)
}

// TestCreateNetworkInterfaceOptions_Finish tests the Finish function of CreateNetworkInterfaceOptions
func TestCreateNetworkInterfaceOptions_Finish(t *testing.T) {
	// Prepare the test data
	niOptions := &NetworkInterfaceOptions{
		VSwitchID:        "vsw-xxxxxx",
		SecurityGroupIDs: []string{"sg-xxxxxx"},
		ResourceGroupID:  "rg-xxxxxx",
		Tags:             map[string]string{"key1": "value1", "key2": "value2"},
		Trunk:            true,
		ERDMA:            true,
		IPCount:          2,
		IPv6Count:        1,
	}

	c := &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: niOptions,
	}

	// Execute the function to be tested
	req, cleanup, err := c.Finish(&MockIdempotentKeyGen{generatedKeys: map[string]string{}})

	// Verify the result
	assert.NoError(t, err)
	assert.NotNil(t, req)
	assert.NotNil(t, cleanup)

	assert.Equal(t, niOptions.VSwitchID, req.VSwitchId)
	assert.Equal(t, ENITypeTrunk, req.InstanceType)
	assert.Equal(t, ENITrafficModeRDMA, req.NetworkInterfaceTrafficMode)
	assert.Equal(t, 1, len(*req.SecurityGroupIds))
	assert.Equal(t, niOptions.ResourceGroupID, req.ResourceGroupId)
	assert.Equal(t, eniDescription, req.Description)
	assert.Equal(t, "mockToken", req.ClientToken)
	assert.Equal(t, requests.NewInteger(1), req.SecondaryPrivateIpAddressCount)
	assert.Equal(t, requests.NewInteger(1), req.Ipv6AddressCount)
	assert.NotNil(t, c.Backoff)

	// Cleanup
	cleanup()
}

func TestCreateNetworkInterfaceOptions_ApplyCreateNetworkInterface(t *testing.T) {
	type fields struct {
		NetworkInterfaceOptions *NetworkInterfaceOptions
		Backoff                 *wait.Backoff
	}
	type args struct {
		options *CreateNetworkInterfaceOptions
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *CreateNetworkInterfaceOptions
	}{
		{
			name: "TestApplyCreateNetworkInterface",
			fields: fields{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					VSwitchID:        "vsw-xxxxxx",
					SecurityGroupIDs: []string{"sg-xxxxxx"},
					ResourceGroupID:  "rg-xxxxxx",
					Tags:             map[string]string{"key1": "value1", "key2": "value2"},
					Trunk:            true,
					ERDMA:            true,
					IPCount:          2,
					IPv6Count:        1,
				},
			},
			args: args{
				options: &CreateNetworkInterfaceOptions{
					NetworkInterfaceOptions: &NetworkInterfaceOptions{},
				},
			},
			want: &CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					Trunk:                 true,
					ERDMA:                 true,
					VSwitchID:             "vsw-xxxxxx",
					SecurityGroupIDs:      []string{"sg-xxxxxx"},
					ResourceGroupID:       "rg-xxxxxx",
					IPCount:               2,
					IPv6Count:             1,
					Tags:                  map[string]string{"key1": "value1", "key2": "value2"},
					InstanceID:            "",
					InstanceType:          "",
					Status:                "",
					NetworkInterfaceID:    "",
					DeleteENIOnECSRelease: nil,
				},
				Backoff: nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: tt.fields.NetworkInterfaceOptions,
				Backoff:                 tt.fields.Backoff,
			}
			c.ApplyCreateNetworkInterface(tt.args.options)
		})
	}
}

func TestApplyTo(t *testing.T) {
	t.Run("All fields are nil", func(t *testing.T) {
		src := &AttachNetworkInterfaceOptions{}
		dst := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID: ptr.To("old-nic"),
			InstanceID:         ptr.To("old-instance"),
		}
		src.ApplyTo(dst)
		assert.Equal(t, "old-nic", *dst.NetworkInterfaceID)
		assert.Equal(t, "old-instance", *dst.InstanceID)
	})

	t.Run("Some fields are non-nil", func(t *testing.T) {
		src := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID: ptr.To("new-nic"),
		}
		dst := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID: ptr.To("old-nic"),
			InstanceID:         ptr.To("old-instance"),
		}
		src.ApplyTo(dst)
		assert.Equal(t, "new-nic", *dst.NetworkInterfaceID)
		assert.Equal(t, "old-instance", *dst.InstanceID)
	})

	t.Run("All fields are non-nil", func(t *testing.T) {
		src := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID: ptr.To("new-nic"),
			InstanceID:         ptr.To("new-instance"),
		}
		dst := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID: ptr.To("old-nic"),
			InstanceID:         ptr.To("old-instance"),
		}
		src.ApplyTo(dst)
		assert.Equal(t, "new-nic", *dst.NetworkInterfaceID)
		assert.Equal(t, "new-instance", *dst.InstanceID)
	})
}

func TestECS(t *testing.T) {
	t.Run("All fields are non-nil", func(t *testing.T) {
		opts := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID:     ptr.To("nic-123"),
			InstanceID:             ptr.To("instance-456"),
			TrunkNetworkInstanceID: ptr.To("trunk-789"),
			NetworkCardIndex:       ptr.To(1),
			Backoff:                &wait.Backoff{Steps: 2},
		}
		req, err := opts.ECS()
		assert.NoError(t, err)
		assert.Equal(t, "nic-123", req.NetworkInterfaceId)
		assert.Equal(t, "instance-456", req.InstanceId)
		assert.Equal(t, "trunk-789", req.TrunkNetworkInstanceId)
		val, _ := req.NetworkCardIndex.GetValue()
		assert.Equal(t, 1, val)
		assert.Equal(t, 2, opts.Backoff.Steps)
	})

	t.Run("Missing required fields", func(t *testing.T) {
		opts := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID: ptr.To("nic-123"),
			InstanceID:         nil,
		}
		_, err := opts.ECS()
		assert.Equal(t, ErrInvalidArgs, err)
	})

	t.Run("Backoff is nil", func(t *testing.T) {
		opts := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID: ptr.To("nic-123"),
			InstanceID:         ptr.To("instance-456"),
			Backoff:            nil,
		}
		_, err := opts.ECS()
		assert.NoError(t, err)
		assert.Equal(t, 1, opts.Backoff.Steps)
	})

	t.Run("Partial fields are nil", func(t *testing.T) {
		opts := &AttachNetworkInterfaceOptions{
			NetworkInterfaceID:     ptr.To("nic-123"),
			InstanceID:             ptr.To("instance-456"),
			TrunkNetworkInstanceID: nil,
			NetworkCardIndex:       nil,
		}
		req, err := opts.ECS()
		assert.NoError(t, err)
		assert.Equal(t, "nic-123", req.NetworkInterfaceId)
		assert.Equal(t, "instance-456", req.InstanceId)
		assert.Empty(t, req.TrunkNetworkInstanceId)
		assert.Equal(t, requests.Integer(""), req.NetworkCardIndex)
	})
}

func TestCreateNetworkInterfaceOptions_EFLO(t *testing.T) {
	tests := []struct {
		name    string
		options *CreateNetworkInterfaceOptions
		wantErr error
	}{
		{
			name: "Valid EFLO options",
			options: &CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					VSwitchID:        "vsw-xxxxxx",
					SecurityGroupIDs: []string{"sg-xxxxxx"},
					InstanceID:       "i-xxxxxx",
					ZoneID:           "cn-hangzhou-a",
					VPCID:            "vpc-xxxxxx",
					IPCount:          1, // EFLO only supports single IP
				},
			},
			wantErr: nil,
		},
		{
			name: "Missing VSwitchID",
			options: &CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					SecurityGroupIDs: []string{"sg-xxxxxx"},
					InstanceID:       "i-xxxxxx",
					ZoneID:           "cn-hangzhou-a",
					VPCID:            "vpc-xxxxxx",
				},
			},
			wantErr: ErrInvalidArgs,
		},
		{
			name: "Multi IP not supported",
			options: &CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					VSwitchID:        "vsw-xxxxxx",
					SecurityGroupIDs: []string{"sg-xxxxxx"},
					IPCount:          2, // EFLO doesn't support multi IP
				},
			},
			wantErr: ErrInvalidArgs,
		},
		{
			name: "Missing SecurityGroupID",
			options: &CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					VSwitchID: "vsw-xxxxxx",
					ZoneID:    "cn-hangzhou-a",
					VPCID:     "vpc-xxxxxx",
				},
			},
			wantErr: ErrInvalidArgs,
		},
		{
			name: "Missing ZoneID",
			options: &CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					VSwitchID:        "vsw-xxxxxx",
					SecurityGroupIDs: []string{"sg-xxxxxx"},
					VPCID:            "vpc-xxxxxx",
				},
			},
			wantErr: ErrInvalidArgs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, cleanup, err := tt.options.EFLO(&MockIdempotentKeyGen{generatedKeys: map[string]string{}})

			if tt.wantErr != nil {
				assert.Equal(t, tt.wantErr, err)
				assert.Nil(t, req)
				assert.Nil(t, cleanup)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, req)
			assert.NotNil(t, cleanup)

			// Verify the request fields
			assert.Equal(t, tt.options.NetworkInterfaceOptions.VSwitchID, req.VSwitchId)
			if len(tt.options.NetworkInterfaceOptions.SecurityGroupIDs) > 0 {
				assert.Equal(t, tt.options.NetworkInterfaceOptions.SecurityGroupIDs[0], req.SecurityGroupId)
			}
			assert.Equal(t, eniDescription, req.Description)
			if tt.options.NetworkInterfaceOptions.InstanceID != "" {
				assert.Equal(t, tt.options.NetworkInterfaceOptions.InstanceID, req.NodeId)
			}
			if tt.options.NetworkInterfaceOptions.ZoneID != "" {
				assert.Equal(t, tt.options.NetworkInterfaceOptions.ZoneID, req.ZoneId)
			}
			if tt.options.NetworkInterfaceOptions.VPCID != "" {
				assert.Equal(t, tt.options.NetworkInterfaceOptions.VPCID, req.VpcId)
			}
			assert.Equal(t, "mockToken", req.ClientToken)

			// Verify backoff is set
			assert.NotNil(t, tt.options.Backoff)
			assert.Equal(t, 1, tt.options.Backoff.Steps)

			// Cleanup
			cleanup()
		})
	}
}

func TestAssignPrivateIPAddressOptions_ApplyAssignPrivateIPAddress(t *testing.T) {
	tests := []struct {
		name   string
		source *AssignPrivateIPAddressOptions
		target *AssignPrivateIPAddressOptions
		want   *AssignPrivateIPAddressOptions
	}{
		{
			name: "Apply with all fields",
			source: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPCount:            2,
				},
				Backoff: &wait.Backoff{Steps: 3},
			},
			target: &AssignPrivateIPAddressOptions{},
			want: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPCount:            2,
				},
				Backoff: &wait.Backoff{Steps: 3},
			},
		},
		{
			name: "Apply with nil backoff",
			source: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPCount:            1,
				},
				Backoff: nil,
			},
			target: &AssignPrivateIPAddressOptions{
				Backoff: &wait.Backoff{Steps: 1},
			},
			want: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPCount:            1,
				},
				Backoff: &wait.Backoff{Steps: 1},
			},
		},
		{
			name: "Apply with existing target",
			source: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-new",
					IPCount:            3,
				},
				Backoff: &wait.Backoff{Steps: 5},
			},
			target: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-old",
					IPCount:            1,
				},
				Backoff: &wait.Backoff{Steps: 1},
			},
			want: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-new",
					IPCount:            3,
				},
				Backoff: &wait.Backoff{Steps: 5},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.source.ApplyAssignPrivateIPAddress(tt.target)

			assert.Equal(t, tt.want.NetworkInterfaceOptions, tt.target.NetworkInterfaceOptions)
			assert.Equal(t, tt.want.Backoff, tt.target.Backoff)
		})
	}
}

func TestAssignPrivateIPAddressOptions_EFLO(t *testing.T) {
	tests := []struct {
		name    string
		options *AssignPrivateIPAddressOptions
		wantErr error
	}{
		{
			name: "Valid EFLO options with single IP",
			options: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPCount:            1,
				},
			},
			wantErr: nil,
		},
		{
			name: "Missing NetworkInterfaceID",
			options: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					IPCount: 1,
				},
			},
			wantErr: ErrInvalidArgs,
		},
		{
			name: "Invalid IP count (not 1)",
			options: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPCount:            2,
				},
			},
			wantErr: ErrInvalidArgs,
		},
		{
			name: "Zero IP count",
			options: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPCount:            0,
				},
			},
			wantErr: ErrInvalidArgs,
		},
		{
			name: "Nil NetworkInterfaceOptions",
			options: &AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: nil,
			},
			wantErr: ErrInvalidArgs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, cleanup, err := tt.options.EFLO(&MockIdempotentKeyGen{generatedKeys: map[string]string{}})

			if tt.wantErr != nil {
				assert.Equal(t, tt.wantErr, err)
				assert.Nil(t, req)
				assert.Nil(t, cleanup)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, req)
			assert.NotNil(t, cleanup)

			// Verify the request fields
			assert.Equal(t, tt.options.NetworkInterfaceOptions.NetworkInterfaceID, req.ElasticNetworkInterfaceId)
			assert.Equal(t, "mockToken", req.ClientToken)

			// Verify backoff is set
			assert.NotNil(t, tt.options.Backoff)
			assert.Equal(t, 1, tt.options.Backoff.Steps)

			// Cleanup
			cleanup()
		})
	}
}

func TestAssignIPv6AddressesOptions_ApplyAssignIPv6Addresses(t *testing.T) {
	tests := []struct {
		name   string
		source *AssignIPv6AddressesOptions
		target *AssignIPv6AddressesOptions
		want   *AssignIPv6AddressesOptions
	}{
		{
			name: "Apply with all fields",
			source: &AssignIPv6AddressesOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPv6Count:          2,
				},
				Backoff: &wait.Backoff{Steps: 3},
			},
			target: &AssignIPv6AddressesOptions{},
			want: &AssignIPv6AddressesOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPv6Count:          2,
				},
				Backoff: &wait.Backoff{Steps: 3},
			},
		},
		{
			name: "Apply with nil backoff",
			source: &AssignIPv6AddressesOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPv6Count:          1,
				},
				Backoff: nil,
			},
			target: &AssignIPv6AddressesOptions{
				Backoff: &wait.Backoff{Steps: 1},
			},
			want: &AssignIPv6AddressesOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-xxxxxx",
					IPv6Count:          1,
				},
				Backoff: &wait.Backoff{Steps: 1},
			},
		},
		{
			name: "Apply with existing target",
			source: &AssignIPv6AddressesOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-new",
					IPv6Count:          3,
				},
				Backoff: &wait.Backoff{Steps: 5},
			},
			target: &AssignIPv6AddressesOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-old",
					IPv6Count:          1,
				},
				Backoff: &wait.Backoff{Steps: 1},
			},
			want: &AssignIPv6AddressesOptions{
				NetworkInterfaceOptions: &NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-new",
					IPv6Count:          3,
				},
				Backoff: &wait.Backoff{Steps: 5},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.source.ApplyAssignIPv6Addresses(tt.target)

			assert.Equal(t, tt.want.NetworkInterfaceOptions, tt.target.NetworkInterfaceOptions)
			assert.Equal(t, tt.want.Backoff, tt.target.Backoff)
		})
	}
}
