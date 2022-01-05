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

package vlan

import (
	"fmt"

	"github.com/AliyunContainerService/terway/plugin/driver/utils"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type Vlan struct {
	Master string
	IfName string
	Vid    int
	MTU    int
}

func Setup(cfg *Vlan, netNS ns.NetNS) error {
	master, err := netlink.LinkByName(cfg.Master)
	if err != nil {
		return fmt.Errorf("cannot found master link by name %s", master)
	}
	peerName := fmt.Sprintf("%s.%d", master.Attrs().Name, cfg.Vid)
	if len(peerName) > 15 {
		peerName = peerName[len(peerName)-15:]
	}
	peer, err := netlink.LinkByName(peerName)
	if err == nil {
		// del pre link
		err = utils.LinkDel(peer)
		if err != nil {
			return err
		}
	}

	if _, ok := err.(netlink.LinkNotFoundError); !ok {
		return err
	}

	v := &netlink.Vlan{
		LinkAttrs: netlink.LinkAttrs{
			MTU:         cfg.MTU,
			Name:        peerName,
			ParentIndex: master.Attrs().Index,
			Namespace:   netlink.NsFd(int(netNS.Fd())),
		},
		VlanId: cfg.Vid,
	}
	err = utils.LinkAdd(v)
	if err != nil {
		return err
	}

	return netNS.Do(func(netNS ns.NetNS) error {
		contLink, innerErr := netlink.LinkByName(peerName)
		if innerErr != nil {
			return innerErr
		}
		_, innerErr = utils.EnsureLinkName(contLink, cfg.IfName)
		return innerErr
	})
}
