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

//go:generate mockery --name Interface --tags default_build

package register

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/controller/status"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types/controlplane"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Interface aliyun client for terway-controlplane
type Interface interface {
	aliyunClient.VPC
	aliyunClient.ECS
	aliyunClient.ENI
	aliyunClient.EFLO
}

type ControllerCtx struct {
	context.Context

	Config       *controlplane.Config
	VSwitchPool  *vswitch.SwitchPool
	AliyunClient Interface

	Wg *wait.Group

	TracerProvider trace.TracerProvider

	RegisterResource []client.Object

	NodeStatusCache *status.Cache[status.NodeStatus]
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
