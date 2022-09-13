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
	"sync"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
)

var ErrMaxENI = errors.New("max eni executed")

type Status string

const (
	StatusIdle      Status = "idle"
	StatusInUse     Status = "inUse"
	StatusDeleting  Status = "deleting"
	StatusUnManaged Status = "unManaged"
)

type allocPolicyContextKey struct{}
type AllocPolicy int

const (
	AllocPolicyDirect AllocPolicy = iota
	AllocPolicyPreferPool
)

func AllocPolicyCtx(ctx context.Context) AllocPolicy {
	v, ok := ctx.Value(allocPolicyContextKey{}).(AllocPolicy)
	if ok {
		return v
	}
	return AllocPolicyDirect
}

func AllocTypeWithCtx(ctx context.Context, allocType AllocPolicy) context.Context {
	return context.WithValue(ctx, allocPolicyContextKey{}, allocType)
}

// Allocation store eni alloc status
type Allocation struct {
	sync.RWMutex
	NetworkInterface *client.NetworkInterface // find a way to update the fields
	Status           Status
	AllocType        AllocPolicy
}

func (a *Allocation) GetStatus() Status {
	a.RLock()
	defer a.RUnlock()
	return a.Status
}

func (a *Allocation) SetStatus(status Status) {
	a.Lock()
	defer a.Unlock()
	a.Status = status
}

func (a *Allocation) GetNetworkInterface() *client.NetworkInterface {
	a.RLock()
	defer a.RUnlock()
	return a.NetworkInterface
}

type AllocManager struct {
	sync.Mutex
	allocation map[string]*Allocation // index by resource id

	max     int
	pending int

	enableTrunk bool
}

func NewAllocManager(max int, enableTrunk bool) *AllocManager {
	return &AllocManager{allocation: make(map[string]*Allocation, max), max: max, enableTrunk: enableTrunk}
}

// Alloc try alloc eni by vSwitchID
func (a *AllocManager) Alloc(vSwitchID string) *client.NetworkInterface {
	a.Lock()
	defer a.Unlock()

	for _, alloc := range a.allocation {
		if alloc.GetStatus() != StatusIdle || alloc.GetNetworkInterface().VSwitchID != vSwitchID {
			continue
		}
		if a.enableTrunk && alloc.GetNetworkInterface().Type != "Member" {
			continue
		}
		if !a.enableTrunk && alloc.GetNetworkInterface().Type != "Secondary" {
			continue
		}

		chosen := alloc.GetNetworkInterface()
		alloc.SetStatus(StatusInUse)
		return chosen
	}
	return nil
}

// Release set status to StatusIdle
func (a *AllocManager) Release(eniID string) {
	a.Lock()
	defer a.Unlock()

	alloc, ok := a.allocation[eniID]
	if !ok {
		return
	}
	switch alloc.GetStatus() {
	case StatusUnManaged, StatusDeleting:
		return
	}
	alloc.SetStatus(StatusIdle)
}

// ReleaseIdle pick one idle and set status to StatusDeleting
func (a *AllocManager) ReleaseIdle() *client.NetworkInterface {
	a.Lock()
	defer a.Unlock()

	var chosen *client.NetworkInterface

	for _, alloc := range a.allocation {
		if alloc.GetStatus() != StatusIdle {
			continue
		}
		chosen = alloc.GetNetworkInterface()
		alloc.SetStatus(StatusDeleting)
	}
	return chosen
}

func (a *AllocManager) Add(alloc *Allocation) {
	a.Lock()
	defer a.Unlock()

	a.allocation[alloc.GetNetworkInterface().NetworkInterfaceID] = alloc
}

func (a *AllocManager) Dispose(eniID string) {
	a.Lock()
	defer a.Unlock()

	delete(a.allocation, eniID)
}

func (a *AllocManager) Range(f func(key string, value *Allocation) bool) {
	a.Lock()
	defer a.Unlock()

	for key, value := range a.allocation {
		if !f(key, value) {
			return
		}
	}
}

func (a *AllocManager) Load(eniID string) *Allocation {
	a.Lock()
	defer a.Unlock()

	return a.allocation[eniID]
}

func (a *AllocManager) RequireQuota() bool {
	a.Lock()
	defer a.Unlock()

	if len(a.allocation)+a.pending >= a.max {
		return false
	}
	a.pending++
	return true
}

func (a *AllocManager) ReleaseQuota() {
	a.Lock()
	defer a.Unlock()

	a.pending--
	if a.pending < 0 {
		a.pending = 0
	}
}
