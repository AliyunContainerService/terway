package eni

import (
	"context"
	"net"
	"net/netip"
	"sync"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ NetworkInterface = &CRDV2{}

type CRDV2 struct {
	mgr ctrl.Manager

	client client.Client

	nodeName string
}

func NewCRDV2(nodeName string) *CRDV2 {
	restConfig := ctrl.GetConfigOrDie()

	options := ctrl.Options{
		Scheme:                 types.Scheme,
		HealthProbeBindAddress: "0",
		MetricsBindAddress:     "0",
		WebhookServer:          nil,
		Cache: cache.Options{
			HTTPClient:           nil,
			Scheme:               nil,
			Mapper:               nil,
			SyncPeriod:           nil,
			Namespaces:           nil,
			DefaultLabelSelector: nil,
			DefaultFieldSelector: nil,
			DefaultTransform:     nil,
			ByObject: map[client.Object]cache.ByObject{
				&networkv1beta1.Node{}: {
					Label: labels.Set(map[string]string{
						"name": nodeName,
					}).AsSelector(),
					Field:     nil,
					Transform: nil,
				},
			},
			UnsafeDisableDeepCopy: nil,
		},
	}

	mgr, err := ctrl.NewManager(restConfig, options)
	if err != nil {
		panic(err)
	}
	if err = (&nodeReconcile{
		nodeName: nodeName,
		client:   mgr.GetClient(),
		record:   mgr.GetEventRecorderFor("terway-daemon"),
	}).SetupWithManager(mgr); err != nil {
		panic(err)
	}

	return &CRDV2{
		mgr:      mgr,
		client:   mgr.GetClient(),
		nodeName: nodeName,
	}
}

func (r *CRDV2) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	klog.Info("start CRDV2 controller")

	wg.Add(1)
	go func() {
		defer wg.Done()

		err := r.mgr.Start(ctx)
		if err != nil {
			if ctx.Err() == nil {
				klog.Fatalf("manager failed: %v", err)
			}
		}
	}()

	return nil
}

func (r *CRDV2) Priority() int {
	return 100
}

func (r *CRDV2) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	if request.ResourceType() != ResourceTypeRDMA && request.ResourceType() != ResourceTypeLocalIP {
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}

	resp := make(chan *AllocResp)

	go func() {
		l := logf.FromContext(ctx)

		node := &networkv1beta1.Node{}
		allocResp := &AllocResp{}

		var err error
		err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitPodENIStatus), func(ctx context.Context) (bool, error) {
			err = r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, node)
			if err != nil {
				l.Error(err, "get node failed")
				return false, nil
			}
			// cni.PodName
			var ipv4, ipv6 netip.Addr
			var eniInfo *networkv1beta1.NetworkInterface
			for _, eni := range node.Status.NetworkInterfaces {
				if eni.Status != aliyunClient.ENIStatusInUse {
					continue
				}
				for _, ip := range eni.IPv4 {
					if ip.Status != networkv1beta1.IPStatusValid ||
						ip.PodID != cni.PodID {
						continue
					}
					addr, err := netip.ParseAddr(ip.IP)
					if err != nil {
						return false, err
					}
					ipv4 = addr
					eniInfo = eni
				}
				for _, ip := range eni.IPv6 {
					if ip.Status != networkv1beta1.IPStatusValid ||
						ip.PodID != cni.PodID {
						continue
					}
					addr, err := netip.ParseAddr(ip.IP)
					if err != nil {
						return false, err
					}
					ipv6 = addr
					eniInfo = eni
				}
			}
			if (!ipv4.IsValid() && !ipv6.IsValid()) || eniInfo == nil {
				return false, nil
			}

			var ip types.IPSet2

			ip.IPv4 = ipv4
			ip.IPv6 = ipv6
			gw := types.IPSet{}
			vsw := types.IPNetSet{}
			if ipv4.IsValid() {
				gw.IPv4 = net.ParseIP(terwayIP.DeriveGatewayIP(eniInfo.IPv4CIDR))

				_, cidr, err := net.ParseCIDR(eniInfo.IPv4CIDR)
				if err != nil {
					return false, err
				}
				vsw.IPv4 = cidr
			}
			if ipv6.IsValid() {
				gw.IPv6 = net.ParseIP(terwayIP.DeriveGatewayIP(eniInfo.IPv6CIDR))

				_, cidr, err := net.ParseCIDR(eniInfo.IPv6CIDR)
				if err != nil {
					return false, err
				}
				vsw.IPv6 = cidr
			}

			allocResp.NetworkConfigs = append(allocResp.NetworkConfigs, &LocalIPResource{
				ENI: daemon.ENI{
					ID:               eniInfo.ID,
					MAC:              eniInfo.MacAddress,
					SecurityGroupIDs: eniInfo.SecurityGroupIDs,
					Trunk:            false,
					ERdma:            eniInfo.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeHighPerformance,
					GatewayIP:        gw,
					VSwitchCIDR:      vsw,
					VSwitchID:        eniInfo.VSwitchID,
				},
				IP: ip,
			})

			return true, nil
		})

		select {
		case <-ctx.Done():
		case resp <- allocResp:
		}
	}()

	return resp, nil
}

func (r *CRDV2) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) bool {
	return false
}

func (r *CRDV2) Dispose(n int) int {
	return 0
}
