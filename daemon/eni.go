package daemon

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/ipam"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
)

var eniLog = logger.DefaultLogger

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
	pool     pool.ObjectPool
	ecs      ipam.API
	trunkENI *types.ENI
}

func newENIResourceManager(poolConfig *types.PoolConfig, ecs ipam.API, allocatedResources map[string]resourceManagerInitItem, ipFamily *types.IPFamily, k8s Kubernetes) (ResourceManager, error) {
	eniLog.Debugf("new ENI Resource Manager, pool config: %+v, allocated resources: %+v", poolConfig, allocatedResources)
	factory, err := newENIFactory(poolConfig, ecs)
	if err != nil {
		return nil, errors.Wrapf(err, "error create ENI factory")
	}

	_ = tracing.Register(tracing.ResourceTypeFactory, factoryNameENI, factory)

	var capacity, memberLimit int

	if !poolConfig.DisableDevicePlugin {
		limit, err := aliyun.GetLimit(ecs, aliyun.GetInstanceMeta().InstanceType)
		if err != nil {
			return nil, fmt.Errorf("error get max eni for eni factory, %w", err)
		}
		capacity = limit.Adapters
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

		memberLimit = limit.MemberAdapterLimit
		if poolConfig.ENICapPolicy == types.ENICapPolicyPreferTrunk {
			memberLimit = limit.MaxMemberAdapterLimit
		}
	} else {
		// NB(l1b0k): adapt DisableDevicePlugin func, will refactor latter
		capacity = 1
		memberLimit = 0
		poolConfig.MaxPoolSize = 1
		poolConfig.MinPoolSize = 0
	}

	var trunkENI *types.ENI

	if poolConfig.WaitTrunkENI {
		logger.DefaultLogger.Infof("waitting trunk eni ready")
		factory.trunkOnEni, err = k8s.WaitTrunkReady()
		if err != nil {
			return nil, err
		}
		logger.DefaultLogger.Infof("trunk eni found %s", factory.trunkOnEni)
	}

	poolCfg := pool.Config{
		Name:     poolNameENI,
		Type:     typeNameENI,
		MaxIdle:  poolConfig.MaxPoolSize,
		MinIdle:  poolConfig.MinPoolSize,
		Capacity: capacity,
		Factory:  factory,
		Initializer: func(holder pool.ResourceHolder) error {
			ctx := context.Background()
			enis, err := ecs.GetAttachedENIs(ctx, false)
			if err != nil {
				return fmt.Errorf("error get attach ENI on pool init, %w", err)
			}

			if factory.enableTrunk {
				logger.DefaultLogger.Infof("lookup trunk eni")
				for _, eni := range enis {
					if eni.Trunk {
						logger.DefaultLogger.Infof("find trunk eni %s", eni.ID)
						factory.trunkOnEni = eni.ID
					}
					if eni.ID == factory.trunkOnEni {
						trunkENI = eni
						eni.Trunk = true
					}
				}
				if factory.trunkOnEni == "" && len(enis) < memberLimit {
					trunkENIRes, err := factory.CreateWithIPCount(1, true)
					if err != nil {
						return errors.Wrapf(err, "error init trunk eni")
					}
					trunkENI, _ = trunkENIRes[0].(*types.ENI)
					factory.trunkOnEni = trunkENI.ID
					enis = append(enis, trunkENI)
				}
			}
			for _, e := range enis {
				if ipFamily.IPv6 {
					_, ipv6, err := ecs.GetENIIPs(ctx, e.MAC)
					if err != nil || len(ipv6) == 0 {
						return errors.Wrapf(err, "error get eni ip")
					}
					e.PrimaryIP.IPv6 = ipv6[0]
				}
				if item, ok := allocatedResources[e.GetResourceID()]; ok {
					holder.AddInuse(e, podInfoKey(item.podInfo.Namespace, item.podInfo.Name))
				} else {
					holder.AddIdle(e)
				}
			}
			return nil
		},
	}

	p, err := pool.NewSimpleObjectPool(poolCfg)
	if err != nil {
		return nil, err
	}
	mgr := &eniResourceManager{
		pool:     p,
		ecs:      ecs,
		trunkENI: trunkENI,
	}

	if poolConfig.DisableDevicePlugin {
		return mgr, nil
	}
	//init deviceplugin for ENI
	realCap := 0
	eniType := deviceplugin.ENITypeENI
	if !poolConfig.EnableENITrunking {
		realCap = capacity
	}

	// report only trunk is created
	if poolConfig.EnableENITrunking && factory.trunkOnEni != "" {
		eniType = deviceplugin.ENITypeMember
		realCap = memberLimit
		err = k8s.PatchTrunkInfo(factory.trunkOnEni)
		if err != nil {
			return nil, errors.Wrapf(err, "error patch trunk info on node")
		}
	}
	logger.DefaultLogger.Infof("set deviceplugin cap %d", realCap)
	dp := deviceplugin.NewENIDevicePlugin(realCap, eniType)
	err = dp.Serve()
	if err != nil {
		return nil, errors.Wrapf(err, "error set deviceplugin on node")
	}

	return mgr, nil
}

func (m *eniResourceManager) Allocate(ctx *networkContext, prefer string) (types.NetworkResource, error) {
	return m.pool.Acquire(ctx, prefer, podInfoKey(ctx.pod.Namespace, ctx.pod.Name))
}

func (m *eniResourceManager) Release(context *networkContext, resItem types.ResourceItem) error {
	if context != nil && context.pod != nil {
		return m.pool.ReleaseWithReservation(resItem.ID, context.pod.IPStickTime)
	}
	return m.pool.Release(resItem.ID)
}

func (m *eniResourceManager) GarbageCollection(inUseResSet map[string]types.ResourceItem, expireResSet map[string]types.ResourceItem) error {
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
	eniLog.Debugf("before sorting, slice = %+v", ms)
	sort.Slice(ms, func(i, j int) bool {
		// reverse sorting
		return ms[i].Val > ms[j].Val
	})
	eniLog.Debugf("after sorting, slice = %+v", ms)
}

type eniFactory struct {
	name                      string
	enableTrunk               bool
	trunkOnEni                string
	switches                  []string
	eniTags                   map[string]string
	securityGroups            []string
	instanceID                string
	ecs                       ipam.API
	vswitchIPCntMap           map[string]int
	tsExpireAt                time.Time
	vswitchSelectionPolicy    string
	disableSecurityGroupCheck bool
	sync.RWMutex
}

func newENIFactory(poolConfig *types.PoolConfig, ecs ipam.API) (*eniFactory, error) {
	if len(poolConfig.SecurityGroups) == 0 {
		securityGroups, err := ecs.GetAttachedSecurityGroups(context.Background(), poolConfig.InstanceID)
		if err != nil {
			return nil, errors.Wrapf(err, "error get security group on factory init")
		}
		poolConfig.SecurityGroups = securityGroups
	}
	return &eniFactory{
		name:                      factoryNameENI,
		switches:                  poolConfig.VSwitch,
		eniTags:                   poolConfig.ENITags,
		securityGroups:            poolConfig.SecurityGroups,
		enableTrunk:               poolConfig.EnableENITrunking,
		instanceID:                poolConfig.InstanceID,
		ecs:                       ecs,
		vswitchIPCntMap:           make(map[string]int),
		vswitchSelectionPolicy:    poolConfig.VSwitchSelectionPolicy,
		disableSecurityGroupCheck: poolConfig.DisableSecurityGroupCheck,
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
				vsw, err = f.ecs.DescribeVSwitchByID(context.Background(), vswitch)
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
	eniLog.Infof("adjusted vswitch slice: %+v", vSwitches)

	tags := map[string]string{
		types.NetworkInterfaceTagCreatorKey: types.NetworkInterfaceTagCreatorValue,
	}
	for k, v := range f.eniTags {
		tags[k] = v
	}
	eni, err := f.ecs.AllocateENI(context.Background(), vSwitches[0], f.securityGroups, f.instanceID, trunk, count, tags)
	if err != nil {
		return nil, err
	}
	return []types.NetworkResource{eni}, nil
}

func (f *eniFactory) Dispose(resource types.NetworkResource) error {
	eni := resource.(*types.ENI)
	if f.enableTrunk && eni.Trunk {
		return fmt.Errorf("trunk ENI %+v will not dispose", eni.ID)
	}
	return f.ecs.FreeENI(context.Background(), eni.ID, f.instanceID)
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
		mapping, err := f.ListResource()
		message <- fmt.Sprintf("mapping: %v, err: %s\n", mapping, err)
	default:
		message <- "can't recognize command\n"
	}

	close(message)
}

func (f *eniFactory) Check(res types.NetworkResource) error {
	eni, ok := res.(*types.ENI)
	if !ok {
		return fmt.Errorf("unsupported type %T", res)
	}
	_, err := f.ecs.GetENIByMac(context.Background(), eni.MAC)
	return err
}

func (f *eniFactory) ListResource() (map[string]types.NetworkResource, error) {
	enis, err := f.ecs.GetAttachedENIs(context.Background(), false)
	if err != nil {
		return nil, err
	}

	mapping := make(map[string]types.NetworkResource, len(enis))
	for i := 0; i < len(enis); i++ {
		mapping[enis[i].GetResourceID()] = enis[i]
	}

	return mapping, nil
}

func (f *eniFactory) Reconcile() {
	// check security group
	if f.disableSecurityGroupCheck {
		return
	}
	err := f.ecs.CheckEniSecurityGroup(context.Background(), f.securityGroups)
	if err != nil {
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning, "ResourceInvalid", fmt.Sprintf("eni has misconfiged security group. %s", err.Error()))
	}
}
