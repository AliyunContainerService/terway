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

// TestSwitchPool_GetOne_WithMostPolicy_GetByIDError tests the scenario where GetByID returns errors
// during the sorting phase with VSwitchSelectionPolicyMost. This covers lines 90-92 in vswitch.go
// where errors are logged and processing continues with the next vSwitch.
func TestSwitchPool_GetOne_WithMostPolicy_GetByIDError(t *testing.T) {
	openAPI := mocks.NewVPC(t)

	// Mock vswitches: one will fail, others will succeed
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-error").Return(nil, fmt.Errorf("api error")).Maybe()
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-1",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 10,
	}, nil).Maybe()
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-2").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-2",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 20,
	}, nil).Maybe()

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	// Test with VSwitchSelectionPolicyMost where one vSwitch fails to get
	// The error should be logged and processing should continue with other vSwitches
	sw, err := switchPool.GetOne(context.Background(), openAPI, "zone-1", []string{"vsw-error", "vsw-1", "vsw-2"}, &SelectOptions{
		IgnoreZone:          false,
		VSwitchSelectPolicy: VSwitchSelectionPolicyMost,
	})
	assert.NoError(t, err)
	// Should select vsw-2 which has the most available IPs (20)
	assert.Equal(t, "vsw-2", sw.ID)
	assert.Equal(t, int64(20), sw.AvailableIPCount)
}

// TestSwitchPool_GetOne_FallbackSwitches_IPNotEnough tests the scenario where fallback switches
// have AvailableIPCount == 0. This covers lines 132-134 in vswitch.go where errors are added
// and processing continues.
func TestSwitchPool_GetOne_FallbackSwitches_IPNotEnough(t *testing.T) {
	openAPI := mocks.NewVPC(t)

	// Mock vswitches in different zones with IgnoreZone=true
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-zone1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-zone1",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 0, // No IPs available
	}, nil).Maybe()
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-zone2").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-zone2",
		ZoneId:                  "zone-2",
		AvailableIpAddressCount: 10, // Has IPs available
	}, nil).Maybe()
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-zone3").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-zone3",
		ZoneId:                  "zone-3",
		AvailableIpAddressCount: 0, // No IPs available
	}, nil).Maybe()

	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	// Test with IgnoreZone=true, requesting zone-0 but switches are in other zones
	// vsw-zone1 and vsw-zone3 have 0 IPs, should be skipped
	// vsw-zone2 has IPs, should be selected
	sw, err := switchPool.GetOne(context.Background(), openAPI, "zone-0", []string{"vsw-zone1", "vsw-zone2", "vsw-zone3"}, &SelectOptions{
		IgnoreZone:          true,
		VSwitchSelectPolicy: VSwitchSelectionPolicyOrdered,
	})
	assert.NoError(t, err)
	assert.Equal(t, "vsw-zone2", sw.ID)
	assert.Equal(t, int64(10), sw.AvailableIPCount)

	// Test case where all fallback switches have 0 IPs
	_, err = switchPool.GetOne(context.Background(), openAPI, "zone-0", []string{"vsw-zone1", "vsw-zone3"}, &SelectOptions{
		IgnoreZone:          true,
		VSwitchSelectPolicy: VSwitchSelectionPolicyOrdered,
	})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoAvailableVSwitch))
	assert.True(t, errors.Is(err, ErrIPNotEnough))
}

// TestSwitchPool_Del tests the Del method which removes a switch from cache.
// This covers lines 187-191 in vswitch.go. This is for test purposes.
func TestSwitchPool_Del(t *testing.T) {
	openAPI := mocks.NewVPC(t)
	switchPool, err := NewSwitchPool(100, "100m")
	assert.NoError(t, err)

	// Add switches to cache
	switchPool.Add(&Switch{
		ID:               "vsw-1",
		Zone:             "zone-1",
		AvailableIPCount: 10,
	})
	switchPool.Add(&Switch{
		ID:               "vsw-2",
		Zone:             "zone-2",
		AvailableIPCount: 20,
	})

	// Verify switches are in cache
	sw1, err := switchPool.GetByID(context.Background(), openAPI, "vsw-1")
	assert.NoError(t, err)
	assert.NotNil(t, sw1)
	assert.Equal(t, "vsw-1", sw1.ID)

	sw2, err := switchPool.GetByID(context.Background(), openAPI, "vsw-2")
	assert.NoError(t, err)
	assert.NotNil(t, sw2)
	assert.Equal(t, "vsw-2", sw2.ID)

	// Delete vsw-1 from cache
	switchPool.Del("vsw-1")

	// Verify vsw-1 is no longer in cache (will need to fetch from API)
	// Create a new mock to verify API is called after deletion
	openAPI2 := mocks.NewVPC(t)
	openAPI2.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-1",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 10,
	}, nil).Once()

	sw1AfterDel, err := switchPool.GetByID(context.Background(), openAPI2, "vsw-1")
	assert.NoError(t, err)
	assert.NotNil(t, sw1AfterDel)
	assert.Equal(t, "vsw-1", sw1AfterDel.ID)
	// API should have been called since it's not in cache
	openAPI2.AssertExpectations(t)

	// Verify vsw-2 is still in cache (API should not be called)
	openAPI3 := mocks.NewVPC(t)
	sw2AfterDel, err := switchPool.GetByID(context.Background(), openAPI3, "vsw-2")
	assert.NoError(t, err)
	assert.NotNil(t, sw2AfterDel)
	assert.Equal(t, "vsw-2", sw2AfterDel.ID)
	// API should not have been called since it's still in cache
	openAPI3.AssertNotCalled(t, "DescribeVSwitchByID", mock.Anything, "vsw-2")

	// Test deleting a non-existent switch (should not panic)
	switchPool.Del("vsw-nonexistent")

	// Test deleting with empty string (should not panic)
	switchPool.Del("")
}
