package daemon

import (
	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/types"

	_ "github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/pool"
	//"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/pkg/errors"
)

type ENIResourceManager struct {
	pool pool.ObjectPool
	ecs  aliyun.ECS
}

func NewENIResourceManager(poolConfig *types.PoolConfig, ecs aliyun.ECS, allocatedIPs []string) (ResourceManager, error) {
	factory, err := NewENIFactory(poolConfig, ecs)
	if err != nil {
		return nil, errors.Wrapf(err, "error create eni factory")
	}

	capacity, err := ecs.GetInstanceMaxENI(poolConfig.InstanceID)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni max capacity for eni factory")
	}

	capacity = int(float64(capacity) * poolConfig.EniCapRatio) + poolConfig.EniCapShift - 1
	if poolConfig.MaxPoolSize > capacity {
		poolConfig.MaxPoolSize = capacity
	}
	poolCfg := pool.PoolConfig{
		MaxIdle:  poolConfig.MaxPoolSize,
		MinIdle:  poolConfig.MinPoolSize,
		Capacity: capacity,
		Factory:  factory,
		Initializer: func(holder pool.ResourceHolder) error {
			enis, err := ecs.GetAttachedENIs(poolConfig.InstanceID, false)
			if err != nil {
				return errors.Wrapf(err, "error get attach eni on pool init")
			}
			allocatedMap := make(map[string]bool)
			for _, allocated := range allocatedIPs {
				allocatedMap[allocated] = true
			}
			for _, e := range enis {
				if _, ok := allocatedMap[e.Address.IP.String()]; ok {
					holder.AddInuse(e)
				} else {
					holder.AddIdle(e)
				}
			}
			return nil
		},
	}

	//init deviceplugin for eni
	dp := deviceplugin.NewEniDevicePlugin(capacity)
	err = dp.Serve(deviceplugin.DefaultResourceName)
	if err != nil {
		return nil, errors.Wrapf(err, "error set deviceplugin on node")
	}

	pool, err := pool.NewSimpleObjectPool(poolCfg)
	if err != nil {
		return nil, err
	}
	return &ENIResourceManager{
		pool: pool,
		ecs:  ecs,
	}, nil
}

func (m *ENIResourceManager) Allocate(ctx *NetworkContext, prefer string) (types.NetworkResource, error) {
	return m.pool.Acquire(ctx, prefer)
}

func (m *ENIResourceManager) Release(context *NetworkContext, resId string) error {
	return m.pool.Release(resId)
}

func (m *ENIResourceManager) GarbageCollection(inUseResList []string, expireResList []string) error {
	for _, expireRes := range expireResList {
		if err := m.pool.Stat(expireRes); err == nil {
			err = m.Release(nil, expireRes)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

type ENIFactory struct {
	switches      []string
	securityGroup string
	instanceId    string
	ecs           aliyun.ECS
}

func NewENIFactory(poolConfig *types.PoolConfig, ecs aliyun.ECS) (*ENIFactory, error) {
	return &ENIFactory{
		switches:      poolConfig.VSwitch,
		securityGroup: poolConfig.SecurityGroup,
		instanceId:    poolConfig.InstanceID,
		ecs:           ecs,
	}, nil
}

func (f *ENIFactory) Create() (types.NetworkResource, error) {
	//TODO 支持多个交换机
	return f.ecs.AllocateENI(f.switches[0], f.securityGroup, f.instanceId)
}

func (f *ENIFactory) Dispose(resource types.NetworkResource) error {
	eni := resource.(*types.ENI)
	return f.ecs.FreeENI(eni.ID, f.instanceId)
}