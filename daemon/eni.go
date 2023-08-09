package daemon

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"

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
	// vSwitchIPCntTimeout is the duration for the vswitchCnt content's effectiveness
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

func newENIResourceManager(poolConfig *types.PoolConfig, ecs ipam.API, allocatedResources map[string]resourceManagerInitItem, ipFamily *types.IPFamily, k8s Kubernetes, ipamType types.IPAMType) (ResourceManager, error) {
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
		Name:               poolNameENI,
		Type:               typeNameENI,
		MaxIdle:            poolConfig.MaxPoolSize,
		MinIdle:            poolConfig.MinPoolSize,
		Capacity:           capacity,
		Factory:            factory,
		IPConditionHandler: k8s.PatchNodeIPResCondition,
		Initializer: func(holder pool.ResourceHolder) error {
			if ipamType == types.IPAMTypeCRD {
				return nil
			}
			ctx := context.Background()
			enis, err := ecs.GetAttachedENIs(ctx, false, factory.trunkOnEni)
			if err != nil {
				return fmt.Errorf("error get attach ENI on pool init, %w", err)
			}

			if factory.enableTrunk && memberLimit > 0 {
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
				if factory.trunkOnEni == "" && len(enis) < capacity-1 {
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

	if capacity > 0 {
		err = k8s.PatchAvailableIPs(types.NormalIPTypeIPs, capacity)
		if err != nil {
			return nil, errors.Wrapf(err, "error patch available ips")
		}
	}

	logger.DefaultLogger.Infof("set deviceplugin cap %d", realCap)
	dp := deviceplugin.NewENIDevicePlugin(realCap, eniType)
	go dp.Serve()

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

type vswitch struct {
	id      string
	ipCount int
}

func (v *vswitch) String() string {
	return fmt.Sprintf("%s(%d)", v.id, v.ipCount)
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
	vswitchCnt                []vswitch
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
		vswitchCnt:                make([]vswitch, 0),
		vswitchSelectionPolicy:    poolConfig.VSwitchSelectionPolicy,
		disableSecurityGroupCheck: poolConfig.DisableSecurityGroupCheck,
	}, nil
}

func (f *eniFactory) GetVSwitches() ([]vswitch, error) {

	var vSwitches []vswitch

	vswCnt := len(f.switches)
	// If there is ONLY ONE vswitch, then there is no need for ordering per switches' available IP counts,
	// return the slice with only this vswitch.
	if vswCnt == 1 {
		return []vswitch{{
			id:      f.switches[0],
			ipCount: 0,
		}}, nil
	}

	if f.vswitchSelectionPolicy == types.VSwitchSelectionPolicyRandom {
		vSwitches = lo.Map(f.switches, func(item string, index int) vswitch {
			return vswitch{
				id:      item,
				ipCount: 0,
			}
		})
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(vswCnt, func(i, j int) { vSwitches[i], vSwitches[j] = vSwitches[j], vSwitches[i] })
		return vSwitches, nil
	}

	if f.vswitchSelectionPolicy == types.VSwitchSelectionPolicyOrdered {
		// If VSwitchSelectionPolicy is ordered, then call f.ecs.DescribeVSwitch API to get the switch's available IP count
		// PS: this is only feasible for systems with RAM policy for VPC API permission.
		// Use f.vswitchCnt to track IP count + vswitch ID
		var (
			start = time.Now()
			err   error
		)
		// If f.vswitchCnt is empty, then fill in the map with switch + switch's available IP count.
		f.Lock()
		if (len(f.vswitchCnt) == 0 && f.tsExpireAt.IsZero()) || start.After(f.tsExpireAt) {
			f.vswitchCnt = make([]vswitch, 0)
			// Loop vsw slice to get each vsw's available IP count.
			for _, vswID := range f.switches {
				var vsw *vpc.VSwitch
				vsw, err = f.ecs.DescribeVSwitchByID(context.Background(), vswID)
				if err != nil {
					f.vswitchCnt = append(f.vswitchCnt, vswitch{
						id:      vswID,
						ipCount: 0,
					})
				} else {
					f.vswitchCnt = append(f.vswitchCnt, vswitch{
						id:      vswID,
						ipCount: int(vsw.AvailableIpAddressCount),
					})
				}
			}
			if err == nil {
				// don't cache result when error
				f.tsExpireAt = time.Now().Add(vSwitchIPCntTimeout)
			}
		}
		f.Unlock()

		if len(f.vswitchCnt) > 0 {
			sort.Slice(f.vswitchCnt, func(i, j int) bool {
				return f.vswitchCnt[i].ipCount > f.vswitchCnt[j].ipCount
			})
		} else {
			vSwitches = lo.Map(f.switches, func(item string, index int) vswitch {
				return vswitch{
					id:      item,
					ipCount: 0,
				}
			})
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
	eni, err := f.ecs.AllocateENI(context.Background(), vSwitches[0].id, f.securityGroups, f.instanceID, trunk, count, tags)
	if err != nil {
		if strings.Contains(err.Error(), apiErr.InvalidVSwitchIDIPNotEnough) {
			reportIPExhaustive := false
			if len(vSwitches) == 1 {
				reportIPExhaustive = true
			}
			if f.vswitchSelectionPolicy == types.VSwitchSelectionPolicyOrdered {
				reportIPExhaustive = true
			}
			if reportIPExhaustive {
				return nil, &types.IPInsufficientError{
					Err:    err,
					Reason: fmt.Sprintf("all configure vswitches: %v has no available ip address", vSwitches)}
			}
		} else if strings.Contains(err.Error(), apiErr.ErrEniPerInstanceLimitExceeded) {
			return nil, &types.IPInsufficientError{
				Err:    err,
				Reason: fmt.Sprintf("instance %v exceeded max eni limit", f.instanceID)}
		} else if strings.Contains(err.Error(), apiErr.ErrSecurityGroupInstanceLimitExceed) {
			return nil, &types.IPInsufficientError{
				Err:    err,
				Reason: fmt.Sprintf("security group %v exceeded max ip limit", f.securityGroups)}
		}
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

	for _, cnt := range f.vswitchCnt {
		key := fmt.Sprintf("vswitch/%s/ip_count", cnt.id)
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
	enis, err := f.ecs.GetAttachedENIs(context.Background(), false, f.trunkOnEni)
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
