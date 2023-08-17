/*
Copyright 2021-2022 Terway Authors.

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

package vswitch

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/util/cache"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
)

// Switch hole all switch info from both terway config and podNetworking
type Switch struct {
	ID   string
	Zone string

	AvailableIPCount int64 // for ipv4
	IPv4CIDR         string
	IPv6CIDR         string
}

type ByAvailableIP []Switch

func (a ByAvailableIP) Len() int           { return len(a) }
func (a ByAvailableIP) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByAvailableIP) Less(i, j int) bool { return a[i].AvailableIPCount > a[j].AvailableIPCount }

// SwitchPool contain all vSwitches
type SwitchPool struct {
	cache *cache.LRUExpireCache
	ttl   time.Duration
}

// NewSwitchPool create pool and set vSwitches to pool
func NewSwitchPool(size int, ttl string) (*SwitchPool, error) {
	t, err := time.ParseDuration(ttl)
	if err != nil {
		return nil, err
	}

	return &SwitchPool{cache: cache.NewLRUExpireCache(size), ttl: t}, nil
}

// GetOne get one vSwitch by zone and limit in ids
func (s *SwitchPool) GetOne(ctx context.Context, client client.VSwitch, zone string, ids []string, opts ...SelectOption) (*Switch, error) {
	var fallBackSwitches []*Switch

	selectOptions := &SelectOptions{}
	selectOptions.ApplyOptions(opts)

	switch selectOptions.VSwitchSelectPolicy {
	case VSwitchSelectionPolicyRandom:
		rand.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	case VSwitchSelectionPolicyMost:
		// lookup all vsw in cache and get one matched
		// try sort the vsw

		var byAvailableIP ByAvailableIP
		for _, id := range ids {
			vsw, err := s.GetByID(ctx, client, id)
			if err != nil {
				log.FromContext(ctx).Error(err, "get vSwitch", "id", id)
				continue
			}

			if vsw.Zone != zone {
				continue
			}
			if vsw.AvailableIPCount == 0 {
				continue
			}
			byAvailableIP = append(byAvailableIP, *vsw)
		}

		sort.Sort(byAvailableIP)
		// keep the below logic untouched
		newOrder := make([]string, 0, len(byAvailableIP))
		for _, vsw := range byAvailableIP {
			newOrder = append(newOrder, vsw.ID)
		}
		ids = newOrder
	}

	// lookup all vsw in cache and get one matched
	for _, id := range ids {
		vsw, err := s.GetByID(ctx, client, id)
		if err != nil {
			log.FromContext(ctx).Error(err, "get vSwitch", "id", id)
			continue
		}

		if vsw.Zone != zone {
			if selectOptions.IgnoreZone {
				fallBackSwitches = append(fallBackSwitches, vsw)
			}
			continue
		}
		if vsw.AvailableIPCount == 0 {
			continue
		}
		return vsw, nil
	}

	for _, vsw := range fallBackSwitches {
		if vsw.AvailableIPCount == 0 {
			continue
		}
		return vsw, nil
	}

	return nil, fmt.Errorf("no available vSwitch for zone %s, vswList %v", zone, ids)
}

// GetByID will get vSwitch info from local store or openAPI
func (s *SwitchPool) GetByID(ctx context.Context, client client.VSwitch, id string) (*Switch, error) {
	v, ok := s.cache.Get(id)
	if !ok {
		resp, err := client.DescribeVSwitchByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("error get vSwitch %s, %w", id, err)
		}
		sw := &Switch{
			ID:               resp.VSwitchId,
			Zone:             resp.ZoneId,
			AvailableIPCount: resp.AvailableIpAddressCount,
			IPv4CIDR:         resp.CidrBlock,
			IPv6CIDR:         resp.Ipv6CidrBlock,
		}
		s.cache.Add(resp.VSwitchId, sw, s.ttl)
		return sw, nil
	}
	sw := v.(*Switch)
	return sw, nil
}

func (s *SwitchPool) Block(id string) {
	v, ok := s.cache.Get(id)
	if !ok {
		return
	}
	vsw := *(v.(*Switch))
	vsw.AvailableIPCount = 0
	s.cache.Add(id, &vsw, s.ttl)
}

// Add Switch to cache. Test purpose.
func (s *SwitchPool) Add(sw *Switch) {
	s.cache.Add(sw.ID, sw, s.ttl)
}

// Del Switch from cache. Test purpose.
func (s *SwitchPool) Del(key string) {
	s.cache.Remove(key)
}

type SelectionPolicy string

// VSwitch Selection Policy
const (
	VSwitchSelectionPolicyOrdered SelectionPolicy = "ordered"
	VSwitchSelectionPolicyRandom  SelectionPolicy = "random"
	VSwitchSelectionPolicyMost    SelectionPolicy = "most"
)

type SelectOption interface {
	// Apply applies this configuration to the given select options.
	Apply(*SelectOptions)
}

// SelectOptions contains options for requests.
type SelectOptions struct {
	IgnoreZone bool

	VSwitchSelectPolicy SelectionPolicy
}

// ApplyOptions applies the given select options on these options
func (o *SelectOptions) ApplyOptions(opts []SelectOption) *SelectOptions {
	for _, opt := range opts {
		opt.Apply(o)
	}
	return o
}

var _ SelectOption = &SelectOptions{}

// Apply implements SelectOption.
func (o *SelectOptions) Apply(so *SelectOptions) {
	so.IgnoreZone = o.IgnoreZone
	if o.VSwitchSelectPolicy != "" {
		so.VSwitchSelectPolicy = o.VSwitchSelectPolicy
	}
}
