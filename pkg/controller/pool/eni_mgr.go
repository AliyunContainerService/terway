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

package pool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc

	aliyun      register.Interface
	vSwitchPool *vswitch.SwitchPool

	cfg *Config

	allocations *AllocManager
}

func NewManager(cfg *Config, previous map[string]*Allocation, vSwitchPool *vswitch.SwitchPool, aliyun register.Interface) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := &Manager{
		ctx:         ctx,
		cancel:      cancel,
		cfg:         cfg,
		vSwitchPool: vSwitchPool,
		allocations: NewAllocManager(cfg.MaxENI, cfg.TrunkENIID != ""),
		aliyun:      aliyun,
	}

	for _, alloc := range previous {
		mgr.allocations.Add(alloc)
	}

	return mgr
}

var _ register.Interface = &Manager{}

func (m *Manager) Stop() {
	m.cancel()
}

func (m *Manager) Run() {
	l := ctrl.Log.WithName("eni-pool")
	l.Info("pool manage start", "node", m.cfg.NodeName, "maxENI", m.cfg.MaxENI)

	period := m.cfg.SyncPeriod
	if period <= 0 {
		period = 30 * time.Second
	}
	wait.JitterUntilWithContext(m.ctx, func(ctx context.Context) {
		m.cleanUP(ctx)
	}, period, 1.2, true)

	l.Info("pool manage exited", "node", m.cfg.NodeName)
}

func (m *Manager) CreateNetworkInterface(ctx context.Context, trunk bool, vSwitchID string, securityGroups []string, resourceGroupID string, ipCount, ipv6Count int, eniTags map[string]string) (*aliyunClient.NetworkInterface, error) {
	realClient, play, err := common.Became(ctx, m.aliyun)
	if err != nil {
		return nil, err
	}

	l := log.FromContext(ctx).WithValues("rolePlay", fmt.Sprintf("%t", play))

	if eniTags == nil {
		eniTags = make(map[string]string)
	}

	allocPolicy := AllocPolicyCtx(ctx)
	if allocPolicy == AllocPolicyPreferPool {
		eniTags[types.TagENIAllocPolicy] = "pool"
		eniTags[types.TagK8SNodeName] = m.cfg.NodeName

		cached := m.allocations.Alloc(vSwitchID)
		if cached != nil {
			// use cached res
			l.Info("alloc eni", "id", cached.NetworkInterfaceID, "cache", "true", "sg", strings.Join(securityGroups, ","))
			err = m.aliyun.ModifyNetworkInterfaceAttribute(context.Background(), cached.NetworkInterfaceID, securityGroups)
			if err != nil {
				m.allocations.Release(cached.NetworkInterfaceID)
				return nil, err
			}
			cached.SecurityGroupIDs = securityGroups
			return cached, nil
		}
	}

	// check we have reached max eni
	if !m.allocations.RequireQuota() {
		// pop one idle
		cached := m.allocations.ReleaseIdle()
		if cached == nil {
			return nil, ErrMaxENI
		}

		l.Info("rob cache eni to dispose", "id", cached.NetworkInterfaceID)

		// detach and delete cached eni
		err = detachNetworkInterface(ctx, m.aliyun, cached.NetworkInterfaceID, cached.InstanceID, cached.TrunkNetworkInterfaceID)
		if err != nil {
			l.Error(err, "rob eni, detach failed")
			return nil, err
		}

		time.Sleep(backoff.Backoff(backoff.WaitENIStatus).Duration)

		err = m.aliyun.DeleteNetworkInterface(ctx, cached.NetworkInterfaceID)
		if err != nil {
			l.Error(err, "rob eni, delete failed")
			return nil, err
		}
		m.allocations.Dispose(cached.NetworkInterfaceID)
	}
	defer m.allocations.ReleaseQuota()

	// create new
	result, err := realClient.CreateNetworkInterface(ctx, trunk, vSwitchID, securityGroups, resourceGroupID, ipCount, ipv6Count, eniTags)
	if err != nil {
		return nil, err
	}

	defer func() {
		alloc := &Allocation{
			NetworkInterface: result,
			Status:           StatusInUse,
			AllocType:        allocPolicy,
		}
		if err != nil {
			alloc.Status = StatusDeleting
		}

		m.allocations.Add(alloc)
		l.Info("pool add eni", "id", result.NetworkInterfaceID, "type", result.Type, "status", alloc.Status)
	}()

	if allocPolicy == AllocPolicyPreferPool {
		// do attach
		var attachResp *aliyunClient.NetworkInterface
		attachResp, err = attachNetworkInterface(ctx, m.aliyun, result.NetworkInterfaceID, m.cfg.InstanceID, m.cfg.TrunkENIID)
		if err != nil {
			return nil, err
		}
		result.Type = attachResp.Type
		result.Status = attachResp.Status
		result.ResourceGroupID = attachResp.ResourceGroupID
		result.DeviceIndex = attachResp.DeviceIndex
		result.InstanceID = attachResp.InstanceID
		result.CreationTime = attachResp.CreationTime
		result.TrunkNetworkInterfaceID = attachResp.TrunkNetworkInterfaceID
	}

	return result, nil
}

func (m *Manager) AttachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	alloc := m.allocations.Load(eniID)
	if alloc != nil && alloc.AllocType == AllocPolicyPreferPool {
		return nil
	}
	// check status and do attach option

	return m.aliyun.AttachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
}

func (m *Manager) DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	l := log.FromContext(ctx)
	alloc := m.allocations.Load(eniID)
	if alloc != nil && alloc.AllocType == AllocPolicyPreferPool {
		l.Info("detach eni", "id", eniID, "cache", "false")
		m.allocations.Release(eniID)
		return nil
	}
	l.Info("detach eni", "id", eniID, "cache", "true")
	return m.aliyun.DetachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
}

func (m *Manager) DeleteNetworkInterface(ctx context.Context, eniID string) (err error) {
	l := log.FromContext(ctx)
	alloc := m.allocations.Load(eniID)
	if alloc != nil && alloc.AllocType == AllocPolicyPreferPool {
		l.Info("delete eni", "id", eniID, "cache", "true")
		m.allocations.Release(eniID)
		return nil
	}
	defer func() {
		if err == nil {
			m.allocations.Dispose(eniID)
		}
	}()
	l.Info("delete eni", "id", eniID, "cache", "false")
	realClient, _, err := common.Became(ctx, m.aliyun)
	if err != nil {
		return err
	}

	return realClient.DeleteNetworkInterface(ctx, eniID)
}

func (m *Manager) DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error) {
	panic("implement me")
}

func (m *Manager) DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType string, status string, tags map[string]string) ([]*aliyunClient.NetworkInterface, error) {
	return m.aliyun.DescribeNetworkInterface(ctx, vpcID, eniID, instanceID, instanceType, status, tags)
}

func (m *Manager) WaitForNetworkInterface(ctx context.Context, eniID string, status string, bo wait.Backoff, ignoreNotExist bool) (*aliyunClient.NetworkInterface, error) {
	alloc := m.allocations.Load(eniID)
	if alloc != nil && alloc.AllocType == AllocPolicyPreferPool {
		return alloc.GetNetworkInterface(), nil
	}

	time.Sleep(bo.Duration)
	realClient, _, err := common.Became(ctx, m.aliyun)
	if err != nil {
		return nil, err
	}

	return realClient.WaitForNetworkInterface(ctx, eniID, status, bo, ignoreNotExist)
}

func (m *Manager) ModifyNetworkInterfaceAttribute(ctx context.Context, eniID string, securityGroupIDs []string) error {
	panic("implement me")
}

func (m *Manager) DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error) {
	panic("implement me")
}

func (m *Manager) AssignPrivateIPAddress(ctx context.Context, eniID string, count int, idempotentKey string) ([]net.IP, error) {
	panic("implement me")
}

func (m *Manager) UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []net.IP) error {
	panic("implement me")
}

func (m *Manager) AssignIpv6Addresses(ctx context.Context, eniID string, count int, idempotentKey string) ([]net.IP, error) {
	panic("implement me")
}

func (m *Manager) UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []net.IP) error {
	//TODO implement me
	panic("implement me")
}

func (m *Manager) cleanUP(ctx context.Context) {
	l := log.FromContext(ctx).WithValues("node", m.cfg.NodeName)

	var deleting []*aliyunClient.NetworkInterface

	idle := 0
	m.allocations.Range(func(key string, value *Allocation) bool {
		l.V(4).Info("eni", "id", value.GetNetworkInterface().NetworkInterfaceID, "type", value.GetNetworkInterface().Type, "status", value.GetStatus())

		switch value.GetStatus() {
		case StatusIdle:
			idle++
		case StatusDeleting:
			deleting = append(deleting, value.GetNetworkInterface())
		}
		return true
	})

	// 1. del removing status
	for _, del := range deleting {
		err := detachNetworkInterface(ctx, m.aliyun, del.NetworkInterfaceID, del.InstanceID, del.TrunkNetworkInterfaceID)
		if err != nil {
			l.Error(err, "detach eni failed")
			continue
		}
		err = m.aliyun.DeleteNetworkInterface(ctx, del.NetworkInterfaceID)
		if err != nil {
			l.Error(err, "delete eni failed")
			continue
		}
		m.allocations.Dispose(del.NetworkInterfaceID)
	}

	// del
	if idle > m.cfg.MaxIdle {
		l.Info("dispose idle", "idle", idle, "maxIdle", m.cfg.MaxIdle)
		for i := 0; i < (idle - m.cfg.MaxIdle); i++ {
			m.allocations.ReleaseIdle()
		}
	}

	v4 := 0
	v6 := 0
	if m.cfg.IPv4Enable {
		v4 = 1
	}
	if m.cfg.IPv6Enable {
		v6 = 1
	}

	if m.cfg.MinIdle <= idle {
		return
	}

	// add
	l.Info("add idle", "idle", idle, "minIdle", m.cfg.MinIdle)
	for i := 0; i < (m.cfg.MinIdle - idle); i++ {
		err := func() error {
			if !m.allocations.RequireQuota() {
				l.Info("add idle reach quota")
				return nil
			}
			defer func() {
				m.allocations.ReleaseQuota()
			}()

			vsw, err := m.vSwitchPool.GetOne(ctx, m.aliyun, m.cfg.ZoneID, m.cfg.VSwitchIDs)
			if err != nil {
				return err
			}

			networkInterface, err := m.aliyun.CreateNetworkInterface(ctx, false, vsw.ID, m.cfg.SecurityGroupIDs, "", v4, v6, m.cfg.ENITags)
			if err != nil {
				return err
			}

			alloc := &Allocation{
				AllocType: AllocPolicyPreferPool,
			}

			defer func() {
				m.allocations.Add(alloc)
			}()

			attachResp, err := attachNetworkInterface(ctx, m.aliyun, networkInterface.NetworkInterfaceID, m.cfg.InstanceID, m.cfg.TrunkENIID)
			if err != nil {
				alloc.Status = StatusDeleting
				return err
			}
			alloc.NetworkInterface = attachResp
			alloc.Status = StatusIdle

			l.Info("add eni to pool", "id", networkInterface.NetworkInterfaceID)
			return nil
		}()
		if err != nil {
			l.Error(err, "failed to add eni")
		}
	}
}

func attachNetworkInterface(ctx context.Context, api register.Interface, eniID, instanceID, trunkENIID string) (*aliyunClient.NetworkInterface, error) {
	err := api.AttachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
	if err != nil {
		return nil, err
	}
	time.Sleep(backoff.Backoff(backoff.WaitENIStatus).Duration)

	return api.WaitForNetworkInterface(ctx, eniID, aliyunClient.ENIStatusInUse, backoff.Backoff(backoff.WaitENIStatus), false)
}

func detachNetworkInterface(ctx context.Context, api register.Interface, eniID, instanceID, trunkENIID string) error {
	err := api.DetachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
	if err != nil {
		return err
	}
	time.Sleep(backoff.Backoff(backoff.WaitENIStatus).Duration)

	_, err = api.WaitForNetworkInterface(ctx, eniID, aliyunClient.ENIStatusAvailable, backoff.Backoff(backoff.WaitENIStatus), true)
	if err == nil || errors.Is(err, apiErr.ErrNotFound) {
		return nil
	}
	return err
}
