package daemon

import (
	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/types"
	"github.com/sirupsen/logrus"
	"math/rand"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/pkg/errors"
)

const (
	VSwitchIPCntTimeout = 10 * time.Minute
)

type eniResourceManager struct {
	pool pool.ObjectPool
	ecs  aliyun.ECS
}

func newENIResourceManager(poolConfig *types.PoolConfig, ecs aliyun.ECS, allocatedResources []resourceManagerInitItem) (ResourceManager, error) {
	logrus.Debugf("new ENI Resource Manager, pool config: %+v, allocated resources: %+v", poolConfig, allocatedResources)
	factory, err := newENIFactory(poolConfig, ecs)
	if err != nil {
		return nil, errors.Wrapf(err, "error create ENI factory")
	}

	capacity, err := ecs.GetInstanceMaxENI(poolConfig.InstanceID)
	if err != nil {
		return nil, errors.Wrapf(err, "error get ENI max capacity for ENI factory")
	}

	capacity = int(float64(capacity)*poolConfig.EniCapRatio) + poolConfig.EniCapShift - 1

	if poolConfig.MaxENI != 0 && poolConfig.MaxENI < capacity {
		capacity = poolConfig.MaxENI
	}

	if poolConfig.MaxPoolSize > capacity {
		poolConfig.MaxPoolSize = capacity
	}

	if poolConfig.MinENI != 0 {
		poolConfig.MinPoolSize = poolConfig.MinENI
	}

	poolCfg := pool.Config{
		MaxIdle:  poolConfig.MaxPoolSize,
		MinIdle:  poolConfig.MinPoolSize,
		Capacity: capacity,
		Factory:  factory,
		Initializer: func(holder pool.ResourceHolder) error {
			enis, err := ecs.GetAttachedENIs(poolConfig.InstanceID, false)
			if err != nil {
				return errors.Wrapf(err, "error get attach ENI on pool init")
			}
			allocatedMap := make(map[string]*podInfo)
			for _, allocated := range allocatedResources {
				allocatedMap[allocated.resourceID] = allocated.podInfo
			}
			for _, e := range enis {
				if podInfo, ok := allocatedMap[e.GetResourceID()]; ok {
					holder.AddInuse(e, podInfoKey(podInfo.Namespace, podInfo.Name))
				} else {
					holder.AddIdle(e)
				}
			}
			return nil
		},
	}

	//init deviceplugin for ENI
	dp := deviceplugin.NewEniDevicePlugin(capacity)
	err = dp.Serve(deviceplugin.DefaultResourceName)
	if err != nil {
		return nil, errors.Wrapf(err, "error set deviceplugin on node")
	}

	pool, err := pool.NewSimpleObjectPool(poolCfg)
	if err != nil {
		return nil, err
	}
	return &eniResourceManager{
		pool: pool,
		ecs:  ecs,
	}, nil
}

func (m *eniResourceManager) Allocate(ctx *networkContext, prefer string) (types.NetworkResource, error) {
	return m.pool.Acquire(ctx, prefer, podInfoKey(ctx.pod.Namespace, ctx.pod.Name))
}

func (m *eniResourceManager) Release(context *networkContext, resID string) error {
	if context != nil && context.pod != nil {
		return m.pool.ReleaseWithReverse(resID, context.pod.IPStickTime)
	}
	return m.pool.Release(resID)
}

func (m *eniResourceManager) GarbageCollection(inUseSet map[string]interface{}, expireResSet map[string]interface{}) error {
	for expireRes := range expireResSet {
		if err := m.pool.Stat(expireRes); err == nil {
			err = m.Release(nil, expireRes)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

type MapSorter []Item
type Item struct {
	Key string
	Val int
}
func newMapSorter(m map[string]int) MapSorter {
	ms := make(MapSorter, 0, len(m))
	for k, v := range m {
		ms = append(ms, Item{k, v})
	}
	return ms
}

func (ms MapSorter) SortInDescendingOrder()  {
	logrus.Debugf("before bubble sorting, slice = %+v", ms)
	for i := 0; i < ms.Len(); i++ {
		for j := 0; j < ms.Len()-i-1; j++ {
			if ms[j].Val < ms[j+1].Val {
				ms.Swap(j, j+1)
			}
		}
	}
	logrus.Debugf("after bubble sorting, slice = %+v", ms)

}
func (ms MapSorter) Len() int {
	return len(ms)
}
func (ms MapSorter) Less(i, j int) bool {
	//return ms[i].Key < ms[j].Key // order by key
	return ms[i].Val < ms[j].Val // order by value
}
func (ms MapSorter) Swap(i, j int) {
	ms[i], ms[j] = ms[j], ms[i]
}

type eniFactory struct {
	switches      []string
	securityGroup string
	instanceID    string
	ecs           aliyun.ECS
	vswitchIpCntMap map[string]int
	tsExpireAt time.Time
	vswitchSelectionPolicy string
	sync.RWMutex
}

func newENIFactory(poolConfig *types.PoolConfig, ecs aliyun.ECS) (*eniFactory, error) {
	if poolConfig.SecurityGroup == "" {
		securityGroup, err := ecs.GetAttachedSecurityGroup(poolConfig.InstanceID)
		if err != nil {
			return nil, errors.Wrapf(err, "error get security group on factory init")
		}
		poolConfig.SecurityGroup = securityGroup
	}
	return &eniFactory{
		switches:      poolConfig.VSwitch,
		securityGroup: poolConfig.SecurityGroup,
		instanceID:    poolConfig.InstanceID,
		ecs:           ecs,
		vswitchIpCntMap: make(map[string]int),
		vswitchSelectionPolicy: poolConfig.VSwitchSelectionPolicy,
	}, nil
}

func (f *eniFactory) GetVSwitches() ([]string, error) {

	var vSwitches []string

	vswCnt := len(f.switches)
	// If there is ONLY ONE vswitch, then there is no need for ordering per switches' available IP counts,
	// return the slice with only this vswitch.
	if vswCnt == 1 {
		return f.switches, nil
	}

	if f.vswitchSelectionPolicy == types.VSwitchSelectionPolicyRandom {
		vSwitches = make([]string, vswCnt)
		copy(vSwitches, f.switches)
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(vswCnt, func(i, j int){ vSwitches[i], vSwitches[j] = vSwitches[j], vSwitches[i] })
		return vSwitches, nil
	}

	if f.vswitchSelectionPolicy == types.VSwitchSelectionPolicyOrdered {
		// If VSwitchSelectionPolicy is ordered, then call f.ecs.DescribeVSwitch API to get the switch's available IP count
		// PS: this is only feasible for systems with RAM policy for VPC API permission.
		// Use f.vswitchIpCntMap to track IP count + vswitch ID
		var start = time.Now()
		// If f.vswitchIpCntMap is empty, then fill in the map with switch + switch's available IP count.
		if (len(f.vswitchIpCntMap) == 0 && f.tsExpireAt.IsZero()) || start.After(f.tsExpireAt)  {
			// Loop vswitch slice to get each vswitch's available IP count.
			for _, vswitch := range f.switches {
				availIpCount, err := f.ecs.DescribeVSwitch(vswitch)
				f.Lock()
				if err != nil {
					f.vswitchIpCntMap[vswitch] = 0
				} else {
					f.vswitchIpCntMap[vswitch] = availIpCount
				}
				f.Unlock()
			}

			f.Lock()
			f.tsExpireAt = time.Now().Add(VSwitchIPCntTimeout)
			f.Unlock()
		}

		if len(f.vswitchIpCntMap) > 0 {
			m := newMapSorter(f.vswitchIpCntMap)
			//sort.Sort(sort.Reverse(m))
			m.SortInDescendingOrder()
			for _, item := range m {
				vSwitches = append(vSwitches, item.Key)
			}
		} else {
			vSwitches = f.switches
		}
	}

	return vSwitches, nil
}

func (f *eniFactory) Create() (types.NetworkResource, error) {
	vSwitches, _ := f.GetVSwitches()
	logrus.Infof("adjusted vswitch slice: %+v", vSwitches)
	return f.ecs.AllocateENI(vSwitches[0], f.securityGroup, f.instanceID)
}

func (f *eniFactory) Dispose(resource types.NetworkResource) error {
	eni := resource.(*types.ENI)
	return f.ecs.FreeENI(eni.ID, f.instanceID)
}
