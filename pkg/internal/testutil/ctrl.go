package testutil

import (
	"context"

	aliyun "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/status"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types/controlplane"
	"go.opentelemetry.io/otel/trace/noop"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	wh "sigs.k8s.io/controller-runtime/pkg/webhook"
)

func NewManager(rest *rest.Config, openAPI aliyun.OpenAPI, directClient client.Client) (manager.Manager, *register.ControllerCtx) {
	vSwitchCtrl, _ := vswitch.NewSwitchPool(100, "10m")
	options := ctrl.Options{
		WebhookServer: wh.NewServer(wh.Options{
			Port: 0,
		}),
		LeaderElectionResourceLock: "leases",
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	}
	mgr, _ := ctrl.NewManager(rest, options)

	return mgr, &register.ControllerCtx{
		Context: context.Background(),
		Config: &controlplane.Config{
			MultiIPController: controlplane.MultiIPController{
				MultiIPNodeSyncPeriod:         "2m",
				MultiIPGCPeriod:               "2m",
				MultiIPMinSyncPeriodOnFailure: "2m",
				MultiIPMaxSyncPeriodOnFailure: "2m",
			},
			NodeController: controlplane.NodeController{},
			EnableTrunk:    ptr.To(true),
		},
		VSwitchPool:     vSwitchCtrl,
		AliyunClient:    openAPI,
		Wg:              &wait.Group{},
		TracerProvider:  noop.NewTracerProvider(),
		NodeStatusCache: status.NewCache[status.NodeStatus](),
		DirectClient:    directClient,
	}
}
