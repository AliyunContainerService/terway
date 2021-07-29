package daemon

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

const (
	// vSwitchIPCntTimeout is the duration for the vswitchIPCntMap content's effectiveness
	vSwitchIPCntTimeout = 10 * time.Minute

	typeNameENI    = "eni"
	poolNameENI    = "eni"
	factoryNameENI = "eni"

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

	_ = tracing.Register(tracing.ResourceTypeFactory, factoryNameENI, factory)

	limit, ok := aliyun.GetLimit(aliyun.GetInstanceMeta().InstanceType)
	if !ok {
		return nil, errors.Wrapf(err, "error get max eni for eni factory")
	}
	capacity := limit.Adapters
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
		Name:     poolNameENI,
		Type:     typeNameENI,
		MaxIdle:  poolConfig.MaxPoolSize,
		MinIdle:  poolConfig.MinPoolSize,
		Capacity: capacity,
		Factory:  factory,
		Initializer: func(holder pool.ResourceHolder) error {
			enis, err := ecs.GetAttachedENIs(false)
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
	dp := deviceplugin.NewENIDevicePlugin(capacity, deviceplugin.ENITypeENI)
	err = dp.Serve()
	if err != nil {
		return nil, errors.Wrapf(err, "error set deviceplugin on node")
	}

	p, err := pool.NewSimpleObjectPool(poolCfg)
	if err != nil {
		return nil, err
	}
	mgr := &eniResourceManager{
		pool: p,
		ecs:  ecs,
	}

	return mgr, nil
}

func (m *eniResourceManager) Allocate(ctx *networkContext, prefer string) (types.NetworkResource, error) {
	return m.pool.Acquire(ctx, prefer, podInfoKey(ctx.pod.Namespace, ctx.pod.Name))
}

func (m *eniResourceManager) Release(context *networkContext, resItem ResourceItem) error {
	if context != nil && context.pod != nil {
		return m.pool.ReleaseWithReservation(resItem.ID, context.pod.IPStickTime)
	}
	return m.pool.Release(resItem.ID)
}

func (m *eniResourceManager) GarbageCollection(inUseResSet map[string]ResourceItem, expireResSet map[string]ResourceItem) error {
	for expireRes, expireItem := range expireResSet {
		if _, err := m.pool.Stat(expireRes); err == nil {
			err = m.Release(nil, expireItem)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *eniResourceManager) Stat(context *networkContext, resID string) (types.NetworkResource, error) {
	return m.pool.Stat(resID)
}

func (m *eniResourceManager) GetResourceMapping() (tracing.ResourcePoolStats, error) {
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
	eniTags                map[string]string
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
		securityGroups, err := ecs.GetAttachedSecurityGroups(poolConfig.InstanceID)
		if err != nil {
			return nil, errors.Wrapf(err, "error get security group on factory init")
		}
		poolConfig.SecurityGroup = securityGroups[0]
	}
	return &eniFactory{
		name:                   factoryNameENI,
		switches:               poolConfig.VSwitch,
		eniTags:                poolConfig.ENITags,
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
			start = time.Now()
			err   error
		)
		// If f.vswitchIPCntMap is empty, then fill in the map with switch + switch's available IP count.
		f.Lock()
		if (len(f.vswitchIPCntMap) == 0 && f.tsExpireAt.IsZero()) || start.After(f.tsExpireAt) {
			// Loop vswitch slice to get each vswitch's available IP count.
			for _, vswitch := range f.switches {
				var vsw *vpc.VSwitch
				vsw, err = f.ecs.DescribeVSwitchByID(vswitch)
				if err != nil {
					f.vswitchIPCntMap[vswitch] = 0
				} else {
					f.vswitchIPCntMap[vswitch] = int(vsw.AvailableIpAddressCount)
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
	return f.CreateWithIPCount(1, false)
}

func (f *eniFactory) CreateWithIPCount(count int, trunk bool) ([]types.NetworkResource, error) {
	vSwitches, _ := f.GetVSwitches()
	logrus.Infof("adjusted vswitch slice: %+v", vSwitches)

	tags := map[string]string{
		types.NetworkInterfaceTagCreatorKey: types.NetworkInterfaceTagCreatorValue,
	}
	for k, v := range f.eniTags {
		tags[k] = v
	}
	eni, err := f.ecs.AllocateENI(vSwitches[0], f.securityGroup, f.instanceID, trunk, count, tags)
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
		mapping, err := f.GetResource()
		message <- fmt.Sprintf("mapping: %v, err: %s\n", mapping, err)
	default:
		message <- "can't recognize command\n"
	}

	close(message)
}

func (f *eniFactory) Get(res types.NetworkResource) (types.NetworkResource, error) {
	eni := res.(*types.ENI)
	return f.ecs.GetENIByMac(eni.MAC)
}

func (f *eniFactory) GetResource() (map[string]types.FactoryResIf, error) {
	// Get ENIs from Aliyun API
	enis, err := f.ecs.GetAttachedENIs(false)
	if err != nil {
		return nil, err
	}

	mapping := make(map[string]types.FactoryResIf, len(enis))
	for i := 0; i < len(enis); i++ {
		mapping[enis[i].GetResourceID()] = &types.FactoryRes{
			ID:   enis[i].GetResourceID(),
			Type: enis[i].GetType(),
		}
	}

	return mapping, nil
}

func (f *eniFactory) Reconcile() {
	// check security group
	err := f.ecs.CheckEniSecurityGroup([]string{f.securityGroup})
	if err != nil {
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning, "ResourceInvalid", fmt.Sprintf("eni has misconfiged security group. %s", err.Error()))
	}
}
