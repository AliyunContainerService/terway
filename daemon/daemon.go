package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/AliyunContainerService/terway/pkg/apis/alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/apis/crds"
	podENITypes "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	vswpool "github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/flowcontrol"
)

const (
	daemonModeVPC        = "VPC"
	daemonModeENIMultiIP = "ENIMultiIP"
	daemonModeENIOnly    = "ENIOnly"

	gcPeriod        = 5 * time.Minute
	poolCheckPeriod = 10 * time.Minute

	conditionFalse = "false"
	conditionTrue  = "true"

	networkServiceName       = "default"
	tracingKeyName           = "name"
	tracingKeyDaemonMode     = "daemon_mode"
	tracingKeyConfigFilePath = "config_file_path"

	tracingKeyPendingPodsCount = "pending_pods_count"

	commandMapping = "mapping"

	IfEth0 = "eth0"
)

type networkService struct {
	daemonMode     string
	configFilePath string

	k8s         Kubernetes
	resourceDB  storage.Storage
	vethResMgr  ResourceManager
	eniResMgr   ResourceManager
	eniIPResMgr ResourceManager
	eipResMgr   ResourceManager
	//networkResourceMgr ResourceManager
	mgrForResource map[string]ResourceManager
	pendingPods    sync.Map
	sync.RWMutex

	enableTrunk bool

	ipFamily     *types.IPFamily
	ipamType     types.IPAMType
	eniCapPolicy types.ENICapPolicy

	rpc.UnimplementedTerwayBackendServer
}

var serviceLog = logger.DefaultLogger.WithField("subSys", "network-service")

var _ rpc.TerwayBackendServer = (*networkService)(nil)

func (n *networkService) getResourceManagerForRes(resType string) ResourceManager {
	return n.mgrForResource[resType]
}

// return resource relation in db, or return nil.
func (n *networkService) getPodResource(info *types.PodInfo) (types.PodResources, error) {
	obj, err := n.resourceDB.Get(podInfoKey(info.Namespace, info.Name))
	if err == nil {
		return obj.(types.PodResources), nil
	}
	if err == storage.ErrNotFound {
		return types.PodResources{}, nil
	}

	return types.PodResources{}, err
}

func (n *networkService) deletePodResource(info *types.PodInfo) error {
	key := podInfoKey(info.Namespace, info.Name)
	return n.resourceDB.Delete(key)
}

func (n *networkService) allocateVeth(ctx *networkContext, old *types.PodResources) (*types.Veth, error) {
	oldVethRes := old.GetResourceItemByType(types.ResourceTypeVeth)
	oldVethID := ""
	if old.PodInfo != nil {
		if len(oldVethRes) == 0 {
			ctx.Log().Debugf("veth for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldVethRes) > 1 {
			ctx.Log().Warnf("veth for pod %s is more than one", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldVethID = oldVethRes[0].ID
		}
	}

	res, err := n.vethResMgr.Allocate(ctx, oldVethID)
	if err != nil {
		return nil, err
	}
	return res.(*types.Veth), nil
}

func (n *networkService) allocateENI(ctx *networkContext, old *types.PodResources) (*types.ENI, error) {
	oldENIRes := old.GetResourceItemByType(types.ResourceTypeENI)
	oldENIID := ""
	if old.PodInfo != nil {
		if len(oldENIRes) == 0 {
			ctx.Log().Debugf("eniip for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldENIRes) > 1 {
			ctx.Log().Warnf("eniip for pod %s is more than one", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldENIID = oldENIRes[0].ID
		}
	}

	res, err := n.eniResMgr.Allocate(ctx, oldENIID)
	if err != nil {
		return nil, err
	}
	return res.(*types.ENI), nil
}

func (n *networkService) allocateENIMultiIP(ctx *networkContext, old *types.PodResources) (*types.ENIIP, error) {
	oldENIIPRes := old.GetResourceItemByType(types.ResourceTypeENIIP)
	oldENIIPID := ""
	if old.PodInfo != nil {
		if len(oldENIIPRes) == 0 {
			ctx.Log().Debugf("eniip for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldENIIPRes) > 1 {
			ctx.Log().Warnf("eniip for pod %s is more than one", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldENIIPID = oldENIIPRes[0].ID
		}
	}

	res, err := n.eniIPResMgr.Allocate(ctx, oldENIIPID)
	if err != nil {
		return nil, err
	}
	return res.(*types.ENIIP), nil
}

func (n *networkService) allocateEIP(ctx *networkContext, old *types.PodResources) (*types.EIP, error) {
	oldEIPRes := old.GetResourceItemByType(types.ResourceTypeEIP)
	oldEIPID := ""
	if old.PodInfo != nil {
		if len(oldEIPRes) == 0 {
			ctx.Log().Debugf("eip for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldEIPRes) > 1 {
			ctx.Log().Warnf("eip for pod %s is more than one", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldEIPID = oldEIPRes[0].ID
		}
	}

	res, err := n.eipResMgr.Allocate(ctx, oldEIPID)
	if err != nil {
		return nil, err
	}
	return res.(*types.EIP), nil
}

func (n *networkService) AllocIP(ctx context.Context, r *rpc.AllocIPRequest) (*rpc.AllocIPReply, error) {
	serviceLog.WithFields(map[string]interface{}{
		"pod":         podInfoKey(r.K8SPodNamespace, r.K8SPodName),
		"containerID": r.K8SPodInfraContainerId,
		"netNS":       r.Netns,
		"ifName":      r.IfName,
	}).Info("alloc ip req")

	_, exist := n.pendingPods.LoadOrStore(podInfoKey(r.K8SPodNamespace, r.K8SPodName), struct{}{})
	if exist {
		return nil, fmt.Errorf("pod %s resource processing", podInfoKey(r.K8SPodNamespace, r.K8SPodName))
	}
	defer func() {
		n.pendingPods.Delete(podInfoKey(r.K8SPodNamespace, r.K8SPodName))
	}()

	n.RLock()
	defer n.RUnlock()
	var (
		start = time.Now()
		err   error
	)
	defer func() {
		metric.RPCLatency.WithLabelValues("AllocIP", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	}()

	// 0. Get pod Info
	podinfo, err := n.k8s.GetPod(r.K8SPodNamespace, r.K8SPodName, true)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pod info for: %+v", r)
	}

	// 1. Init Context
	networkContext := &networkContext{
		Context:    ctx,
		resources:  []types.ResourceItem{},
		pod:        podinfo,
		k8sService: n.k8s,
	}
	allocIPReply := &rpc.AllocIPReply{IPv4: n.ipFamily.IPv4, IPv6: n.ipFamily.IPv6}

	defer func() {
		// roll back allocated resource when error
		if err != nil {
			networkContext.Log().Errorf("alloc result with error, %+v", err)
			for _, res := range networkContext.resources {
				err = n.deletePodResource(podinfo)
				networkContext.Log().Errorf("rollback res[%v] with error, %+v", res, err)
				mgr := n.getResourceManagerForRes(res.Type)
				if mgr == nil {
					networkContext.Log().Warnf("error cleanup allocated network resource %s, %s: %v", res.ID, res.Type, err)
					continue
				}
				err = mgr.Release(networkContext, res)
				if err != nil {
					networkContext.Log().Infof("rollback res error: %+v", err)
				}
			}
		} else {
			networkContext.Log().Infof("alloc result: %+v", allocIPReply)

			for _, netConfig := range allocIPReply.NetConfs {
				if netConfig.IfName != IfEth0 && netConfig.IfName != "" {
					continue
				}
				if netConfig.BasicInfo == nil || netConfig.BasicInfo.PodIP == nil {
					continue
				}
				var ips []string
				if netConfig.BasicInfo.PodIP.IPv4 != "" {
					ips = append(ips, netConfig.BasicInfo.PodIP.IPv4)
				}
				if netConfig.BasicInfo.PodIP.IPv6 != "" {
					ips = append(ips, netConfig.BasicInfo.PodIP.IPv6)
				}
				_ = n.k8s.PatchPodIPInfo(podinfo, strings.Join(ips, ","))
			}
		}
	}()

	// 2. Find old resource info
	oldRes, err := n.getPodResource(podinfo)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pod resources from db for pod %+v", podinfo)
	}

	if !n.verifyPodNetworkType(podinfo.PodNetworkType) {
		return nil, fmt.Errorf("unexpect pod network type allocate, maybe daemon mode changed: %+v", podinfo.PodNetworkType)
	}
	var netConf []*rpc.NetConf
	// 3. Allocate network resource for pod
	switch podinfo.PodNetworkType {
	case podNetworkTypeENIMultiIP:
		allocIPReply.IPType = rpc.IPType_TypeENIMultiIP
		var netConfs []*rpc.NetConf
		netConfs, err = n.multiIPFromCRD(podinfo, true)
		if err != nil {
			return nil, err
		}
		netConf = append(netConf, netConfs...)

		defaultIfSet := false
		for _, cfg := range netConf {
			if defaultIf(cfg.IfName) {
				defaultIfSet = true
			}
		}
		if !defaultIfSet {
			// alloc eniip
			var eniIP *types.ENIIP
			eniIP, err = n.allocateENIMultiIP(networkContext, &oldRes)
			if err != nil {
				return nil, fmt.Errorf("error get allocated eniip ip for: %+v, result: %+v", podinfo, err)
			}
			newRes := types.PodResources{
				PodInfo:   podinfo,
				Resources: eniIP.ToResItems(),
				NetNs: func(s string) *string {
					return &s
				}(r.Netns),
				ContainerID: func(s string) *string {
					return &s
				}(r.K8SPodInfraContainerId),
			}
			networkContext.resources = append(networkContext.resources, newRes.Resources...)
			if n.eipResMgr != nil && podinfo.EipInfo.PodEip {
				podinfo.PodIPs = eniIP.IPSet
				var eipRes *types.EIP
				eipRes, err = n.allocateEIP(networkContext, &oldRes)
				if err != nil {
					return nil, fmt.Errorf("error get allocated eip for: %+v, result: %+v", podinfo, err)
				}
				eipResItem := eipRes.ToResItems()
				newRes.Resources = append(newRes.Resources, eipResItem...)
				networkContext.resources = append(networkContext.resources, eipResItem...)
			}
			err = n.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
			if err != nil {
				return nil, errors.Wrapf(err, "error put resource into store")
			}

			netConf = append(netConf, &rpc.NetConf{
				BasicInfo: &rpc.BasicInfo{
					PodIP:       eniIP.IPSet.ToRPC(),
					PodCIDR:     eniIP.ENI.VSwitchCIDR.ToRPC(),
					GatewayIP:   eniIP.ENI.GatewayIP.ToRPC(),
					ServiceCIDR: n.k8s.GetServiceCIDR().ToRPC(),
				},
				ENIInfo: &rpc.ENIInfo{
					MAC:   eniIP.ENI.MAC,
					Trunk: false,
				},
				Pod: &rpc.Pod{
					Ingress:         podinfo.TcIngress,
					Egress:          podinfo.TcEgress,
					NetworkPriority: podinfo.NetworkPriority,
				},
				IfName:       "",
				ExtraRoutes:  nil,
				DefaultRoute: true,
			})
		}

		err = defaultForNetConf(netConf)
		if err != nil {
			return nil, err
		}
		allocIPReply.Success = true
	case podNetworkTypeVPCENI:
		allocIPReply.IPType = rpc.IPType_TypeVPCENI
		if n.ipamType == types.IPAMTypeCRD {
			var netConfs []*rpc.NetConf
			netConfs, err = n.exclusiveENIFromCRD(podinfo, true)
			if err != nil {
				return nil, err
			}
			netConf = append(netConf, netConfs...)
		} else {
			var eni *types.ENI
			eni, err = n.allocateENI(networkContext, &oldRes)
			if err != nil {
				return nil, fmt.Errorf("error get allocated vpc ENI ip for: %+v, result: %+v", podinfo, err)
			}
			newRes := types.PodResources{
				PodInfo:   podinfo,
				Resources: eni.ToResItems(),
				NetNs: func(s string) *string {
					return &s
				}(r.Netns),
				ContainerID: func(s string) *string {
					return &s
				}(r.K8SPodInfraContainerId),
			}
			networkContext.resources = append(networkContext.resources, newRes.Resources...)
			if n.eipResMgr != nil && podinfo.EipInfo.PodEip {
				podinfo.PodIPs = eni.PrimaryIP
				var eipRes *types.EIP
				eipRes, err = n.allocateEIP(networkContext, &oldRes)
				if err != nil {
					return nil, fmt.Errorf("error get allocated eip for: %+v, result: %+v", podinfo, err)
				}
				eipResItem := eipRes.ToResItems()
				newRes.Resources = append(newRes.Resources, eipResItem...)
				networkContext.resources = append(networkContext.resources, eipResItem...)
			}
			err = n.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
			if err != nil {
				return nil, errors.Wrapf(err, "error put resource into store")
			}
			netConf = append(netConf, &rpc.NetConf{
				BasicInfo: &rpc.BasicInfo{
					PodIP:       eni.PrimaryIP.ToRPC(),
					PodCIDR:     eni.VSwitchCIDR.ToRPC(),
					GatewayIP:   eni.GatewayIP.ToRPC(),
					ServiceCIDR: n.k8s.GetServiceCIDR().ToRPC(),
				},
				ENIInfo: &rpc.ENIInfo{
					MAC:   eni.MAC,
					Trunk: false,
				},
				Pod: &rpc.Pod{
					Ingress:         podinfo.TcIngress,
					Egress:          podinfo.TcEgress,
					NetworkPriority: podinfo.NetworkPriority,
				},
				IfName:       "",
				ExtraRoutes:  nil,
				DefaultRoute: true,
			})
		}
		allocIPReply.Success = true
	case podNetworkTypeVPCIP:
		allocIPReply.IPType = rpc.IPType_TypeVPCIP
		var vpcVeth *types.Veth
		vpcVeth, err = n.allocateVeth(networkContext, &oldRes)
		if err != nil {
			return nil, fmt.Errorf("error get allocated vpc ip for: %+v, result: %+v", podinfo, err)
		}
		newRes := types.PodResources{
			PodInfo:   podinfo,
			Resources: vpcVeth.ToResItems(),
			NetNs: func(s string) *string {
				return &s
			}(r.Netns),
			ContainerID: func(s string) *string {
				return &s
			}(r.K8SPodInfraContainerId),
		}
		networkContext.resources = append(networkContext.resources, newRes.Resources...)
		err = n.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
		if err != nil {
			return nil, errors.Wrapf(err, "error put resource into store")
		}
		netConf = append(netConf, &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				PodIP:       nil,
				PodCIDR:     n.k8s.GetNodeCidr().ToRPC(),
				GatewayIP:   nil,
				ServiceCIDR: n.k8s.GetServiceCIDR().ToRPC(),
			},
			ENIInfo: nil,
			Pod: &rpc.Pod{
				Ingress:         podinfo.TcIngress,
				Egress:          podinfo.TcEgress,
				NetworkPriority: podinfo.NetworkPriority,
			},
			IfName:       "",
			ExtraRoutes:  nil,
			DefaultRoute: true,
		})
		allocIPReply.Success = true
	default:
		return nil, fmt.Errorf("not support pod network type")
	}

	// 4. grpc connection
	if ctx.Err() != nil {
		err = ctx.Err()
		return nil, fmt.Errorf("error on grpc connection, %w", err)
	}

	allocIPReply.NetConfs = netConf
	allocIPReply.EnableTrunking = n.enableTrunk

	// 5. return allocate result
	return allocIPReply, err
}

func (n *networkService) ReleaseIP(ctx context.Context, r *rpc.ReleaseIPRequest) (*rpc.ReleaseIPReply, error) {
	serviceLog.WithFields(map[string]interface{}{
		"pod":         podInfoKey(r.K8SPodNamespace, r.K8SPodName),
		"containerID": r.K8SPodInfraContainerId,
	}).Info("release ip req")

	_, exist := n.pendingPods.LoadOrStore(podInfoKey(r.K8SPodNamespace, r.K8SPodName), struct{}{})
	if exist {
		return nil, fmt.Errorf("pod %s resource processing", podInfoKey(r.K8SPodNamespace, r.K8SPodName))
	}
	defer func() {
		n.pendingPods.Delete(podInfoKey(r.K8SPodNamespace, r.K8SPodName))
	}()

	n.RLock()
	defer n.RUnlock()
	var (
		start = time.Now()
		err   error
	)
	defer func() {
		metric.RPCLatency.WithLabelValues("ReleaseIP", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	}()

	// 0. Get pod Info
	podinfo, err := n.k8s.GetPod(r.K8SPodNamespace, r.K8SPodName, true)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pod info for: %+v", r)
	}

	// 1. Init Context
	netCtx := &networkContext{
		Context:    ctx,
		resources:  []types.ResourceItem{},
		pod:        podinfo,
		k8sService: n.k8s,
	}
	releaseReply := &rpc.ReleaseIPReply{
		Success: true,
		IPv4:    n.ipFamily.IPv4,
		IPv6:    n.ipFamily.IPv6,
	}

	defer func() {
		if err != nil {
			netCtx.Log().Errorf("release result with error, %+v", err)
		} else {
			netCtx.Log().Infof("release result: %+v", releaseReply)
		}
	}()

	oldRes, err := n.getPodResource(podinfo)
	if err != nil {
		return nil, err
	}

	if !n.verifyPodNetworkType(podinfo.PodNetworkType) {
		netCtx.Log().Warnf("unexpect pod network type release, maybe daemon mode changed: %+v", podinfo.PodNetworkType)
		return releaseReply, nil
	}
	if oldRes.ContainerID != nil {
		if r.K8SPodInfraContainerId != *oldRes.ContainerID {
			netCtx.Log().Warnf("cni request not macth stored resource, expect %s, got %s, ignored", *oldRes.ContainerID, r.K8SPodInfraContainerId)
			return releaseReply, nil
		}
	}
	for _, res := range oldRes.Resources {
		//record old resource for pod
		netCtx.resources = append(netCtx.resources, res)
		mgr := n.getResourceManagerForRes(res.Type)
		if mgr == nil {
			netCtx.Log().Warnf("error cleanup allocated network resource %s, %s: %v", res.ID, res.Type, err)
			continue
		}
		if podinfo.IPStickTime == 0 {
			if err = mgr.Release(netCtx, res); err != nil && err != pool.ErrInvalidState {
				return nil, errors.Wrapf(err, "error release request network resource for: %+v", r)
			}
			if err = n.deletePodResource(podinfo); err != nil {
				return nil, errors.Wrapf(err, "error delete resource from db: %+v", r)
			}
		}
	}

	if netCtx.Err() != nil {
		err = ctx.Err()
		return nil, fmt.Errorf("error on grpc connection, %w", err)
	}

	return releaseReply, nil
}

func (n *networkService) GetIPInfo(ctx context.Context, r *rpc.GetInfoRequest) (*rpc.GetInfoReply, error) {
	serviceLog.Debugf("GetIPInfo request: %+v", r)
	// 0. Get pod Info
	podinfo, err := n.k8s.GetPod(r.K8SPodNamespace, r.K8SPodName, true)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pod info for: %+v", r)
	}

	if !n.verifyPodNetworkType(podinfo.PodNetworkType) {
		return nil, fmt.Errorf("unexpect pod network type get info, maybe daemon mode changed: %+v", podinfo.PodNetworkType)
	}

	// 1. Init Context
	networkContext := &networkContext{
		Context:    ctx,
		resources:  []types.ResourceItem{},
		pod:        podinfo,
		k8sService: n.k8s,
	}

	getIPInfoResult := &rpc.GetInfoReply{IPv4: n.ipFamily.IPv4, IPv6: n.ipFamily.IPv6}

	defer func() {
		networkContext.Log().Debugf("getIpInfo result: %+v", getIPInfoResult)
	}()

	n.RLock()
	podRes, err := n.getPodResource(podinfo)
	n.RUnlock()
	if err != nil {
		networkContext.Log().Errorf("failed to get pod info : %+v", err)
		return getIPInfoResult, err
	}

	if podRes.ContainerID != nil {
		if r.K8SPodInfraContainerId != *podRes.ContainerID {
			networkContext.Log().Warnf("cni request not macth stored resource, expect %s, got %s, ignored", *podRes.ContainerID, r.K8SPodInfraContainerId)
			return getIPInfoResult, nil
		}
	}

	var netConf []*rpc.NetConf
	// 2. return network info for pod
	switch podinfo.PodNetworkType {
	case podNetworkTypeENIMultiIP:
		getIPInfoResult.IPType = rpc.IPType_TypeENIMultiIP
		netConfs, err2 := n.multiIPFromCRD(podinfo, false)
		if err != nil {
			if k8sErr.IsNotFound(err2) {
				getIPInfoResult.Error = rpc.Error_ErrCRDNotFound
			}
			return getIPInfoResult, nil
		}
		netConf = append(netConf, netConfs...)

		defaultIfSet := false
		for _, cfg := range netConf {
			if defaultIf(cfg.IfName) {
				defaultIfSet = true
			}
		}
		if !defaultIfSet {
			resItems := podRes.GetResourceItemByType(types.ResourceTypeENIIP)
			if len(resItems) > 0 {
				// only have one
				res, err := n.eniIPResMgr.Stat(networkContext, resItems[0].ID)
				if err == nil {
					eniIP := res.(*types.ENIIP)

					netConf = append(netConf, &rpc.NetConf{
						BasicInfo: &rpc.BasicInfo{
							PodIP:       eniIP.IPSet.ToRPC(),
							PodCIDR:     eniIP.ENI.VSwitchCIDR.ToRPC(),
							GatewayIP:   eniIP.ENI.GatewayIP.ToRPC(),
							ServiceCIDR: n.k8s.GetServiceCIDR().ToRPC(),
						},
						ENIInfo: &rpc.ENIInfo{
							MAC:   eniIP.ENI.MAC,
							Trunk: false,
						},
						Pod: &rpc.Pod{
							Ingress:         podinfo.TcIngress,
							Egress:          podinfo.TcEgress,
							NetworkPriority: podinfo.NetworkPriority,
						},
						IfName:      "",
						ExtraRoutes: nil,
					})

				} else {
					serviceLog.Debugf("failed to get res stat %s", resItems[0].ID)
				}
			}
		}
		err = defaultForNetConf(netConf)
		if err != nil {
			return getIPInfoResult, err
		}
	case podNetworkTypeVPCIP:
		getIPInfoResult.IPType = rpc.IPType_TypeVPCIP
		netConf = append(netConf, &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				PodIP:       nil,
				PodCIDR:     n.k8s.GetNodeCidr().ToRPC(),
				GatewayIP:   nil,
				ServiceCIDR: n.k8s.GetServiceCIDR().ToRPC(),
			},
			Pod: &rpc.Pod{
				Ingress:         podinfo.TcIngress,
				Egress:          podinfo.TcEgress,
				NetworkPriority: podinfo.NetworkPriority,
			},
			DefaultRoute: true,
		})
	case podNetworkTypeVPCENI:
		getIPInfoResult.IPType = rpc.IPType_TypeVPCENI
		if n.ipamType == types.IPAMTypeCRD {
			netConfs, err2 := n.exclusiveENIFromCRD(podinfo, false)
			if err2 != nil {
				if k8sErr.IsNotFound(err2) {
					getIPInfoResult.Error = rpc.Error_ErrCRDNotFound
				}
				return getIPInfoResult, nil
			}
			netConf = append(netConf, netConfs...)
		} else {
			resItems := podRes.GetResourceItemByType(types.ResourceTypeENI)
			if len(resItems) > 0 {
				// only have one
				res, err := n.eniResMgr.Stat(networkContext, resItems[0].ID)
				if err == nil {
					eni := res.(*types.ENI)

					netConf = append(netConf, &rpc.NetConf{
						BasicInfo: &rpc.BasicInfo{
							PodIP:       eni.PrimaryIP.ToRPC(),
							PodCIDR:     eni.VSwitchCIDR.ToRPC(),
							GatewayIP:   eni.GatewayIP.ToRPC(),
							ServiceCIDR: n.k8s.GetServiceCIDR().ToRPC(),
						},
						ENIInfo: &rpc.ENIInfo{
							MAC:   eni.MAC,
							Trunk: podinfo.PodENI && n.enableTrunk && eni.Trunk,
						},
						Pod: &rpc.Pod{
							Ingress:         podinfo.TcIngress,
							Egress:          podinfo.TcEgress,
							NetworkPriority: podinfo.NetworkPriority,
						},
						IfName:       "",
						ExtraRoutes:  nil,
						DefaultRoute: true,
					})
				} else {
					serviceLog.Debugf("failed to get res stat %s", resItems[0].ID)
				}
			}
		}
	default:
		return getIPInfoResult, errors.Errorf("unknown or unsupport network type for: %v", r)
	}

	getIPInfoResult.NetConfs = netConf
	getIPInfoResult.EnableTrunking = n.enableTrunk

	return getIPInfoResult, nil
}

func (n *networkService) RecordEvent(_ context.Context, r *rpc.EventRequest) (*rpc.EventReply, error) {
	eventType := eventTypeNormal
	if r.EventType == rpc.EventType_EventTypeWarning {
		eventType = eventTypeWarning
	}

	reply := &rpc.EventReply{
		Succeed: true,
		Error:   "",
	}

	if r.EventTarget == rpc.EventTarget_EventTargetNode { // Node
		n.k8s.RecordNodeEvent(eventType, r.Reason, r.Message)
		return reply, nil
	}

	// Pod
	err := n.k8s.RecordPodEvent(r.K8SPodName, r.K8SPodNamespace, eventType, r.Reason, r.Message)
	if err != nil {
		reply.Succeed = false
		reply.Error = err.Error()

		return reply, err
	}

	return reply, nil
}

func (n *networkService) verifyPodNetworkType(podNetworkMode string) bool {
	return (n.daemonMode == daemonModeVPC && //vpc
		(podNetworkMode == podNetworkTypeVPCENI || podNetworkMode == podNetworkTypeVPCIP)) ||
		// eni-multi-ip
		(n.daemonMode == daemonModeENIMultiIP && podNetworkMode == podNetworkTypeENIMultiIP) ||
		// eni-only
		(n.daemonMode == daemonModeENIOnly && podNetworkMode == podNetworkTypeVPCENI)
}

func (n *networkService) startGarbageCollectionLoop() {
	// period do network resource gc
	gcTicker := time.NewTicker(gcPeriod)
	go func() {
		for range gcTicker.C {
			serviceLog.Debugf("do resource gc on node")
			n.Lock()
			pods, err := n.k8s.GetLocalPods()
			if err != nil {
				serviceLog.Warnf("error get local pods for gc")
				n.Unlock()
				continue
			}
			podKeyMap := make(map[string]bool)

			for _, pod := range pods {
				if !pod.SandboxExited {
					podKeyMap[podInfoKey(pod.Namespace, pod.Name)] = true
				}
			}

			var (
				inUseSet         = make(map[string]map[string]types.ResourceItem)
				expireSet        = make(map[string]map[string]types.ResourceItem)
				relateExpireList = make([]string, 0)
			)

			resRelateList, err := n.resourceDB.List()
			if err != nil {
				serviceLog.Warnf("error list resource db for gc")
				n.Unlock()
				continue
			}

			for _, resRelateObj := range resRelateList {
				resRelate := resRelateObj.(types.PodResources)
				_, podExist := podKeyMap[podInfoKey(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name)]
				if !podExist {
					// check kbe-api again
					exist, err := n.k8s.PodExist(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name)
					if err != nil || exist {
						continue
					}
					serviceLog.Infof("found pod %s not exist, will cleanup related resource", podInfoKey(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name))

					if resRelate.PodInfo.IPStickTime != 0 {
						// delay resource garbage collection for sticky ip
						resRelate.PodInfo.IPStickTime = 0
						if err = n.resourceDB.Put(podInfoKey(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name),
							resRelate); err != nil {
							serviceLog.Warnf("error store pod info to resource db")
						}
						podExist = true
					} else {
						relateExpireList = append(relateExpireList, podInfoKey(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name))
					}
				}
				for _, res := range resRelate.Resources {
					if _, ok := inUseSet[res.Type]; !ok {
						inUseSet[res.Type] = make(map[string]types.ResourceItem)
						expireSet[res.Type] = make(map[string]types.ResourceItem)
					}
					// already in use by others
					if _, ok := inUseSet[res.Type][res.ID]; ok {
						continue
					}
					if podExist {
						// remove resource from expirelist
						delete(expireSet[res.Type], res.ID)
						inUseSet[res.Type][res.ID] = res
					} else {
						if _, ok := inUseSet[res.Type][res.ID]; !ok {
							expireSet[res.Type][res.ID] = res
						}
					}
				}
			}
			gcDone := true
			for mgrType := range inUseSet {
				mgr, ok := n.mgrForResource[mgrType]
				if ok {
					serviceLog.Debugf("start garbage collection for %v, list: %+v， %+v", mgrType, inUseSet[mgrType], expireSet[mgrType])
					err = mgr.GarbageCollection(inUseSet[mgrType], expireSet[mgrType])
					if err != nil {
						serviceLog.Warnf("error do garbage collection for %+v, inuse: %v, expire: %v, err: %v", mgrType, inUseSet[mgrType], expireSet[mgrType], err)
						gcDone = false
					}
				}
			}
			if gcDone {
				func() {
					resMap, ok := expireSet[types.ResourceTypeENIIP]
					if !ok {
						return
					}
					for resID := range resMap {
						// try clean ip rules
						list := strings.SplitAfterN(resID, ".", 2)
						if len(list) <= 1 {
							serviceLog.Debugf("skip gc res id %s", resID)
							continue
						}
						serviceLog.Debugf("checking ip %s", list[1])
						_, addr, err := net.ParseCIDR(fmt.Sprintf("%s/32", list[1]))
						if err != nil {
							serviceLog.Errorf("failed parse ip %s", list[1])
							return
						}
						// try clean all
						err = link.DeleteIPRulesByIP(addr)
						if err != nil {
							serviceLog.Errorf("failed release ip rules %v", err)
						}
						err = link.DeleteRouteByIP(addr)
						if err != nil {
							serviceLog.Errorf("failed delete route %v", err)
						}
					}
				}()

				for _, relate := range relateExpireList {
					err = n.resourceDB.Delete(relate)
					if err != nil {
						serviceLog.Warnf("error delete resource db relation: %v", err)
					}
				}
			}
			n.Unlock()
		}
	}()
}

func (n *networkService) startPeriodCheck() {
	// check pool
	func() {
		serviceLog.Debugf("compare poll with metadata")
		podMapping, err := n.GetResourceMapping()
		if err != nil {
			serviceLog.Error(err)
			return
		}
		for _, res := range podMapping {
			if res.Valid {
				continue
			}
			if res.Name == "" || res.Namespace == "" {
				// just log
				serviceLog.Warnf("found resource invalid %s %s", res.LocalResID, res.RemoteResID)
			} else {
				_ = tracing.RecordPodEvent(res.Name, res.Namespace, corev1.EventTypeWarning, "ResourceInvalid", fmt.Sprintf("resource %s", res.LocalResID))
			}
		}
	}()
}

// requestCRD get crd from api
// note: need tolerate crd is not exist, so contained can del pod normally
func (n *networkService) requestCRD(podInfo *types.PodInfo, waitReady bool) (*podENITypes.PodENI, error) {
	if n.ipamType == types.IPAMTypeCRD || podInfo.PodENI && n.enableTrunk {
		var podENI *podENITypes.PodENI
		var err error
		if waitReady {
			podENI, err = n.k8s.WaitPodENIInfo(podInfo)
		} else {
			podENI, err = n.k8s.GetPodENIInfo(podInfo)
		}
		if err != nil {
			return nil, err
		}
		if len(podENI.Spec.Allocations) <= 0 {
			return nil, fmt.Errorf("podENI has no allocation info")
		}

		return podENI, nil
	}
	return nil, nil
}

func (n *networkService) multiIPFromCRD(podInfo *types.PodInfo, waitReady bool) ([]*rpc.NetConf, error) {
	var netConf []*rpc.NetConf

	var nodeTrunkENI *types.ENI
	podEni, err := n.requestCRD(podInfo, waitReady)
	if err != nil {
		return nil, fmt.Errorf("error wait pod eni info, %w", err)
	}
	if podEni == nil {
		return nil, nil
	}
	nodeTrunkENI = n.eniIPResMgr.(*eniIPResourceManager).trunkENI
	if nodeTrunkENI == nil || nodeTrunkENI.ID != podEni.Status.TrunkENIID {
		return nil, fmt.Errorf("pod status eni parent not match instance trunk eni")
	}
	// for now only ipvlan is supported

	// call api to get eni info
	for _, alloc := range podEni.Spec.Allocations {
		podIP := &rpc.IPSet{}
		cidr := &rpc.IPSet{}
		gw := &rpc.IPSet{}
		if alloc.IPv4 != "" {
			podIP.IPv4 = alloc.IPv4
			cidr.IPv4 = alloc.IPv4CIDR
			gw.IPv4 = terwayIP.DeriveGatewayIP(alloc.IPv4CIDR)

			if cidr.IPv4 == "" || gw.IPv4 == "" {
				return nil, fmt.Errorf("empty cidr or gateway")
			}
		}
		if alloc.IPv6 != "" {
			podIP.IPv6 = alloc.IPv6
			cidr.IPv6 = alloc.IPv6CIDR
			gw.IPv6 = terwayIP.DeriveGatewayIP(alloc.IPv6CIDR)

			if cidr.IPv6 == "" || gw.IPv6 == "" {
				return nil, fmt.Errorf("empty cidr or gateway")
			}
		}
		eniInfo := &rpc.ENIInfo{
			MAC:       nodeTrunkENI.MAC, // set trunk eni mac
			Trunk:     true,
			GatewayIP: nodeTrunkENI.GatewayIP.ToRPC(),
		}
		info, ok := podEni.Status.ENIInfos[alloc.ENI.ID]
		if !ok {
			return nil, fmt.Errorf("error get podENI status")
		}
		vid := uint32(info.Vid)
		eniInfo.Vid = vid

		netConf = append(netConf, &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				PodIP:       podIP,
				PodCIDR:     cidr,
				GatewayIP:   gw,
				ServiceCIDR: n.k8s.GetServiceCIDR().ToRPC(),
			},
			ENIInfo: eniInfo,
			Pod: &rpc.Pod{
				Ingress:         podInfo.TcIngress,
				Egress:          podInfo.TcEgress,
				NetworkPriority: podInfo.NetworkPriority,
			},
			IfName:       alloc.Interface,
			ExtraRoutes:  parseExtraRoute(alloc.ExtraRoutes),
			DefaultRoute: alloc.DefaultRoute,
		})
	}

	return netConf, nil
}

func (n *networkService) exclusiveENIFromCRD(podInfo *types.PodInfo, waitReady bool) ([]*rpc.NetConf, error) {
	var netConf []*rpc.NetConf

	var nodeTrunkENI *types.ENI
	podEni, err := n.requestCRD(podInfo, waitReady)
	if err != nil {
		return nil, fmt.Errorf("error wait pod eni info, %w", err)
	}

	if n.enableTrunk {
		nodeTrunkENI = n.eniResMgr.(*eniResourceManager).trunkENI
		if nodeTrunkENI == nil || nodeTrunkENI.ID != podEni.Status.TrunkENIID {
			return nil, fmt.Errorf("pod status eni parent not match instance trunk eni")
		}
	}

	// call api to get eni info
	for _, alloc := range podEni.Spec.Allocations {
		podIP := &rpc.IPSet{}
		cidr := &rpc.IPSet{}
		gw := &rpc.IPSet{}
		if alloc.IPv4 != "" {
			podIP.IPv4 = alloc.IPv4
			cidr.IPv4 = alloc.IPv4CIDR
			gw.IPv4 = terwayIP.DeriveGatewayIP(alloc.IPv4CIDR)

			if cidr.IPv4 == "" || gw.IPv4 == "" {
				return nil, fmt.Errorf("empty cidr or gateway")
			}
		}
		if alloc.IPv6 != "" {
			podIP.IPv6 = alloc.IPv6
			cidr.IPv6 = alloc.IPv6CIDR
			gw.IPv6 = terwayIP.DeriveGatewayIP(alloc.IPv6CIDR)

			if cidr.IPv6 == "" || gw.IPv6 == "" {
				return nil, fmt.Errorf("empty cidr or gateway")
			}
		}
		eniInfo := &rpc.ENIInfo{
			MAC:   alloc.ENI.MAC,
			Trunk: false,
		}
		if n.enableTrunk {
			eniInfo.MAC = nodeTrunkENI.MAC // set trunk eni mac
			eniInfo.Trunk = true
			info, ok := podEni.Status.ENIInfos[alloc.ENI.ID]
			if !ok {
				return nil, fmt.Errorf("error get podENI status")
			}
			eniInfo.Vid = uint32(info.Vid)
			eniInfo.GatewayIP = nodeTrunkENI.GatewayIP.ToRPC()
		}
		netConf = append(netConf, &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				PodIP:       podIP,
				PodCIDR:     cidr,
				GatewayIP:   gw,
				ServiceCIDR: n.k8s.GetServiceCIDR().ToRPC(),
			},
			ENIInfo: eniInfo,
			Pod: &rpc.Pod{
				Ingress:         podInfo.TcIngress,
				Egress:          podInfo.TcEgress,
				NetworkPriority: podInfo.NetworkPriority,
			},
			IfName:       alloc.Interface,
			ExtraRoutes:  parseExtraRoute(alloc.ExtraRoutes),
			DefaultRoute: alloc.DefaultRoute,
		})
	}
	err = defaultForNetConf(netConf)
	if err != nil {
		return nil, err
	}
	return netConf, nil
}

func (n *networkService) migrateEIP(ctx context.Context, objs []interface{}) error {
	once := sync.Once{}

	for _, resObj := range objs {
		podRes, ok := resObj.(types.PodResources)
		if !ok {
			continue
		}
		if podRes.PodInfo == nil || !podRes.PodInfo.EipInfo.PodEip {
			continue
		}
		for _, eipRes := range podRes.Resources {
			if eipRes.Type != types.ResourceTypeEIP {
				continue
			}
			allocType := v1beta1.IPAllocTypeAuto
			if podRes.PodInfo.EipInfo.PodEipID != "" {
				allocType = v1beta1.IPAllocTypeStatic
			}
			releaseStrategy := v1beta1.ReleaseStrategyFollow
			releaseAfter := ""

			if podRes.PodInfo.IPStickTime > 0 {
				releaseStrategy = v1beta1.ReleaseStrategyTTL
				releaseAfter = podRes.PodInfo.IPStickTime.String()
			}

			var err error
			once.Do(func() {
				err = crds.RegisterCRD([]string{crds.CRDPodEIP})
			})
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)

			l := serviceLog.WithField("name", fmt.Sprintf("%s/%s", podRes.PodInfo.Namespace, podRes.PodInfo.Name))

			c := n.k8s.GetClient()
			podEIP := &v1beta1.PodEIP{}
			err = c.Get(ctx, k8stypes.NamespacedName{Namespace: podRes.PodInfo.Namespace, Name: podRes.PodInfo.Name}, podEIP)
			if err == nil {
				cancel()
				l.Info("skip create podEIP, already exist")
				continue
			}
			if !k8sErr.IsNotFound(err) {
				cancel()
				return err
			}

			err = retry.OnError(wait.Backoff{
				Steps:    4,
				Duration: 200 * time.Millisecond,
				Factor:   5.0,
				Jitter:   0.1,
			}, func(err error) bool {
				if k8sErr.IsTooManyRequests(err) {
					return true
				}
				if k8sErr.IsInternalError(err) {
					return true
				}
				return false
			}, func() error {
				podEIP = &v1beta1.PodEIP{
					ObjectMeta: metav1.ObjectMeta{
						Name:        podRes.PodInfo.Name,
						Namespace:   podRes.PodInfo.Namespace,
						Annotations: map[string]string{},
						Finalizers:  []string{"podeip-controller.alibabacloud.com/finalizer"},
					},
					Spec: v1beta1.PodEIPSpec{
						AllocationID:       eipRes.ID,
						BandwidthPackageID: podRes.PodInfo.EipInfo.PodEipBandwidthPackageID,
						AllocationType: v1beta1.AllocationType{
							Type:            allocType,
							ReleaseStrategy: releaseStrategy,
							ReleaseAfter:    releaseAfter,
						},
					},
				}

				l.Infof("create podEIP for %v", podRes)

				err := c.Create(ctx, podEIP)
				if k8sErr.IsAlreadyExists(err) {
					return nil
				}
				return err
			})
			cancel()

			if err != nil {
				return err
			}
		}
	}
	return nil
}

// tracing
func (n *networkService) Config() []tracing.MapKeyValueEntry {
	// name, daemon_mode, configFilePath, kubeconfig, master
	config := []tracing.MapKeyValueEntry{
		{Key: tracingKeyName, Value: networkServiceName}, // use a unique name?
		{Key: tracingKeyDaemonMode, Value: n.daemonMode},
		{Key: tracingKeyConfigFilePath, Value: n.configFilePath},
	}

	return config
}

func (n *networkService) Trace() []tracing.MapKeyValueEntry {
	count := 0
	n.pendingPods.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	trace := []tracing.MapKeyValueEntry{
		{Key: tracingKeyPendingPodsCount, Value: fmt.Sprint(count)},
	}
	resList, err := n.resourceDB.List()
	if err != nil {
		trace = append(trace, tracing.MapKeyValueEntry{Key: "error", Value: err.Error()})
		return trace
	}

	for _, v := range resList {
		res := v.(types.PodResources)

		var resources []string
		for _, v := range res.Resources {
			resource := fmt.Sprintf("(%s)%s", v.Type, v.ID)
			resources = append(resources, resource)
		}

		key := fmt.Sprintf("pods/%s/%s/resources", res.PodInfo.Namespace, res.PodInfo.Name)
		trace = append(trace, tracing.MapKeyValueEntry{Key: key, Value: strings.Join(resources, " ")})
	}

	return trace
}

func (n *networkService) Execute(cmd string, _ []string, message chan<- string) {
	switch cmd {
	case commandMapping:
		mapping, err := n.GetResourceMapping()
		message <- fmt.Sprintf("mapping: %v, err: %s\n", mapping, err)
	default:
		message <- "can't recognize command\n"
	}

	close(message)
}

func (n *networkService) GetResourceMapping() ([]*tracing.PodMapping, error) {
	var poolStats tracing.ResourcePoolStats
	var err error

	n.RLock()
	// get []ResourceMapping
	switch n.daemonMode {
	case daemonModeENIMultiIP:
		poolStats, err = n.eniIPResMgr.GetResourceMapping()
	case daemonModeVPC:
		n.RUnlock()
		return nil, nil
	case daemonModeENIOnly:
		poolStats, err = n.eniResMgr.GetResourceMapping()
	}
	if err != nil {
		n.RUnlock()
		return nil, err
	}
	// pod related res
	pods, err := n.resourceDB.List()
	n.RUnlock()
	if err != nil {
		return nil, err
	}

	return toResMapping(poolStats, pods)
}

// toResMapping toResMapping
func toResMapping(poolStats tracing.ResourcePoolStats, pods []interface{}) ([]*tracing.PodMapping, error) {
	// three way compare, use resource id as key

	all := map[string]*tracing.PodMapping{}

	for _, res := range poolStats.GetLocal() {
		old, ok := all[res.GetID()]
		if !ok {
			all[res.GetID()] = &tracing.PodMapping{
				LocalResID: res.GetID(),
			}
			continue
		}
		old.LocalResID = res.GetID()
	}

	for _, res := range poolStats.GetRemote() {
		old, ok := all[res.GetID()]
		if !ok {
			all[res.GetID()] = &tracing.PodMapping{
				RemoteResID: res.GetID(),
			}
			continue
		}
		old.RemoteResID = res.GetID()
	}

	for _, pod := range pods {
		p := pod.(types.PodResources)
		for _, res := range p.Resources {
			if res.Type == types.ResourceTypeEIP {
				continue
			}
			old, ok := all[res.ID]
			if !ok {
				all[res.ID] = &tracing.PodMapping{
					Name:         p.PodInfo.Name,
					Namespace:    p.PodInfo.Namespace,
					PodBindResID: res.ID,
				}
				continue
			}
			old.Name = p.PodInfo.Name
			old.Namespace = p.PodInfo.Namespace
			old.PodBindResID = res.ID
			if old.PodBindResID == old.LocalResID && old.LocalResID == old.RemoteResID {
				old.Valid = true
			}
		}
	}

	mapping := make([]*tracing.PodMapping, 0, len(all))
	for _, res := range all {
		// idle
		if res.Name == "" && res.LocalResID == res.RemoteResID {
			res.Valid = true
		}
		mapping = append(mapping, res)
	}

	sort.Slice(mapping, func(i, j int) bool {
		if mapping[i].Name != mapping[j].Name {
			return mapping[i].Name > mapping[j].Name
		}
		return mapping[i].RemoteResID < mapping[j].RemoteResID
	})
	return mapping, nil
}

func newNetworkService(ctx context.Context, configFilePath, daemonMode string) (rpc.TerwayBackendServer, error) {
	serviceLog.Debugf("start network service with: %s, %s", configFilePath, daemonMode)

	netSrv := &networkService{
		configFilePath: configFilePath,
		pendingPods:    sync.Map{},
	}
	if daemonMode == daemonModeENIMultiIP || daemonMode == daemonModeVPC || daemonMode == daemonModeENIOnly {
		netSrv.daemonMode = daemonMode
	} else {
		return nil, fmt.Errorf("unsupport daemon mode")
	}

	var err error

	globalConfig, err := daemon.GetConfigFromFileWithMerge(configFilePath, nil)
	if err != nil {
		return nil, err
	}

	netSrv.k8s, err = newK8S(daemonMode, globalConfig)
	if err != nil {
		return nil, fmt.Errorf("error init k8s: %w", err)
	}

	// load dynamic config
	dynamicCfg, nodeLabel, err := getDynamicConfig(netSrv.k8s)
	if err != nil {
		serviceLog.Warnf("get dynamic config error: %s. fallback to default config", err.Error())
		dynamicCfg = ""
	}

	config, err := daemon.GetConfigFromFileWithMerge(configFilePath, []byte(dynamicCfg))
	if err != nil {
		return nil, fmt.Errorf("failed parse config: %v", err)
	}

	if len(dynamicCfg) == 0 {
		serviceLog.Infof("got config: %+v from: %+v", config, configFilePath)
	} else {
		serviceLog.Infof("got config: %+v from %+v, with dynamic config %+v", config, configFilePath, nodeLabel)
	}

	config.Populate()
	err = config.Validate()
	if err != nil {
		return nil, err
	}

	backoff.OverrideBackoff(config.BackoffOverride)
	_ = netSrv.k8s.SetCustomStatefulWorkloadKinds(config.CustomStatefulWorkloadKinds)
	netSrv.ipamType = config.IPAMType
	netSrv.eniCapPolicy = config.ENICapPolicy

	ipFamily := types.NewIPFamilyFromIPStack(types.IPStack(config.IPStack))
	netSrv.ipFamily = ipFamily

	var providers []credential.Interface
	if string(config.AccessID) != "" && string(config.AccessSecret) != "" {
		providers = append(providers, credential.NewAKPairProvider(string(config.AccessID), string(config.AccessSecret)))
	}
	providers = append(providers, credential.NewEncryptedCredentialProvider(utils.NormalizePath(config.CredentialPath), "", ""))
	providers = append(providers, credential.NewMetadataProvider())

	clientSet, err := credential.NewClientMgr(config.RegionID, providers...)
	if err != nil {
		return nil, err
	}

	aliyunClient, err := client.New(clientSet,
		flowcontrol.NewTokenBucketRateLimiter(8, 10),
		flowcontrol.NewTokenBucketRateLimiter(4, 5))
	if err != nil {
		return nil, errors.Wrapf(err, "error create aliyun client")
	}

	limit, err := aliyun.GetLimit(aliyunClient, config.InstanceType)
	if err != nil {
		return nil, fmt.Errorf("upable get instance limit, %w", err)
	}
	if ipFamily.IPv6 {
		if !limit.SupportIPv6() {
			ipFamily.IPv6 = false
			serviceLog.Warnf("instance %s is not support ipv6", config.InstanceType)
		} else if daemonMode == daemonModeENIMultiIP && !limit.SupportMultiIPIPv6() {
			ipFamily.IPv6 = false
			serviceLog.Warnf("instance %s is not support ipv6", config.InstanceType)
		}
	}

	if limit.TrunkPod() <= 0 {
		config.EnableENITrunking = false
	}

	ecs := aliyun.NewAliyunImpl(aliyunClient, config.EnableENITrunking && !config.WaitTrunkENI, ipFamily, config.ENITagFilter)

	netSrv.resourceDB, err = storage.NewDiskStorage(
		resDBName, utils.NormalizePath(resDBPath), json.Marshal, func(bytes []byte) (interface{}, error) {
			resourceRel := &types.PodResources{}
			err = json.Unmarshal(bytes, resourceRel)
			if err != nil {
				return nil, errors.Wrapf(err, "error unmarshal pod relate resource")
			}
			return *resourceRel, nil
		})
	if err != nil {
		return nil, errors.Wrapf(err, "error init resource manager storage")
	}

	nodeAnnotations := map[string]string{}

	// get pool config
	poolConfig, err := getPoolConfig(config, daemonMode, limit)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pool config")
	}
	serviceLog.Infof("init pool config: %+v", poolConfig)

	vswPool, err := vswpool.NewSwitchPool(100, "10m")
	if err != nil {
		return nil, fmt.Errorf("error init vsw pool, %w", err)
	}

	// init trunk
	if config.EnableENITrunking {
		preferTrunkID := netSrv.k8s.GetTrunkID()
		if preferTrunkID == "" && config.WaitTrunkENI {
			preferTrunkID, err = netSrv.k8s.WaitTrunkReady()
			if err != nil {
				return nil, fmt.Errorf("error wait trunk ready, %w", err)
			}
		}

		if !config.WaitTrunkENI {
			enis, err := ecs.GetAttachedENIs(ctx, false, preferTrunkID)
			if err != nil {
				return nil, fmt.Errorf("error get attached eni, %w", err)
			}
			found := false
			for _, eni := range enis {
				if eni.Trunk && eni.ID == preferTrunkID {
					found = true

					poolConfig.TrunkENIID = preferTrunkID
					netSrv.enableTrunk = true

					nodeAnnotations[types.TrunkOn] = preferTrunkID
					nodeAnnotations[string(types.MemberENIIPTypeIPs)] = strconv.Itoa(poolConfig.MaxMemberENI)
					break
				}
			}
			if !found {
				if poolConfig.MaxENI > len(enis) {
					vsw, err := vswPool.GetOne(ctx, ecs, poolConfig.ZoneID, poolConfig.VSwitchOptions, &vswpool.SelectOptions{
						VSwitchSelectPolicy: vswpool.VSwitchSelectionPolicyMost,
					})
					if err != nil {
						return nil, fmt.Errorf("error get vsw, %w", err)
					}

					eni, err := ecs.AllocateENI(ctx, vsw.ID, poolConfig.SecurityGroupIDs, poolConfig.InstanceID, true, 1, poolConfig.ENITags)
					if err != nil {
						return nil, fmt.Errorf("error allocate eni, %w", err)
					}

					poolConfig.TrunkENIID = eni.ID
					netSrv.enableTrunk = true

					nodeAnnotations[types.TrunkOn] = eni.ID
					nodeAnnotations[string(types.MemberENIIPTypeIPs)] = strconv.Itoa(poolConfig.MaxMemberENI)
				} else {
					serviceLog.Warnf("no trunk eni found, fallback to non-trunk mode")

					config.EnableENITrunking = false
					config.DisableDevicePlugin = true
				}
			}
		} else {
			// WaitTrunkENI enabled, we believe what we got.
			poolConfig.TrunkENIID = preferTrunkID
			netSrv.enableTrunk = true

			nodeAnnotations[types.TrunkOn] = preferTrunkID
			nodeAnnotations[string(types.MemberENIIPTypeIPs)] = strconv.Itoa(poolConfig.MaxMemberENI)
		}
	}

	if daemonMode != daemonModeVPC {
		nodeAnnotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(poolConfig.Capacity)
	}

	if !(daemonMode == daemonModeENIMultiIP && !config.EnableENITrunking) {
		if !config.DisableDevicePlugin {
			res := deviceplugin.ENITypeENI
			capacity := poolConfig.MaxENI
			if config.EnableENITrunking {
				res = deviceplugin.ENITypeMember
				capacity = poolConfig.MaxMemberENI
			}

			dp := deviceplugin.NewENIDevicePlugin(capacity, res)
			go dp.Serve()
		}
	}

	// ensure node annotations
	err = netSrv.k8s.PatchNodeAnnotations(nodeAnnotations)
	if err != nil {
		return nil, fmt.Errorf("error patch node annotations, %w", err)
	}

	localResource := make(map[string]map[string]resourceManagerInitItem)
	resObjList, err := netSrv.resourceDB.List()
	if err != nil {
		return nil, errors.Wrapf(err, "error list resource relation db")
	}
	for _, resObj := range resObjList {
		podRes := resObj.(types.PodResources)
		for _, res := range podRes.Resources {
			if localResource[res.Type] == nil {
				localResource[res.Type] = make(map[string]resourceManagerInitItem)
			}
			localResource[res.Type][res.ID] = resourceManagerInitItem{item: res, podInfo: podRes.PodInfo}
		}
	}

	if config.EnableEIPMigrate {
		err = netSrv.migrateEIP(ctx, resObjList)
		if err != nil {
			return nil, err
		}
		serviceLog.Infof("eip migrate finished")
	}

	resStr, err := json.Marshal(localResource)
	if err != nil {
		return nil, err
	}
	serviceLog.Debugf("local resources to restore: %s", resStr)

	err = preStartResourceManager(daemonMode, netSrv.k8s)
	if err != nil {
		return nil, err
	}

	switch daemonMode {
	case daemonModeVPC:
		//init ENI
		netSrv.eniResMgr, err = newENIResourceManager(poolConfig, ecs, localResource[types.ResourceTypeENI], ipFamily, netSrv.k8s, netSrv.ipamType)
		if err != nil {
			return nil, errors.Wrapf(err, "error init ENI resource manager")
		}

		netSrv.vethResMgr, err = newVPCResourceManager()
		if err != nil {
			return nil, errors.Wrapf(err, "error init vpc resource manager")
		}

		netSrv.mgrForResource = map[string]ResourceManager{
			types.ResourceTypeENI:  netSrv.eniResMgr,
			types.ResourceTypeVeth: netSrv.vethResMgr,
		}

	case daemonModeENIMultiIP:
		//init ENI multi ip
		netSrv.eniIPResMgr, err = newENIIPResourceManager(poolConfig, ecs, netSrv.k8s, localResource[types.ResourceTypeENIIP], ipFamily)
		if err != nil {
			return nil, errors.Wrapf(err, "error init ENI ip resource manager")
		}
		netSrv.mgrForResource = map[string]ResourceManager{
			types.ResourceTypeENIIP: netSrv.eniIPResMgr,
		}
		if config.EnableEIPPool == conditionTrue && !config.EnableEIPMigrate {
			netSrv.eipResMgr = newEipResourceManager(ecs, netSrv.k8s, config.AllowEIPRob == conditionTrue)
			netSrv.mgrForResource[types.ResourceTypeEIP] = netSrv.eipResMgr
		}

	case daemonModeENIOnly:
		//init eni
		netSrv.eniResMgr, err = newENIResourceManager(poolConfig, ecs, localResource[types.ResourceTypeENI], ipFamily, netSrv.k8s, netSrv.ipamType)
		if err != nil {
			return nil, errors.Wrapf(err, "error init eni resource manager")
		}
		netSrv.mgrForResource = map[string]ResourceManager{
			types.ResourceTypeENIIP: netSrv.eniIPResMgr,
		}
		if config.EnableEIPPool == conditionTrue && !config.EnableENITrunking && !config.EnableEIPMigrate {
			netSrv.eipResMgr = newEipResourceManager(ecs, netSrv.k8s, config.AllowEIPRob == conditionTrue)
			netSrv.mgrForResource[types.ResourceTypeEIP] = netSrv.eipResMgr
		}
	default:
		panic("unsupported daemon mode" + daemonMode)
	}

	if config.IPAMType != types.IPAMTypeCRD {
		//start gc loop
		netSrv.startGarbageCollectionLoop()
		period := poolCheckPeriod
		periodCfg := os.Getenv("POOL_CHECK_PERIOD_SECONDS")
		periodSeconds, err := strconv.Atoi(periodCfg)
		if err == nil {
			period = time.Duration(periodSeconds) * time.Second
		}

		go wait.JitterUntil(netSrv.startPeriodCheck, period, 1, true, wait.NeverStop)
	}

	// register for tracing
	_ = tracing.Register(tracing.ResourceTypeNetworkService, "default", netSrv)
	tracing.RegisterResourceMapping(netSrv)
	tracing.RegisterEventRecorder(netSrv.k8s.RecordNodeEvent, netSrv.k8s.RecordPodEvent)

	return netSrv, nil
}

func parseExtraRoute(routes []podENITypes.Route) []*rpc.Route {
	if routes == nil {
		return nil
	}
	var res []*rpc.Route
	for _, r := range routes {
		res = append(res, &rpc.Route{
			Dst: r.Dst,
		})
	}
	return res
}

// set default val for netConf
func defaultForNetConf(netConf []*rpc.NetConf) error {
	// ignore netConf check
	if len(netConf) == 0 {
		return nil
	}
	defaultRouteSet := false
	defaultIfSet := false
	for i := 0; i < len(netConf); i++ {
		if netConf[i].DefaultRoute && defaultRouteSet {
			return fmt.Errorf("default route is dumplicated")
		}
		defaultRouteSet = defaultRouteSet || netConf[i].DefaultRoute

		if defaultIf(netConf[i].IfName) {
			defaultIfSet = true
		}
	}

	if !defaultIfSet {
		return fmt.Errorf("default interface is not set")
	}

	if !defaultRouteSet {
		for i := 0; i < len(netConf); i++ {
			if netConf[i].IfName == "" || netConf[i].IfName == IfEth0 {
				netConf[i].DefaultRoute = true
				break
			}
		}
	}

	return nil
}

func defaultIf(name string) bool {
	if name == "" || name == IfEth0 {
		return true
	}
	return false
}
