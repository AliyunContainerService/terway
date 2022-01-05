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

package nic

import (
	"fmt"

	terwaySysctl "github.com/AliyunContainerService/terway/pkg/sysctl"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"

	"github.com/vishvananda/netlink"
)

// Conf for the link
type Conf struct {
	IfName string // set link name
	MTU    int    // set link MTU

	Addrs  []*netlink.Addr
	Routes []*netlink.Route
	Rules  []*netlink.Rule
	Neighs []*netlink.Neigh

	SysCtl map[string][]string

	StripVlan bool
}

func Setup(link netlink.Link, conf *Conf) error {
	var err error
	if conf.IfName != "" {
		changed, err := utils.EnsureLinkName(link, conf.IfName)
		if err != nil {
			return err
		}
		if changed {
			link, err = netlink.LinkByIndex(link.Attrs().Index)
			if err != nil {
				return err
			}
		}
	}

	if conf.MTU > 0 {
		_, err = utils.EnsureLinkMTU(link, conf.MTU)
		if err != nil {
			return err
		}
	}

	for _, v := range conf.SysCtl {
		if len(v) != 2 {
			return fmt.Errorf("sysctl config err")
		}
		err = terwaySysctl.EnsureConf(v[0], v[1])
		if err != nil {
			return err
		}
	}

	for _, addr := range conf.Addrs {
		_, err = utils.EnsureAddr(link, addr)
		if err != nil {
			return err
		}
	}

	_, err = utils.EnsureLinkUp(link)
	if err != nil {
		return err
	}

	for _, neigh := range conf.Neighs {
		_, err = utils.EnsureNeigh(neigh)
		if err != nil {
			return err
		}
	}

	for _, route := range conf.Routes {
		_, err = utils.EnsureRoute(route)
		if err != nil {
			return err
		}
	}

	for _, rule := range conf.Rules {
		_, err = utils.EnsureIPRule(rule)
		if err != nil {
			return err
		}
	}

	if conf.StripVlan {
		return utils.EnsureVlanUntagger(link)
	}
	return nil
}
