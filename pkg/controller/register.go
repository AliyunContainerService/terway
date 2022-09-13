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

package register

import (
	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/types/controlplane"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Interface aliyun client for terway-controlplane
type Interface interface {
	client.VSwitch
	client.ENI
	client.ECS
}

type ControllerCtx struct {
	Config         *controlplane.Config
	VSwitchPool    *vswitch.SwitchPool
	AliyunClient   Interface
	DelegateClient Interface
}

type Creator func(mgr manager.Manager, ctrlCtx *ControllerCtx) error

// Controllers collect for all controller
var Controllers = map[string]struct {
	Creator Creator
	Enable  bool
}{}

// Add add controller by name
func Add(name string, creator Creator, enable bool) {
	Controllers[name] = struct {
		Creator Creator
		Enable  bool
	}{
		Creator: creator,
		Enable:  enable,
	}
}
