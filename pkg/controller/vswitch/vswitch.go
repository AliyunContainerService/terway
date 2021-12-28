/*
Copyright 2021 Terway Authors.

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
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"

	"k8s.io/apimachinery/pkg/util/cache"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Switch hole all switch info from both terway config and podNetworking
type Switch struct {
	ID   string
	Zone string

	AvailableIPCount int64 // for ipv4
	IPv4CIDR         string
	IPv6CIDR         string
}

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
func (s *SwitchPool) GetOne(ctx context.Context, client aliyun.VPCOps, zone string, ids []string, ignoreZone bool) (*Switch, error) {
	var fallBackSwitches []*Switch
	// lookup all vsw in cache and get one matched
	for _, id := range ids {
		vsw, err := s.GetByID(ctx, client, id)
		if err != nil {
			log.FromContext(ctx).Error(err, "get vSwitch", "id", id)
			continue
		}

		if vsw.Zone != zone {
			if ignoreZone {
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

	return nil, fmt.Errorf("no available vSwitch for zone %s", zone)
}

// GetByID will get vSwitch info from local store or openAPI
func (s *SwitchPool) GetByID(ctx context.Context, client aliyun.VPCOps, id string) (*Switch, error) {
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
