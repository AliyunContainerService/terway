/*
Copyright 2022 Terway Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vswitch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
)

func TestSwitchPool_GetOne(t *testing.T) {
	openAPI := mocks.NewVPC(t)
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-0").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-0",
		ZoneId:                  "zone-0",
		AvailableIpAddressCount: 0,
	}, nil).Maybe()
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-1",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 10,
	}, nil).Maybe()
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-2").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-2",
		ZoneId:                  "zone-2",
		AvailableIpAddressCount: 10,
	}, nil).Maybe()
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-3").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-3",
		ZoneId:                  "zone-2",
		AvailableIpAddressCount: 10,
	}, nil).Maybe()

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	for i := 0; i < 10; i++ {
		sw, err := switchPool.GetOne(context.Background(), openAPI, "zone-2", []string{"vsw-2", "vsw-3"}, &SelectOptions{
			IgnoreZone:          false,
			VSwitchSelectPolicy: VSwitchSelectionPolicyOrdered,
		})
		assert.NoError(t, err)
		assert.Equal(t, "vsw-2", sw.ID)
	}

	ids := make(map[string]struct{})
	for i := 0; i < 10; i++ {
		sw, err := switchPool.GetOne(context.Background(), openAPI, "zone-2", []string{"vsw-2", "vsw-3"}, &SelectOptions{
			IgnoreZone:          false,
			VSwitchSelectPolicy: VSwitchSelectionPolicyRandom,
		})
		assert.NoError(t, err)
		ids[sw.ID] = struct{}{}
	}

	assert.Equal(t, 2, len(ids))

	_, err = switchPool.GetOne(context.Background(), openAPI, "zone-x", []string{"vsw-2", "vsw-3"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyRandom,
	})

	assert.True(t, errors.Is(err, ErrNoAvailableVSwitch))
	assert.False(t, errors.Is(err, ErrIPNotEnough))

	_, err = switchPool.GetOne(context.Background(), openAPI, "zone-0", []string{"vsw-0"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyRandom,
	})

	assert.True(t, errors.Is(err, ErrNoAvailableVSwitch))
	assert.True(t, errors.Is(err, ErrIPNotEnough))
}

func TestGetByID(t *testing.T) {
	openAPI := mocks.NewVPC(t)

	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-2").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-2",
		ZoneId:                  "zone-2",
		AvailableIpAddressCount: 10,
	}, nil).Maybe()
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-3").Return(nil, fmt.Errorf("err")).Maybe()

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)
	switchPool.Add(&Switch{
		ID:               "vsw-1",
		Zone:             "zone-1",
		AvailableIPCount: 10,
	})
	// Test when switch is in cache
	switchObj, err := switchPool.GetByID(context.Background(), openAPI, "vsw-1")
	assert.NoError(t, err)
	assert.NotNil(t, switchObj)
	assert.Equal(t, "vsw-1", switchObj.ID)

	// Test when switch is not in cache
	switchObj, err = switchPool.GetByID(context.Background(), openAPI, "vsw-2")
	assert.NoError(t, err)
	assert.NotNil(t, switchObj)
	assert.Equal(t, "vsw-2", switchObj.ID)

	// Test when DescribeVSwitchByID returns error
	switchObj, err = switchPool.GetByID(context.Background(), openAPI, "vsw-3")
	assert.Error(t, err)
	assert.Nil(t, switchObj)
}

func TestSwitchPool_GetOne_WithEmptySwitches(t *testing.T) {
	openAPI := mocks.NewVPC(t)
	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	_, err = switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyRandom,
	})
	assert.True(t, errors.Is(err, ErrNoAvailableVSwitch))
}

func TestSwitchPool_GetOne_WithNilOptions(t *testing.T) {
	openAPI := mocks.NewVPC(t)
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-1",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 10,
	}, nil)

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	sw, err := switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-1"})
	assert.NoError(t, err)
	assert.Equal(t, "vsw-1", sw.ID)
}

func TestSwitchPool_GetOne_IgnoreZone(t *testing.T) {
	openAPI := mocks.NewVPC(t)
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-1",
		ZoneId:                  "zone-2", // Different zone
		AvailableIpAddressCount: 10,
	}, nil)

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	// Test with IgnoreZone=true
	sw, err := switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-1"}, &SelectOptions{
		IgnoreZone:          true,
		VSwitchSelectPolicy: VSwitchSelectionPolicyRandom,
	})
	assert.NoError(t, err)
	assert.Equal(t, "vsw-1", sw.ID)

	// Test with IgnoreZone=false
	_, err = switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-1"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyRandom,
	})
	assert.True(t, errors.Is(err, ErrNoAvailableVSwitch))
}

func TestSwitchPool_CacheExpiration(t *testing.T) {
	openAPI := mocks.NewVPC(t)
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-1",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 10,
	}, nil)

	// Create switch pool with very short TTL for testing cache expiration
	switchPool, err := NewSwitchPool(100, "1ms")
	assert.NoError(t, err)

	// Add switch to cache
	switchPool.Add(&Switch{
		ID:               "vsw-1",
		Zone:             "zone-1",
		AvailableIPCount: 10,
	})

	// First access should use cache
	sw, err := switchPool.GetByID(context.Background(), openAPI, "vsw-1")
	assert.NoError(t, err)
	assert.Equal(t, "vsw-1", sw.ID)
	openAPI.AssertNotCalled(t, "DescribeVSwitchByID")

	// Wait for cache to expire
	time.Sleep(10 * time.Millisecond)

	// Second access should call API after cache expiration
	sw, err = switchPool.GetByID(context.Background(), openAPI, "vsw-1")
	assert.NoError(t, err)
	assert.Equal(t, "vsw-1", sw.ID)
	openAPI.AssertExpectations(t)
}

func TestSwitchPool_GetOne_WithErrorFromAPI(t *testing.T) {
	openAPI := mocks.NewVPC(t)
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(nil, fmt.Errorf("api error"))

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	_, err = switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-1"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyRandom,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "api error")
}

func TestSwitchPool_GetOne_WithMostPolicy(t *testing.T) {
	openAPI := mocks.NewVPC(t)

	// Mock different vswitches with different available IP counts
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-few").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-few",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 5,
	}, nil).Maybe()

	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-many").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-many",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 100,
	}, nil).Maybe()

	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-medium").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-medium",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 50,
	}, nil).Maybe()

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	// Test VSwitchSelectionPolicyMost - should select the vswitch with most available IPs
	sw, err := switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-few", "vsw-many", "vsw-medium"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyMost,
	})
	assert.NoError(t, err)
	assert.Equal(t, "vsw-many", sw.ID)
	assert.Equal(t, int64(100), sw.AvailableIPCount)

	// Test again with different order to ensure sorting works correctly
	sw, err = switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-medium", "vsw-few", "vsw-many"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyMost,
	})
	assert.NoError(t, err)
	assert.Equal(t, "vsw-many", sw.ID)
	assert.Equal(t, int64(100), sw.AvailableIPCount)
}

func TestSwitchPool_Block(t *testing.T) {
	openAPI := mocks.NewVPC(t)
	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	// Test blocking a switch that exists in cache
	switchPool.Add(&Switch{
		ID:               "vsw-1",
		Zone:             "zone-1",
		AvailableIPCount: 10,
	})

	// Verify the switch has available IPs before blocking
	sw, err := switchPool.GetByID(context.Background(), openAPI, "vsw-1")
	assert.NoError(t, err)
	assert.Equal(t, int64(10), sw.AvailableIPCount)

	// Block the switch
	switchPool.Block("vsw-1")

	// Verify the switch now has 0 available IPs
	sw, err = switchPool.GetByID(context.Background(), openAPI, "vsw-1")
	assert.NoError(t, err)
	assert.Equal(t, int64(0), sw.AvailableIPCount)

	// Test blocking a switch that doesn't exist in cache (should not panic)
	switchPool.Block("vsw-nonexistent")

	// Test blocking with empty string
	switchPool.Block("")
}

func TestSwitchPool_Block_AffectsSelection(t *testing.T) {
	openAPI := mocks.NewVPC(t)

	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-1",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 10,
	}, nil).Maybe()

	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-2").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-2",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 5,
	}, nil).Maybe()

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	// Initially both switches should be available
	sw, err := switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-1", "vsw-2"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyOrdered,
	})
	assert.NoError(t, err)
	assert.Equal(t, "vsw-1", sw.ID)

	// Block the first switch
	switchPool.Block("vsw-1")

	// Now the second switch should be selected
	sw, err = switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-1", "vsw-2"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyOrdered,
	})
	assert.NoError(t, err)
	assert.Equal(t, "vsw-2", sw.ID)
}
