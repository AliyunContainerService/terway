package daemon

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/tracing"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/types"
	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/pkg/errors"
)

const (
	// vSwitchIPCntTimeout is the duration for the vswitchIPCntMap content's effectiveness
	vSwitchIPCntTimeout = 10 * time.Minute

	typeNameENI    = "eni"
	poolNameENI    = "eni-%s"
	factoryNameENI = "eni-%s"

	tracingKeyVSwitches              = "vswitches"
	tracingKeyVSwitchSelectionPolicy = "vswitch_selection_policy"
	tracingKeyCacheExpireAt          = "cache_expire_at"
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

	_ = tracing.Register(tracing.ResourceTypeFactory, "eni", factory)

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
		Name:     fmt.Sprintf(poolNameENI, randomString()),
		Type:     typeNameENI,
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
	mgr := &eniResourceManager{
		pool: pool,
		ecs:  ecs,
	}

	return mgr, nil
}

func (m *eniResourceManager) Allocate(ctx *networkContext, prefer string) (types.NetworkResource, error) {
	return m.pool.Acquire(ctx, prefer, podInfoKey(ctx.pod.Namespace, ctx.pod.Name))
}

func (m *eniResourceManager) Release(context *networkContext, resID string) error {
	if context != nil && context.pod != nil {
		return m.pool.ReleaseWithReservation(resID, context.pod.IPStickTime)
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
func (m *eniResourceManager) GetResourceMapping() ([]tracing.ResourceMapping, error) {
	return m.pool.GetResourceMapping()
}

// MapSorter is a slice container for sorting
type MapSorter []Item

// Item is the element type of MapSorter, which contains the values for sorting
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

// SortInDescendingOrder is a bubble sort per element's value
func (ms MapSorter) SortInDescendingOrder() {
	logrus.Debugf("before sorting, slice = %+v", ms)
	sort.Slice(ms, func(i, j int) bool {
		// reverse sorting
		return ms[i].Val > ms[j].Val
	})
	logrus.Debugf("after sorting, slice = %+v", ms)
}

type eniFactory struct {
	name                   string
	switches               []string
	securityGroup          string
	instanceID             string
	ecs                    aliyun.ECS
	vswitchIPCntMap        map[string]int
	tsExpireAt             time.Time
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
		name:                   fmt.Sprintf(factoryNameENI, randomString()),
		switches:               poolConfig.VSwitch,
		securityGroup:          poolConfig.SecurityGroup,
		instanceID:             poolConfig.InstanceID,
		ecs:                    ecs,
		vswitchIPCntMap:        make(map[string]int),
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
		rand.Shuffle(vswCnt, func(i, j int) { vSwitches[i], vSwitches[j] = vSwitches[j], vSwitches[i] })
		return vSwitches, nil
	}

	if f.vswitchSelectionPolicy == types.VSwitchSelectionPolicyOrdered {
		// If VSwitchSelectionPolicy is ordered, then call f.ecs.DescribeVSwitch API to get the switch's available IP count
		// PS: this is only feasible for systems with RAM policy for VPC API permission.
		// Use f.vswitchIPCntMap to track IP count + vswitch ID
		var (
			start        = time.Now()
			err          error
			availIPCount int
		)
		// If f.vswitchIPCntMap is empty, then fill in the map with switch + switch's available IP count.
		f.Lock()
		if (len(f.vswitchIPCntMap) == 0 && f.tsExpireAt.IsZero()) || start.After(f.tsExpireAt) {
			// Loop vswitch slice to get each vswitch's available IP count.
			for _, vswitch := range f.switches {
				availIPCount, err = f.ecs.DescribeVSwitch(vswitch)
				if err != nil {
					f.vswitchIPCntMap[vswitch] = 0
				} else {
					f.vswitchIPCntMap[vswitch] = availIPCount
				}
			}
			if err == nil {
				// don't cache result when error
				f.tsExpireAt = time.Now().Add(vSwitchIPCntTimeout)
			}
		}
		f.Unlock()

		if len(f.vswitchIPCntMap) > 0 {
			m := newMapSorter(f.vswitchIPCntMap)
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

func (f *eniFactory) Create(int) ([]types.NetworkResource, error) {
	return f.CreateWithIPCount(1)
}

func (f *eniFactory) CreateWithIPCount(count int) ([]types.NetworkResource, error) {
	vSwitches, _ := f.GetVSwitches()
	logrus.Infof("adjusted vswitch slice: %+v", vSwitches)
	eni, err := f.ecs.AllocateENI(vSwitches[0], f.securityGroup, f.instanceID, count)
	if err != nil {
		return nil, err
	}
	return []types.NetworkResource{eni}, nil
}

func (f *eniFactory) Dispose(resource types.NetworkResource) error {
	eni := resource.(*types.ENI)
	return f.ecs.FreeENI(eni.ID, f.instanceID)
}

func (f *eniFactory) Config() []tracing.MapKeyValueEntry {
	config := []tracing.MapKeyValueEntry{
		{Key: tracingKeyName, Value: f.name},
		{Key: tracingKeyVSwitches, Value: strings.Join(f.switches, " ")},
		{Key: tracingKeyVSwitchSelectionPolicy, Value: f.vswitchSelectionPolicy},
	}

	return config
}

func (f *eniFactory) Trace() []tracing.MapKeyValueEntry {
	trace := []tracing.MapKeyValueEntry{
		{Key: tracingKeyCacheExpireAt, Value: fmt.Sprint(f.tsExpireAt)},
	}

	for vs, cnt := range f.vswitchIPCntMap {
		key := fmt.Sprintf("vswitch/%s/ip_count", vs)
		trace = append(trace, tracing.MapKeyValueEntry{
			Key:   key,
			Value: fmt.Sprint(cnt),
		})
	}

	return trace
}

func (f *eniFactory) Execute(cmd string, _ []string, message chan<- string) {
	switch cmd {
	case commandMapping:
		mapping, err := f.GetResourceMapping()
		message <- fmt.Sprintf("mapping: %v, err: %s\n", mapping, err)
	default:
		message <- "can't recognize command\n"
	}

	close(message)
}

func (f *eniFactory) GetResourceMapping() ([]tracing.FactoryResourceMapping, error) {
	// Get ENIs from Aliyun API
	enis, err := f.ecs.GetAttachedENIs(f.instanceID, false)
	if err != nil {
		return nil, err
	}

	var mapping []tracing.FactoryResourceMapping

	for _, eni := range enis {
		m := tracing.FactoryResourceMapping{
			ResID: eni.GetResourceID(),
			ENI:   eni,
			ENIIP: nil,
		}

		mapping = append(mapping, m)
	}

	return mapping, nil
}
