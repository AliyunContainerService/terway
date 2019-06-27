package daemon

import (
	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/types"
	//"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/pkg/errors"
)

type eniResourceManager struct {
	pool pool.ObjectPool
	ecs  aliyun.ECS
}

func newENIResourceManager(poolConfig *types.PoolConfig, ecs aliyun.ECS, allocatedResource []string) (ResourceManager, error) {
	factory, err := newENIFactory(poolConfig, ecs)
	if err != nil {
		return nil, errors.Wrapf(err, "error create ENI factory")
	}

	capacity, err := ecs.GetInstanceMaxENI(poolConfig.InstanceID)
	if err != nil {
		return nil, errors.Wrapf(err, "error get ENI max capacity for ENI factory")
	}

	capacity = int(float64(capacity)*poolConfig.EniCapRatio) + poolConfig.EniCapShift - 1
	if poolConfig.MaxPoolSize > capacity {
		poolConfig.MaxPoolSize = capacity
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
			allocatedMap := make(map[string]bool)
			for _, allocated := range allocatedResource {
				allocatedMap[allocated] = true
			}
			for _, e := range enis {
				if _, ok := allocatedMap[e.ID]; ok {
					holder.AddInuse(e)
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
	return m.pool.Acquire(ctx, prefer)
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

type eniFactory struct {
	switches      []string
	securityGroup string
	instanceID    string
	ecs           aliyun.ECS
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
	}, nil
}

func (f *eniFactory) Create() (types.NetworkResource, error) {
	//TODO support multi vswitch
	return f.ecs.AllocateENI(f.switches[0], f.securityGroup, f.instanceID)
}

func (f *eniFactory) Dispose(resource types.NetworkResource) error {
	eni := resource.(*types.ENI)
	return f.ecs.FreeENI(eni.ID, f.instanceID)
}
