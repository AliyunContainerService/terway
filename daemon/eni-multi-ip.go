package daemon

import (
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"sync"
)

type ENIIPFactory struct {
	eniFactory *ENIFactory
	enis       []*ENI
	sync.RWMutex
}

type ENIIP struct {
	*types.ENIIP
}

type ENI struct {
	lock sync.Mutex
	*types.ENI
	ips  []*ENIIP
	ecs  aliyun.ECS
}

// 尝试绑一个
// 如果满了，返回nil
func (e *ENI) allocateExistENIsIP() *ENIIP {
	e.lock.Lock()
	defer e.lock.Unlock()

	if len(e.ips) < e.ENI.MaxIPs {
		ip, err := e.ecs.AssignIPForENI(e.ENI.ID)
		if err != nil {
			logrus.Errorf("error assign ip for eni: %v", err)
			return nil
		}
		ipNew := &ENIIP{
			ENIIP: &types.ENIIP{
				Eni: e.ENI,
				SecAddress: ip,
			},
		}
		e.ips = append(e.ips, ipNew)
		return ipNew
	}
	return nil
}

func (f *ENIIPFactory) Create() (types.NetworkResource, error) {
	f.RLock()
	for _, eni := range f.enis {
		ip := eni.allocateExistENIsIP()
		if ip != nil {
			f.RUnlock()
			return ip.ENIIP, nil
		}
	}
	f.RUnlock()

	rawEni, err := f.eniFactory.Create()
	if err != nil {
		return nil, err
	}

	eniObj, ok := rawEni.(*types.ENI)
	if !ok {
		return nil, errors.Errorf("error get type eni from factory, got: %v", rawEni)
	}

	eni := &ENI{
		ENI: eniObj,
		ecs: f.eniFactory.ecs,
	}

	mainENIIP := &types.ENIIP{
		Eni: eni.ENI,
		SecAddress: eni.ENI.Address.IP,
	}

	eni.ips = append(eni.ips, &ENIIP{
		ENIIP: mainENIIP,
	})

	f.Lock()
	f.enis = append(f.enis, eni)
	f.Unlock()

	return mainENIIP, nil
}

func (f *ENIIPFactory) Dispose(res types.NetworkResource) error {
	ip := res.(*types.ENIIP)
	var (
		eni *ENI
		eniip *ENIIP
	)
	f.RLock()
	for _, e := range f.enis {
		if ip.Eni.ID == e.ID {
			eni = e
			for _, eip := range e.ips {
				if eip.SecAddress.String() == ip.SecAddress.String() {
					eniip = eip
				}
			}
		}
	}
	f.RUnlock()
	if eni == nil || eniip == nil {
		return fmt.Errorf("invalid resource to dispose")
	}

	ips, err := f.eniFactory.ecs.GetENIIPs(ip.Eni.ID)

	if err != nil {
		return fmt.Errorf("error get eni ips for: %v", ip)
	}

	if len(ips) == 1 {
		// only remain eni main ip address, release the eni interface
		err = f.eniFactory.Dispose(ip.Eni)
		if err != nil {
			return fmt.Errorf("error dispose eni for eniip, %v", err)
		}
		f.Lock()
		for i, e := range f.enis {
			if ip.Eni.ID == e.ID {
				f.enis[len(f.enis)-1], f.enis[i] = f.enis[i], f.enis[len(f.enis)-1]
				f.enis = f.enis[:len(f.enis)-1]
				break
			}
		}
		f.Unlock()
		return nil
	}

	// main ip of eni, raise put_it_back error
	if ip.Eni.Address.IP.Equal(ip.SecAddress) {
		return fmt.Errorf("ip tobe release is primary ip of eni")
	}

	err = f.eniFactory.ecs.UnAssignIPForENI(ip.Eni.ID, ip.SecAddress)
	if err != nil {
		return fmt.Errorf("error unassign eniip, %v", err)
	}
	eni.lock.Lock()
	for i, e := range eni.ips {
		if e.SecAddress.Equal(eniip.SecAddress) {
			eni.ips[len(eni.ips)-1], eni.ips[i] = eni.ips[i], eni.ips[len(eni.ips)-1]
			eni.ips = eni.ips[:len(eni.ips)-1]
			break
		}
	}
	eni.lock.Unlock()
	return nil
}

func newENIIPFactory(config *types.PoolConfig) (*ENIIPFactory, error) {
	return &ENIIPFactory{}, nil
}


type ENIIPResourceManager struct {
	pool pool.ObjectPool
}

func NewENIIPResourceManager(poolConfig *types.PoolConfig, ecs aliyun.ECS, allocatedResources []string) (ResourceManager, error) {
	eniFactory, err := NewENIFactory(poolConfig, ecs)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni factory for eniip factory")
	}

	factory := &ENIIPFactory{
		eniFactory: eniFactory,
		enis: []*ENI{},
	}

	capacity, err := ecs.GetInstanceMaxPrivateIP(poolConfig.InstanceID)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eniip max capacity for eniip factory")
	}

	if poolConfig.MaxPoolSize > capacity {
		logrus.Infof("max pool size bigger than node capacity, set max pool size to capacity")
	}

	poolCfg := pool.PoolConfig{
		MaxIdle: poolConfig.MaxPoolSize,
		MinIdle: poolConfig.MinPoolSize,
		Factory: factory,
		Capacity: capacity,
		Initializer: func(holder pool.ResourceHolder) error {
			// not use main eni for eni multiple ip allocate
			enis, err := ecs.GetAttachedENIs(poolConfig.InstanceID, false)
			if err != nil {
				return errors.Wrapf(err, "error get attach eni on pool init")
			}
			stubMap := make(map[string]bool)
			for _, allocated := range allocatedResources {
				stubMap[allocated] = true
			}

			for _, eni := range enis {
				ips, err := ecs.GetENIIPs(eni.ID)
				if err != nil {
					return errors.Wrapf(err, "error get eni's ip on pool init")
				}
				poolENI := &ENI{
					lock: sync.Mutex{},
					ENI: eni,
					ips: []*ENIIP{},
					ecs: ecs,
				}
				factory.enis = append(factory.enis, poolENI)
				for _, ip := range ips {
					eniIP := &types.ENIIP{
						Eni: eni,
						SecAddress: ip,
					}
					_, ok := stubMap[eniIP.GetResourceId()]

					poolENI.ips = append(poolENI.ips, &ENIIP{
						ENIIP: eniIP,
					})
					if !ok {
						holder.AddIdle(eniIP)
					} else {
						holder.AddInuse(eniIP)
					}
				}
			}
			return nil
		},
	}
	pool, err := pool.NewSimpleObjectPool(poolCfg)
	if err != nil {
		return nil, err
	}
	return &ENIIPResourceManager{
		pool: pool,
	}, nil
}

func (m *ENIIPResourceManager) Allocate(ctx *NetworkContext, prefer string) (types.NetworkResource, error) {
	return m.pool.Acquire(ctx, prefer)
}

func (m *ENIIPResourceManager) Release(context *NetworkContext, resId string) error {
	return m.pool.Release(resId)
}

func (m *ENIIPResourceManager) GarbageCollection(inUseSet map[string]interface{}, expireResSet map[string]interface{}) error {
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