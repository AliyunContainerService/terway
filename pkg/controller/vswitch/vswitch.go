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
	"errors"
	"flag"
	"fmt"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("switch")

var (
	vSwitchSyncPeriod string
)

func init() {
	flag.StringVar(&vSwitchSyncPeriod, "vswitch-sync-period", "20m", "The period sync with openAPI.Default 20m.")
}

// Switch hole all switch info from both terway config and podNetworking
type Switch struct {
	ID   string
	Zone string

	AvailableIPCount int64 // for ipv4
}

// SwitchPool contain all vSwitches
type SwitchPool struct {
	switches sync.Map

	aliyun *aliyun.OpenAPI

	vSwitchSyncPeriod time.Duration
}

// NewSwitchPool create pool and set vSwitches to pool
func NewSwitchPool(aliyun *aliyun.OpenAPI) (*SwitchPool, error) {
	period, err := time.ParseDuration(vSwitchSyncPeriod)
	if err != nil {
		return nil, err
	}
	sw := &SwitchPool{aliyun: aliyun, vSwitchSyncPeriod: period}
	return sw, sw.SyncSwitch()
}

// Start the controller
func (s *SwitchPool) Start(ctx context.Context) error {
	wait.Until(func() {
		err := s.SyncSwitch()
		if err != nil {
			log.Error(err, "error sync all vSwitch")
		}
	}, s.vSwitchSyncPeriod, ctx.Done())
	return fmt.Errorf("vSwitch sync loop end")
}

// NeedLeaderElection need election
func (s *SwitchPool) NeedLeaderElection() bool {
	return true
}

// SyncSwitch will sync all cached vSwitch info with openAPI
func (s *SwitchPool) SyncSwitch() error {
	ids := []string{}

	s.switches.Range(func(key, value interface{}) bool {
		ids = append(ids, key.(string))
		return true
	})

	for _, id := range ids {
		resp, err := s.aliyun.DescribeVSwitchByID(context.Background(), id)
		if err != nil {
			if errors.Is(err, apiErr.ErrNotFound) {
				log.Info("vSwitch deleted", "ID", resp.VSwitchId)
				s.switches.Delete(id)
				continue
			}
			return fmt.Errorf("error sync vSwitch %s, %w", id, err)
		}

		s.switches.Store(resp.VSwitchId, &Switch{
			ID:               resp.VSwitchId,
			Zone:             resp.ZoneId,
			AvailableIPCount: resp.AvailableIpAddressCount,
		})
		log.Info("sync vSwitch", "ID", resp.VSwitchId, "Zone", resp.ZoneId, "IPCount", resp.AvailableIpAddressCount)
	}

	return nil
}

// GetOne get one vSwitch by zone, if ids is set will limit vSwitch in this ids
func (s *SwitchPool) GetOne(zone string, ids sets.String) (string, error) {
	id := ""
	s.switches.Range(func(key, value interface{}) bool {
		vsw := value.(*Switch)
		if zone != "" {
			if vsw.Zone != zone {
				return true
			}
		}
		if vsw.AvailableIPCount == 0 {
			return true
		}

		if ids.Len() > 0 {
			if _, ok := ids[vsw.ID]; !ok {
				return true
			}
		}
		id = vsw.ID

		return false
	})
	if id == "" {
		return "", fmt.Errorf("no available vswitch")
	}
	return id, nil
}

// GetByID will get vSwitch info from local store
func (s *SwitchPool) GetByID(id string) (*Switch, error) {
	v, ok := s.switches.Load(id)
	if !ok {
		resp, err := s.aliyun.DescribeVSwitchByID(context.Background(), id)
		if err != nil {
			return nil, fmt.Errorf("error get vSwitch %s, %w", id, err)
		}
		sw := &Switch{
			ID:               resp.VSwitchId,
			Zone:             resp.ZoneId,
			AvailableIPCount: resp.AvailableIpAddressCount,
		}
		s.switches.Store(resp.VSwitchId, sw)
		return sw, nil
	}
	sw := v.(*Switch)
	return &Switch{
		ID:               sw.ID,
		Zone:             sw.Zone,
		AvailableIPCount: sw.AvailableIPCount,
	}, nil
}
