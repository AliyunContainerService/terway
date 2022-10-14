package pool

import (
	"context"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client/fake"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/wait"
)

func init() {
	backoff.OverrideBackoff(map[string]wait.Backoff{backoff.WaitENIStatus: {
		Duration: time.Millisecond,
		Factor:   1,
		Steps:    1,
	}})
}

func TestManager_CreateNetworkInterface(t *testing.T) {
	api := fake.New()
	api.VSwitches["vsw-1"] = vpc.VSwitch{
		VSwitchId:     "vsw-1",
		CidrBlock:     "10.0.0.0/24",
		Ipv6CidrBlock: "fd00::/24",
	}

	vswpool, err := vswitch.NewSwitchPool(100, "10m")
	assert.NoError(t, err)

	ctx := context.Background()
	mgr := NewManager(&Config{
		IPv4Enable:       true,
		IPv6Enable:       true,
		VSwitchIDs:       []string{"vsw-1"},
		SecurityGroupIDs: []string{"sg-1"},
		ENITags:          map[string]string{"foo": "bar"},
		MaxENI:           5,
		MaxIdle:          0,
		MinIdle:          0,
		TrunkENIID:       "foo",
	}, nil, vswpool, api)

	resp, err := mgr.CreateNetworkInterface(ctx, false, "vsw-1", []string{"sg-1"}, "", 1, 1, map[string]string{"foo": "bar"})
	assert.NoError(t, err)

	assert.Equal(t, 1, len(resp.Tags))
	assert.Equal(t, 1, len(resp.PrivateIPSets))
	assert.Equal(t, 1, len(resp.IPv6Set))
	assert.Equal(t, "Secondary", resp.Type)

	v := mgr.allocations.Load(resp.NetworkInterfaceID)
	assert.NotNil(t, v)

	assert.Equal(t, AllocPolicyDirect, v.AllocType)
	assert.Equal(t, StatusInUse, v.Status)

	describeResp, err := mgr.DescribeNetworkInterface(ctx, "", []string{resp.NetworkInterfaceID}, "", "", "", nil)
	assert.NoError(t, err)
	assert.Equal(t, "Available", describeResp[0].Status)

	err = mgr.AttachNetworkInterface(ctx, resp.NetworkInterfaceID, "", "foo")
	assert.NoError(t, err)

	describeResp, err = mgr.DescribeNetworkInterface(ctx, "", []string{resp.NetworkInterfaceID}, "", "", "", nil)
	assert.NoError(t, err)
	assert.Equal(t, "InUse", describeResp[0].Status)
	assert.Equal(t, "Member", resp.Type)

	// after detach status will not change
	err = mgr.DetachNetworkInterface(ctx, resp.NetworkInterfaceID, "", "foo")
	assert.NoError(t, err)

	v = mgr.allocations.Load(resp.NetworkInterfaceID)
	assert.NotNil(t, v)
	assert.Equal(t, StatusInUse, v.Status)
	assert.Equal(t, "Secondary", resp.Type)

	// after delete, should have gone
	err = mgr.DeleteNetworkInterface(ctx, resp.NetworkInterfaceID)
	assert.NoError(t, err)

	v = mgr.allocations.Load(resp.NetworkInterfaceID)
	assert.Nil(t, v)
}

func TestManager_CreateNetworkInterfaceWithCached(t *testing.T) {
	api := fake.New()
	api.VSwitches["vsw-1"] = vpc.VSwitch{
		VSwitchId:     "vsw-1",
		CidrBlock:     "10.0.0.0/24",
		Ipv6CidrBlock: "fd00::/24",
	}

	vswpool, err := vswitch.NewSwitchPool(100, "10m")
	assert.NoError(t, err)

	ctx := AllocTypeWithCtx(context.Background(), AllocPolicyPreferPool)

	mgr := NewManager(&Config{
		IPv4Enable:       true,
		IPv6Enable:       true,
		VSwitchIDs:       []string{"vsw-1"},
		SecurityGroupIDs: []string{"sg-1"},
		ENITags:          map[string]string{"foo": "bar"},
		MaxENI:           5,
		MaxIdle:          0,
		MinIdle:          2,
		TrunkENIID:       "foo",
	}, nil, vswpool, api)

	resp, err := mgr.CreateNetworkInterface(ctx, false, "vsw-1", []string{"sg-1"}, "", 1, 1, map[string]string{"foo": "bar"})
	assert.NoError(t, err)

	assert.Equal(t, 1, len(resp.PrivateIPSets))
	assert.Equal(t, 1, len(resp.IPv6Set))

	v := mgr.allocations.Load(resp.NetworkInterfaceID)
	assert.NotNil(t, v)

	assert.Equal(t, AllocPolicyPreferPool, v.AllocType)
	assert.Equal(t, StatusInUse, v.Status)
	assert.Equal(t, "Member", resp.Type)

	// the CreateNetworkInterface cover with attachNetworkInterface , the status should be InUse
	describeResp, err := mgr.DescribeNetworkInterface(ctx, "", []string{resp.NetworkInterfaceID}, "", "", "", nil)
	assert.NoError(t, err)
	assert.Equal(t, "InUse", describeResp[0].Status)

	err = mgr.DetachNetworkInterface(ctx, resp.NetworkInterfaceID, "", "foo")
	assert.NoError(t, err)

	// after delete, should have not gone
	err = mgr.DeleteNetworkInterface(ctx, resp.NetworkInterfaceID)
	assert.NoError(t, err)

	v = mgr.allocations.Load(resp.NetworkInterfaceID)
	assert.NotNil(t, v)
	assert.Equal(t, StatusIdle, v.Status)

	prevENIID := resp.NetworkInterfaceID
	// re alloc, should use same eni
	resp, err = mgr.CreateNetworkInterface(ctx, false, "vsw-1", []string{"sg-1", "sg-2"}, "", 1, 1, map[string]string{"foo": "bar"})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(resp.SecurityGroupIDs))
	assert.Equal(t, 2, len(resp.SecurityGroupIDs))
	assert.Equal(t, prevENIID, resp.NetworkInterfaceID)

	v = mgr.allocations.Load(resp.NetworkInterfaceID)
	assert.NotNil(t, v)
	assert.Equal(t, StatusInUse, v.Status)
}

func TestManager_CreateNetworkInterfaceWithWarnPool(t *testing.T) {
	api := fake.New()
	api.VSwitches["vsw-1"] = vpc.VSwitch{
		VSwitchId:               "vsw-1",
		CidrBlock:               "10.0.0.0/24",
		Ipv6CidrBlock:           "fd00::/24",
		ZoneId:                  "zone-a",
		AvailableIpAddressCount: 100,
	}

	vswpool, err := vswitch.NewSwitchPool(100, "10m")
	assert.NoError(t, err)

	mgr := NewManager(&Config{
		IPv4Enable:       true,
		IPv6Enable:       true,
		VSwitchIDs:       []string{"vsw-1"},
		SecurityGroupIDs: []string{"sg-1"},
		ZoneID:           "zone-a",
		ENITags:          map[string]string{"foo": "bar"},
		MaxENI:           5,
		MaxIdle:          2,
		MinIdle:          2,
		SyncPeriod:       100 * time.Millisecond,
	}, nil, vswpool, api)
	go mgr.Run()
	defer mgr.Stop()

	time.Sleep(2 * time.Second)

	i := 0
	mgr.allocations.Range(func(key string, value *Allocation) bool {
		i++
		return true
	})
	assert.Equal(t, 2, i)
}

func TestManager_CreateNetworkInterfaceWithRob(t *testing.T) {
	api := fake.New()
	api.VSwitches["vsw-1"] = vpc.VSwitch{
		VSwitchId:     "vsw-1",
		CidrBlock:     "10.0.0.0/24",
		Ipv6CidrBlock: "fd00::/24",
	}

	vswpool, err := vswitch.NewSwitchPool(100, "10m")
	assert.NoError(t, err)

	ctx := AllocTypeWithCtx(context.Background(), AllocPolicyPreferPool)

	mgr := NewManager(&Config{
		IPv4Enable:       true,
		IPv6Enable:       true,
		VSwitchIDs:       []string{"vsw-1"},
		SecurityGroupIDs: []string{"sg-1"},
		ENITags:          map[string]string{"foo": "bar"},
		MaxENI:           1,
		MaxIdle:          0,
		MinIdle:          0,
		TrunkENIID:       "foo",
	}, nil, vswpool, api)

	resp, err := mgr.CreateNetworkInterface(ctx, false, "vsw-1", []string{"sg-1"}, "", 1, 1, map[string]string{"foo": "bar"})
	assert.NoError(t, err)

	assert.Equal(t, 1, len(resp.PrivateIPSets))
	assert.Equal(t, 1, len(resp.IPv6Set))

	v := mgr.allocations.Load(resp.NetworkInterfaceID)
	assert.NotNil(t, v)

	// release this
	v.SetStatus(StatusIdle)

	resp2, err := mgr.CreateNetworkInterface(context.Background(), false, "vsw-1", []string{"sg-1"}, "", 1, 1, map[string]string{"foo": "bar"})
	assert.NoError(t, err)

	v = mgr.allocations.Load(resp.NetworkInterfaceID)
	assert.Nil(t, v)

	v = mgr.allocations.Load(resp2.NetworkInterfaceID)
	assert.NotNil(t, v)
}
