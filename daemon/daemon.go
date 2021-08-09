package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	podENITypes "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/cni/libcni"
	containertypes "github.com/containernetworking/cni/pkg/types"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	daemonModeVPC        = "VPC"
	daemonModeENIMultiIP = "ENIMultiIP"
	daemonModeENIOnly    = "ENIOnly"

	gcPeriod        = 5 * time.Minute
	poolCheckPeriod = 10 * time.Minute

	conditionFalse = "false"
	conditionTrue  = "true"

	networkServiceName         = "default"
	tracingKeyName             = "name"
	tracingKeyDaemonMode       = "daemon_mode"
	tracingKeyConfigFilePath   = "config_file_path"
	tracingKeyKubeConfig       = "kubeconfig"
	tracingKeyMaster           = "master"
	tracingKeyPendingPodsCount = "pending_pods_count"

	commandMapping = "mapping"

	cniDefaultPath = "/opt/cni/bin"
	// this file is generated from configmap
	terwayCNIConf  = "/etc/eni/10-terway.conf"
	cniExecTimeout = 10 * time.Second
)

type networkService struct {
	daemonMode     string
	configFilePath string
	kubeConfig     string
	master         string
	k8s            Kubernetes
	resourceDB     storage.Storage
	vethResMgr     ResourceManager
	eniResMgr      ResourceManager
	eniIPResMgr    ResourceManager
	eipResMgr      ResourceManager
	//networkResourceMgr ResourceManager
	mgrForResource map[string]ResourceManager
	pendingPods    sync.Map
	sync.RWMutex

	cniBinPath string

	enableTrunk bool

	rpc.UnimplementedTerwayBackendServer
}

var _ rpc.TerwayBackendServer = (*networkService)(nil)

func (networkService *networkService) getResourceManagerForRes(resType string) ResourceManager {
	return networkService.mgrForResource[resType]
}

//return resource relation in db, or return nil.
func (networkService *networkService) getPodResource(info *podInfo) (PodResources, error) {
	obj, err := networkService.resourceDB.Get(podInfoKey(info.Namespace, info.Name))
	if err == nil {
		return obj.(PodResources), nil
	}
	if err == storage.ErrNotFound {
		return PodResources{}, nil
	}

	return PodResources{}, err
}

func (networkService *networkService) deletePodResource(info *podInfo) error {
	key := podInfoKey(info.Namespace, info.Name)
	return networkService.resourceDB.Delete(key)
}

func (networkService *networkService) allocateVeth(ctx *networkContext, old *PodResources) (*types.Veth, error) {
	oldVethRes := old.GetResourceItemByType(types.ResourceTypeVeth)
	oldVethID := ""
	if old.PodInfo != nil {
		if len(oldVethRes) == 0 {
			ctx.Log().Debugf("veth for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldVethRes) > 1 {
			ctx.Log().Warnf("veth for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldVethID = oldVethRes[0].ID
		}
	}

	res, err := networkService.vethResMgr.Allocate(ctx, oldVethID)
	if err != nil {
		return nil, err
	}
	return res.(*types.Veth), nil
}

func (networkService *networkService) allocateENI(ctx *networkContext, old *PodResources) (*types.ENI, error) {
	oldENIRes := old.GetResourceItemByType(types.ResourceTypeENI)
	oldENIID := ""
	if old.PodInfo != nil {
		if len(oldENIRes) == 0 {
			ctx.Log().Debugf("eniip for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldENIRes) > 1 {
			ctx.Log().Warnf("eniip for pod %s more than one", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldENIID = oldENIRes[0].ID
		}
	}

	res, err := networkService.eniResMgr.Allocate(ctx, oldENIID)
	if err != nil {
		return nil, err
	}
	return res.(*types.ENI), nil
}

func (networkService *networkService) allocateENIMultiIP(ctx *networkContext, old *PodResources) (*types.ENIIP, error) {
	oldENIIPRes := old.GetResourceItemByType(types.ResourceTypeENIIP)
	oldENIIPID := ""
	if old.PodInfo != nil {
		if len(oldENIIPRes) == 0 {
			ctx.Log().Debugf("eniip for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldENIIPRes) > 1 {
			ctx.Log().Warnf("eniip for pod %s more than one", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldENIIPID = oldENIIPRes[0].ID
		}
	}

	res, err := networkService.eniIPResMgr.Allocate(ctx, oldENIIPID)
	if err != nil {
		return nil, err
	}
	return res.(*types.ENIIP), nil
}

func (networkService *networkService) allocateEIP(ctx *networkContext, old *PodResources) (*types.EIP, error) {
	oldEIPRes := old.GetResourceItemByType(types.ResourceTypeEIP)
	oldEIPID := ""
	if old.PodInfo != nil {
		if len(oldEIPRes) == 0 {
			ctx.Log().Debugf("eip for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldEIPRes) > 1 {
			ctx.Log().Warnf("eip for pod %s more than one", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldEIPID = oldEIPRes[0].ID
		}
	}

	res, err := networkService.eipResMgr.Allocate(ctx, oldEIPID)
	if err != nil {
		return nil, err
	}
	return res.(*types.EIP), nil
}

func (networkService *networkService) AllocIP(ctx context.Context, r *rpc.AllocIPRequest) (*rpc.AllocIPReply, error) {
	log.Infof("alloc ip request: %+v", r)
	_, exist := networkService.pendingPods.LoadOrStore(podInfoKey(r.K8SPodNamespace, r.K8SPodName), struct{}{})
	if exist {
		return nil, fmt.Errorf("pod %s resource processing", podInfoKey(r.K8SPodNamespace, r.K8SPodName))
	}
	defer func() {
		networkService.pendingPods.Delete(podInfoKey(r.K8SPodNamespace, r.K8SPodName))
	}()

	networkService.RLock()
	defer networkService.RUnlock()
	var (
		start = time.Now()
		err   error
	)
	defer func() {
		metric.RPCLatency.WithLabelValues("AllocIP", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	}()

	// 0. Get pod Info
	podinfo, err := networkService.k8s.GetPod(r.K8SPodNamespace, r.K8SPodName)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pod info for: %+v", r)
	}

	// 1. Init Context
	networkContext := &networkContext{
		Context:    ctx,
		resources:  []ResourceItem{},
		pod:        podinfo,
		k8sService: networkService.k8s,
	}
	allocIPReply := &rpc.AllocIPReply{}

	defer func() {
		// roll back allocated resource when error
		if err != nil {
			networkContext.Log().Errorf("alloc result with error, %+v", err)
			for _, res := range networkContext.resources {
				err = networkService.deletePodResource(podinfo)
				networkContext.Log().Errorf("rollback res[%v] with error, %+v", res, err)
				mgr := networkService.getResourceManagerForRes(res.Type)
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
		}
	}()

	// 2. Find old resource info
	oldRes, err := networkService.getPodResource(podinfo)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pod resources from db for pod %+v", podinfo)
	}

	if !networkService.verifyPodNetworkType(podinfo.PodNetworkType) {
		return nil, fmt.Errorf("unexpect pod network type allocate, maybe daemon mode changed: %+v", podinfo.PodNetworkType)
	}

	// 3. Allocate network resource for pod
	switch podinfo.PodNetworkType {
	case podNetworkTypeENIMultiIP:
		var eniMultiIP *types.ENIIP
		if podinfo.PodENI && networkService.enableTrunk {
			var podEni *podENITypes.PodENI
			podEni, err = networkService.k8s.WaitPodENIInfo(podinfo)
			if err != nil {
				return nil, errors.Wrapf(err, "error wait pod eni info")
			}
			nodeTrunkENI := networkService.eniIPResMgr.(*eniIPResourceManager).trunkENI
			if nodeTrunkENI == nil || nodeTrunkENI.ID != podEni.Status.TrunkENIID {
				return nil, fmt.Errorf("pod status eni parent not match instance trunk eni")
			}
			eniMultiIP = &types.ENIIP{
				ENI: nodeTrunkENI,
				SecondaryIP: types.IPSet{
					IPv4: net.ParseIP(podEni.Spec.Allocation.IPv4),
					IPv6: net.ParseIP(podEni.Spec.Allocation.IPv6),
				},
			}

		} else {
			eniMultiIP, err = networkService.allocateENIMultiIP(networkContext, &oldRes)
			if err != nil {
				return nil, fmt.Errorf("error get allocated eniip ip for: %+v, result: %+v", podinfo, err)
			}
			newRes := PodResources{
				PodInfo: podinfo,
				Resources: []ResourceItem{
					{
						ID:   eniMultiIP.GetResourceID(),
						Type: eniMultiIP.GetType(),
					},
				},
				NetNs: func(s string) *string {
					return &s
				}(r.Netns),
			}
			networkContext.resources = append(networkContext.resources, newRes.Resources...)
			if networkService.eipResMgr != nil && podinfo.EipInfo.PodEip {
				podinfo.PodIPs = eniMultiIP.SecondaryIP
				var eipRes *types.EIP
				eipRes, err = networkService.allocateEIP(networkContext, &oldRes)
				if err != nil {
					return nil, fmt.Errorf("error get allocated eip for: %+v, result: %+v", podinfo, err)
				}
				eipResItem := ResourceItem{
					Type: eipRes.GetType(),
					ID:   eipRes.GetResourceID(),
					ExtraEipInfo: &ExtraEipInfo{
						Delete:         eipRes.Delete,
						AssociateENI:   eipRes.AssociateENI,
						AssociateENIIP: eipRes.AssociateENIIP,
					},
				}
				newRes.Resources = append(newRes.Resources, eipResItem)
				networkContext.resources = append(networkContext.resources, eipResItem)
			}
			err = networkService.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
			if err != nil {
				return nil, errors.Wrapf(err, "error put resource into store")
			}
		}
		allocIPReply.IPType = rpc.IPType_TypeENIMultiIP
		allocIPReply.Success = true
		allocIPReply.NetworkInfo = &rpc.AllocIPReply_ENIMultiIP{
			ENIMultiIP: &rpc.ENIMultiIP{
				ENIConfig: &rpc.ENI{
					PodIP:     eniMultiIP.SecondaryIP.ToRPC(),
					Subnet:    eniMultiIP.ENI.VSwitchCIDR.ToRPC(),
					MAC:       eniMultiIP.ENI.MAC,
					GatewayIP: eniMultiIP.ENI.GatewayIP.ToRPC(),
					Trunk:     podinfo.PodENI && networkService.enableTrunk && eniMultiIP.ENI.Trunk,
				},
				PodConfig: &rpc.Pod{
					Ingress: podinfo.TcIngress,
					Egress:  podinfo.TcEgress,
				},
				ServiceCIDR: networkService.k8s.GetServiceCIDR().ToRPC(),
			},
		}
	case podNetworkTypeVPCENI:
		var eni *types.ENI
		eni, err = networkService.allocateENI(networkContext, &oldRes)
		if err != nil {
			return nil, fmt.Errorf("error get allocated vpc ENI ip for: %+v, result: %+v", podinfo, err)
		}
		newRes := PodResources{
			PodInfo: podinfo,
			Resources: []ResourceItem{
				{
					ID:   eni.GetResourceID(),
					Type: eni.GetType(),
				},
			},
			NetNs: func(s string) *string {
				return &s
			}(r.Netns),
		}
		networkContext.resources = append(networkContext.resources, newRes.Resources...)
		if networkService.eipResMgr != nil && podinfo.EipInfo.PodEip {
			podinfo.PodIPs = eni.PrimaryIP
			var eipRes *types.EIP
			eipRes, err = networkService.allocateEIP(networkContext, &oldRes)
			if err != nil {
				return nil, fmt.Errorf("error get allocated eip for: %+v, result: %+v", podinfo, err)
			}
			newRes.Resources = append(newRes.Resources, ResourceItem{
				Type: eipRes.GetType(),
				ID:   eipRes.GetResourceID(),
				ExtraEipInfo: &ExtraEipInfo{
					Delete:         eipRes.Delete,
					AssociateENI:   eipRes.AssociateENI,
					AssociateENIIP: eipRes.AssociateENIIP,
				},
			})
			eipResItem := ResourceItem{
				Type: eipRes.GetType(),
				ID:   eipRes.GetResourceID(),
			}
			newRes.Resources = append(newRes.Resources, eipResItem)
			networkContext.resources = append(networkContext.resources, eipResItem)
		}
		err = networkService.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
		if err != nil {
			return nil, errors.Wrapf(err, "error put resource into store")
		}
		allocIPReply.IPType = rpc.IPType_TypeVPCENI
		allocIPReply.Success = true
		allocIPReply.NetworkInfo = &rpc.AllocIPReply_VPCENI{
			VPCENI: &rpc.VPCENI{
				ENIConfig: &rpc.ENI{
					PodIP:     eni.PrimaryIP.ToRPC(),
					Subnet:    eni.VSwitchCIDR.ToRPC(),
					MAC:       eni.MAC,
					GatewayIP: eni.GatewayIP.ToRPC(),
				},
				PodConfig: &rpc.Pod{
					Ingress: podinfo.TcIngress,
					Egress:  podinfo.TcEgress,
				},
				ServiceCIDR: networkService.k8s.GetServiceCIDR().ToRPC(),
			},
		}
	case podNetworkTypeVPCIP:
		var vpcVeth *types.Veth
		vpcVeth, err = networkService.allocateVeth(networkContext, &oldRes)
		if err != nil {
			return nil, fmt.Errorf("error get allocated vpc ip for: %+v, result: %+v", podinfo, err)
		}
		newRes := PodResources{
			PodInfo: podinfo,
			Resources: []ResourceItem{
				{
					ID:   vpcVeth.GetResourceID(),
					Type: vpcVeth.GetType(),
				},
			},
			NetNs: func(s string) *string {
				return &s
			}(r.Netns),
		}
		networkContext.resources = append(networkContext.resources, newRes.Resources...)
		err = networkService.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
		if err != nil {
			return nil, errors.Wrapf(err, "error put resource into store")
		}
		allocIPReply.IPType = rpc.IPType_TypeVPCIP
		allocIPReply.Success = true
		allocIPReply.NetworkInfo = &rpc.AllocIPReply_VPCIP{
			VPCIP: &rpc.VPCIP{
				PodConfig: &rpc.Pod{
					Ingress: podinfo.TcIngress,
					Egress:  podinfo.TcEgress,
				},
				NodeCidr: networkService.k8s.GetNodeCidr().String(),
			},
		}

	default:
		return nil, fmt.Errorf("not support pod network type")
	}

	// 4. grpc connection
	if ctx.Err() != nil {
		err = ctx.Err()
		return nil, errors.Wrapf(err, "error on grpc connection")
	}

	// 5. return allocate result
	return allocIPReply, err
}

func (networkService *networkService) ReleaseIP(ctx context.Context, r *rpc.ReleaseIPRequest) (*rpc.ReleaseIPReply, error) {
	log.Infof("release ip request: %+v", r)
	networkService.RLock()
	defer networkService.RUnlock()
	var (
		start = time.Now()
		err   error
	)
	defer func() {
		metric.RPCLatency.WithLabelValues("ReleaseIP", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	}()

	// 0. Get pod Info
	podinfo, err := networkService.k8s.GetPod(r.K8SPodNamespace, r.K8SPodName)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pod info for: %+v", r)
	}

	// 1. Init Context
	networkContext := &networkContext{
		Context:    ctx,
		resources:  []ResourceItem{},
		pod:        podinfo,
		k8sService: networkService.k8s,
	}
	releaseReply := &rpc.ReleaseIPReply{
		Success: true,
	}
	if podinfo.PodENI {
		return releaseReply, nil
	}
	defer func() {
		if err != nil {
			networkContext.Log().Errorf("release result with error, %+v", err)
		} else {
			networkContext.Log().Infof("release result: %+v", releaseReply)
		}
	}()

	oldRes, err := networkService.getPodResource(podinfo)
	if err != nil {
		return nil, err
	}

	if !networkService.verifyPodNetworkType(podinfo.PodNetworkType) {
		networkContext.Log().Warnf("unexpect pod network type release, maybe daemon mode changed: %+v", podinfo.PodNetworkType)
		return releaseReply, nil
	}

	for _, res := range oldRes.Resources {
		//record old resource for pod
		networkContext.resources = append(networkContext.resources, res)
		mgr := networkService.getResourceManagerForRes(res.Type)
		if mgr == nil {
			networkContext.Log().Warnf("error cleanup allocated network resource %s, %s: %v", res.ID, res.Type, err)
			continue
		}
		if podinfo.IPStickTime == 0 {
			if err = mgr.Release(networkContext, res); err != nil && err != pool.ErrInvalidState {
				return nil, errors.Wrapf(err, "error release request network resource for: %+v", r)
			}
			if err = networkService.deletePodResource(podinfo); err != nil {
				return nil, errors.Wrapf(err, "error delete resource from db: %+v", r)
			}
		}
	}

	if networkContext.Err() != nil {
		err = ctx.Err()
		return nil, errors.Wrapf(err, "error on grpc connection")
	}

	return releaseReply, nil
}

func (networkService *networkService) GetIPInfo(ctx context.Context, r *rpc.GetInfoRequest) (*rpc.GetInfoReply, error) {
	log.Infof("GetIPInfo request: %+v", r)
	// 0. Get pod Info
	podinfo, err := networkService.k8s.GetPod(r.K8SPodNamespace, r.K8SPodName)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pod info for: %+v", r)
	}

	if !networkService.verifyPodNetworkType(podinfo.PodNetworkType) {
		return nil, fmt.Errorf("unexpect pod network type get info, maybe daemon mode changed: %+v", podinfo.PodNetworkType)
	}

	// 1. Init Context
	networkContext := &networkContext{
		Context:    ctx,
		resources:  []ResourceItem{},
		pod:        podinfo,
		k8sService: networkService.k8s,
	}

	var getIPInfoResult *rpc.GetInfoReply

	defer func() {
		networkContext.Log().Infof("getIpInfo result: %+v", getIPInfoResult)
	}()

	networkService.RLock()
	podRes, err := networkService.getPodResource(podinfo)
	networkService.RUnlock()
	if err != nil {
		networkContext.Log().Errorf("failed to get pod info : %+v", err)
		return getIPInfoResult, err
	}

	// 2. return network info for pod
	switch podinfo.PodNetworkType {
	case podNetworkTypeENIMultiIP:
		var reply *rpc.GetInfoReply_ENIMultiIP
		resItems := podRes.GetResourceItemByType(types.ResourceTypeENIIP)
		if len(resItems) > 0 {
			// only have one
			res, err := networkService.eniIPResMgr.Stat(networkContext, resItems[0].ID)
			if err == nil {
				eniMultiIP := res.(*types.ENIIP)
				reply = &rpc.GetInfoReply_ENIMultiIP{
					ENIMultiIP: &rpc.ENIMultiIP{
						ENIConfig: &rpc.ENI{
							PodIP:     eniMultiIP.SecondaryIP.ToRPC(),
							Subnet:    eniMultiIP.ENI.VSwitchCIDR.ToRPC(),
							MAC:       eniMultiIP.ENI.MAC,
							GatewayIP: eniMultiIP.ENI.GatewayIP.ToRPC(),
						},
						PodConfig: &rpc.Pod{
							Ingress: podinfo.TcIngress,
							Egress:  podinfo.TcEgress,
						},
						ServiceCIDR: networkService.k8s.GetServiceCIDR().ToRPC(),
					},
				}
			} else {
				log.Debugf("failed to get res stat %s", resItems[0].ID)
			}
		}
		getIPInfoResult = &rpc.GetInfoReply{
			IPType: rpc.IPType_TypeENIMultiIP,
			PodConfig: &rpc.Pod{
				Ingress: podinfo.TcIngress,
				Egress:  podinfo.TcEgress,
			},
			PodIP:       podinfo.PodIPs.ToRPC(),
			NetworkInfo: reply,
		}
		return getIPInfoResult, nil
	case podNetworkTypeVPCIP:
		getIPInfoResult = &rpc.GetInfoReply{
			IPType: rpc.IPType_TypeVPCIP,
			PodConfig: &rpc.Pod{
				Ingress: podinfo.TcIngress,
				Egress:  podinfo.TcEgress,
			},
			NodeCidr: networkService.k8s.GetNodeCidr().String(),
		}
		return getIPInfoResult, nil
	case podNetworkTypeVPCENI:
		var reply *rpc.GetInfoReply_VPCENI
		resItems := podRes.GetResourceItemByType(types.ResourceTypeENI)
		if len(resItems) > 0 {
			// only have one
			res, err := networkService.eniResMgr.Stat(networkContext, resItems[0].ID)
			if err == nil {
				eni := res.(*types.ENI)
				reply = &rpc.GetInfoReply_VPCENI{
					VPCENI: &rpc.VPCENI{
						ENIConfig: &rpc.ENI{
							PodIP:     eni.PrimaryIP.ToRPC(),
							Subnet:    eni.VSwitchCIDR.ToRPC(),
							MAC:       eni.MAC,
							GatewayIP: eni.GatewayIP.ToRPC(),
						},
						PodConfig: &rpc.Pod{
							Ingress: podinfo.TcIngress,
							Egress:  podinfo.TcEgress,
						},
						ServiceCIDR: networkService.k8s.GetServiceCIDR().ToRPC(),
					},
				}
			} else {
				log.Debugf("failed to get res stat %s", resItems[0].ID)
			}
		}
		getIPInfoResult = &rpc.GetInfoReply{
			IPType: rpc.IPType_TypeVPCENI,
			PodConfig: &rpc.Pod{
				Ingress: podinfo.TcIngress,
				Egress:  podinfo.TcEgress,
			},
			NetworkInfo: reply,
		}
		return getIPInfoResult, nil
	default:
		return getIPInfoResult, errors.Errorf("unknown or unsupport network type for: %v", r)
	}
}

func (networkService *networkService) RecordEvent(_ context.Context, r *rpc.EventRequest) (*rpc.EventReply, error) {
	eventType := eventTypeNormal
	if r.EventType == rpc.EventType_EventTypeWarning {
		eventType = eventTypeWarning
	}

	reply := &rpc.EventReply{
		Succeed: true,
		Error:   "",
	}

	if r.EventTarget == rpc.EventTarget_EventTargetNode { // Node
		networkService.k8s.RecordNodeEvent(eventType, r.Reason, r.Message)
		return reply, nil
	}

	// Pod
	err := networkService.k8s.RecordPodEvent(r.K8SPodName, r.K8SPodNamespace, eventType, r.Reason, r.Message)
	if err != nil {
		reply.Succeed = false
		reply.Error = err.Error()

		return reply, err
	}

	return reply, nil
}

func (networkService *networkService) verifyPodNetworkType(podNetworkMode string) bool {
	return (networkService.daemonMode == daemonModeVPC && //vpc
		(podNetworkMode == podNetworkTypeVPCENI || podNetworkMode == podNetworkTypeVPCIP)) ||
		// eni-multi-ip
		(networkService.daemonMode == daemonModeENIMultiIP && podNetworkMode == podNetworkTypeENIMultiIP) ||
		// eni-only
		(networkService.daemonMode == daemonModeENIOnly && podNetworkMode == podNetworkTypeVPCENI)
}

func (networkService *networkService) startGarbageCollectionLoop() {
	// period do network resource gc
	gcTicker := time.NewTicker(gcPeriod)
	go func() {
		for range gcTicker.C {
			log.Debugf("do resource gc on node")
			networkService.Lock()
			pods, err := networkService.k8s.GetLocalPods()
			if err != nil {
				log.Warnf("error get local pods for gc")
				networkService.Unlock()
				continue
			}
			podKeyMap := make(map[string]bool)

			for _, pod := range pods {
				if !pod.SandboxExited {
					podKeyMap[podInfoKey(pod.Namespace, pod.Name)] = true
				}
			}

			var (
				inUseSet         = make(map[string]map[string]ResourceItem)
				expireSet        = make(map[string]map[string]ResourceItem)
				relateExpireList = make([]string, 0)
			)

			resRelateList, err := networkService.resourceDB.List()
			if err != nil {
				log.Warnf("error list resource db for gc")
				networkService.Unlock()
				continue
			}

			for _, resRelateObj := range resRelateList {
				resRelate := resRelateObj.(PodResources)
				_, podExist := podKeyMap[podInfoKey(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name)]
				if !podExist {
					if resRelate.PodInfo.IPStickTime != 0 {
						// delay resource garbage collection for sticky ip
						resRelate.PodInfo.IPStickTime = 0
						if err = networkService.resourceDB.Put(podInfoKey(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name),
							resRelate); err != nil {
							log.Warnf("error store pod info to resource db")
						}
						podExist = true
					} else {
						relateExpireList = append(relateExpireList, podInfoKey(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name))
					}
				}
				for _, res := range resRelate.Resources {
					if _, ok := inUseSet[res.Type]; !ok {
						inUseSet[res.Type] = make(map[string]ResourceItem)
						expireSet[res.Type] = make(map[string]ResourceItem)
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
				mgr, ok := networkService.mgrForResource[mgrType]
				if ok {
					log.Debugf("start garbage collection for %v, list: %+v， %+v", mgrType, inUseSet[mgrType], expireSet[mgrType])
					err = mgr.GarbageCollection(inUseSet[mgrType], expireSet[mgrType])
					if err != nil {
						log.Warnf("error do garbage collection for %+v, inuse: %v, expire: %v, err: %v", mgrType, inUseSet[mgrType], expireSet[mgrType], err)
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
							log.Debugf("skip gc res id %s", resID)
							continue
						}
						log.Debugf("checking ip %s", list[1])
						_, addr, err := net.ParseCIDR(fmt.Sprintf("%s/32", list[1]))
						if err != nil {
							log.Errorf("failed parse ip %s", list[1])
							return
						}
						// try clean all
						err = link.DeleteIPRulesByIP(addr)
						if err != nil {
							log.Errorf("failed release ip rules %v", err)
						}
						err = link.DeleteRouteByIP(addr)
						if err != nil {
							log.Errorf("failed delete route %v", err)
						}
					}
				}()

				for _, relate := range relateExpireList {
					err = networkService.resourceDB.Delete(relate)
					if err != nil {
						log.Warnf("error delete resource db relation: %v", err)
					}
				}
			}
			networkService.Unlock()
		}
	}()
}

func (networkService *networkService) startPeriodCheck() {
	// check pool
	func() {
		log.Debugf("compare poll with metadata")
		podMapping, err := networkService.GetResourceMapping()
		if err != nil {
			log.Error(err)
			return
		}
		for _, res := range podMapping {
			if res.Valid {
				continue
			}
			if res.Name == "" || res.Namespace == "" {
				// just log
				log.Warnf("found resource invalid %s %s", res.LocalResID, res.RemoteResID)
			} else {
				_ = tracing.RecordPodEvent(res.Name, res.Namespace, corev1.EventTypeWarning, "ResourceInvalid", fmt.Sprintf("resource %s", res.LocalResID))
			}
		}
	}()
	// call CNI CHECK, make sure all dev is ok
	func() {
		log.Debugf("call CNI CHECK")
		defer func() {
			log.Debugf("call CNI CHECK end")
		}()
		networkService.RLock()
		podResList, err := networkService.resourceDB.List()
		networkService.RUnlock()
		if err != nil {
			log.Error(err)
			return
		}
		ff, err := ioutil.ReadFile(terwayCNIConf)
		if err != nil {
			log.Error(err)
			return
		}
		for _, v := range podResList {
			res := v.(PodResources)
			if res.NetNs == nil {
				continue
			}
			log.Debugf("checking pod name %s", res.PodInfo.Name)
			cniCfg := libcni.NewCNIConfig([]string{networkService.cniBinPath}, nil)
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), cniExecTimeout)
				defer cancel()
				err := cniCfg.CheckNetwork(ctx, &libcni.NetworkConfig{
					Network: &containertypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					Bytes: ff,
				}, &libcni.RuntimeConf{
					ContainerID: "fake", // must provide
					NetNS:       filepath.Join("/proc/1/root/", *res.NetNs),
					IfName:      "eth0",
					Args: [][2]string{
						{"K8S_POD_NAME", res.PodInfo.Name},
						{"K8S_POD_NAMESPACE", res.PodInfo.Namespace},
					},
				})
				if err != nil {
					log.Error(err)
					return
				}
			}()
		}
	}()
}

// tracing
func (networkService *networkService) Config() []tracing.MapKeyValueEntry {
	// name, daemon_mode, configFilePath, kubeconfig, master
	config := []tracing.MapKeyValueEntry{
		{Key: tracingKeyName, Value: networkServiceName}, // use a unique name?
		{Key: tracingKeyDaemonMode, Value: networkService.daemonMode},
		{Key: tracingKeyConfigFilePath, Value: networkService.configFilePath},
		{Key: tracingKeyKubeConfig, Value: networkService.kubeConfig},
		{Key: tracingKeyMaster, Value: networkService.master},
	}

	return config
}

func (networkService *networkService) Trace() []tracing.MapKeyValueEntry {
	count := 0
	networkService.pendingPods.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	trace := []tracing.MapKeyValueEntry{
		{Key: tracingKeyPendingPodsCount, Value: fmt.Sprint(count)},
	}
	resList, err := networkService.resourceDB.List()
	if err != nil {
		trace = append(trace, tracing.MapKeyValueEntry{Key: "error", Value: err.Error()})
		return trace
	}

	for _, v := range resList {
		res := v.(PodResources)

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

func (networkService *networkService) Execute(cmd string, _ []string, message chan<- string) {
	switch cmd {
	case commandMapping:
		mapping, err := networkService.GetResourceMapping()
		message <- fmt.Sprintf("mapping: %v, err: %s\n", mapping, err)
	default:
		message <- "can't recognize command\n"
	}

	close(message)
}

func (networkService *networkService) GetResourceMapping() ([]*tracing.PodMapping, error) {
	var poolStats tracing.ResourcePoolStats
	var err error

	networkService.RLock()
	// get []ResourceMapping
	switch networkService.daemonMode {
	case daemonModeENIMultiIP:
		poolStats, err = networkService.eniIPResMgr.GetResourceMapping()
	case daemonModeVPC:
		networkService.RUnlock()
		return nil, nil
	case daemonModeENIOnly:
		poolStats, err = networkService.eniResMgr.GetResourceMapping()
	}
	if err != nil {
		networkService.RUnlock()
		return nil, err
	}
	// pod related res
	pods, err := networkService.resourceDB.List()
	networkService.RUnlock()
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
		p := pod.(PodResources)
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

func newNetworkService(configFilePath, kubeconfig, master, daemonMode string) (rpc.TerwayBackendServer, error) {
	log.Debugf("start network service with: %s, %s", configFilePath, daemonMode)
	cniBinPath := os.Getenv("CNI_PATH")
	if cniBinPath == "" {
		cniBinPath = cniDefaultPath
	}
	netSrv := &networkService{
		configFilePath: configFilePath,
		kubeConfig:     kubeconfig,
		master:         master,
		pendingPods:    sync.Map{},
		cniBinPath:     cniBinPath,
	}
	if daemonMode == daemonModeENIMultiIP || daemonMode == daemonModeVPC || daemonMode == daemonModeENIOnly {
		netSrv.daemonMode = daemonMode
	} else {
		return nil, fmt.Errorf("unsupport daemon mode")
	}

	var err error

	netSrv.k8s, err = newK8S(master, kubeconfig, daemonMode)
	if err != nil {
		return nil, errors.Wrapf(err, "error init k8s service")
	}

	// load dynamic config
	dynamicCfg, nodeLabel, err := getDynamicConfig(netSrv.k8s)
	if err != nil {
		log.Warnf("get dynamic config error: %s. fallback to default config", err.Error())
		dynamicCfg = ""
	}

	config, err := types.GetConfigFromFileWithMerge(configFilePath, []byte(dynamicCfg))
	if err != nil {
		return nil, fmt.Errorf("failed parse config: %v", err)
	}

	if len(dynamicCfg) == 0 {
		log.Infof("got config: %+v from: %+v", config, configFilePath)
	} else {
		log.Infof("got config: %+v from %+v, with dynamic config %+v", config, configFilePath, nodeLabel)
	}

	if err := validateConfig(config); err != nil {
		return nil, err
	}

	if err := setDefault(config); err != nil {
		return nil, err
	}

	ins := aliyun.GetInstanceMeta()

	ignoreLinkNotExist := false
	if daemonMode == daemonModeENIOnly || daemonMode == daemonModeVPC {
		ignoreLinkNotExist = true
	}
	ipFamily := types.NewIPFamilyFromIPStack(types.IPStack(config.IPStack))
	ecs, err := aliyun.NewECS(config.AccessID, config.AccessSecret, config.CredentialPath, config.EnableENITrunking, ignoreLinkNotExist, ins.VPCID, ins.RegionID, ipFamily)
	if err != nil {
		return nil, errors.Wrapf(err, "error get aliyun client")
	}

	netSrv.enableTrunk = config.EnableENITrunking

	ipNetSet := &types.IPNetSet{}
	if config.ServiceCIDR != "" {
		cidrs := strings.Split(config.ServiceCIDR, ",")

		for _, cidr := range cidrs {
			_, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return nil, fmt.Errorf("error parse service CIDR, %w", err)
			}
			if terwayIP.IPv6(ipNet.IP) {
				ipNetSet.IPv6 = ipNet
			} else {
				ipNetSet.IPv4 = ipNet
			}
		}
	}

	err = netSrv.k8s.SetSvcCidr(ipNetSet)
	if err != nil {
		return nil, errors.Wrapf(err, "error set k8s svcCidr")
	}

	netSrv.resourceDB, err = storage.NewDiskStorage(
		resDBName, resDBPath, json.Marshal, func(bytes []byte) (interface{}, error) {
			resourceRel := &PodResources{}
			err = json.Unmarshal(bytes, resourceRel)
			if err != nil {
				return nil, errors.Wrapf(err, "error unmarshal pod relate resource")
			}
			return *resourceRel, nil
		})
	if err != nil {
		return nil, errors.Wrapf(err, "error init resource manager storage")
	}

	// get pool config
	poolConfig, err := getPoolConfig(config, ecs)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pool config")
	}
	log.Infof("init pool config: %+v", poolConfig)

	err = restoreLocalENIRes(ecs, poolConfig, netSrv.k8s, netSrv.resourceDB)
	if err != nil {
		return nil, errors.Wrapf(err, "error restore local eni resources")
	}

	localResource := make(map[string][]resourceManagerInitItem)
	resObjList, err := netSrv.resourceDB.List()
	if err != nil {
		return nil, errors.Wrapf(err, "error list resource relation db")
	}
	for _, resObj := range resObjList {
		podRes := resObj.(PodResources)
		for _, res := range podRes.Resources {
			if localResource[res.Type] == nil {
				localResource[res.Type] = make([]resourceManagerInitItem, 0)
			}
			localResource[res.Type] = append(localResource[res.Type], resourceManagerInitItem{resourceID: res.ID, podInfo: podRes.PodInfo})
		}
	}
	resStr, err := json.Marshal(localResource)
	if err != nil {
		return nil, err
	}
	log.Debugf("local resources to restore: %s", resStr)

	switch daemonMode {
	case daemonModeVPC:
		//init ENI
		netSrv.eniResMgr, err = newENIResourceManager(poolConfig, ecs, localResource[types.ResourceTypeENI])
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
		if config.EnableEIPPool == conditionTrue {
			netSrv.eipResMgr = newEipResourceManager(ecs, netSrv.k8s, config.AllowEIPRob == conditionTrue)
		}
		netSrv.mgrForResource = map[string]ResourceManager{
			types.ResourceTypeENIIP: netSrv.eniIPResMgr,
			types.ResourceTypeEIP:   netSrv.eipResMgr,
		}
	case daemonModeENIOnly:
		//init eni
		netSrv.eniResMgr, err = newENIResourceManager(poolConfig, ecs, localResource[types.ResourceTypeENI])
		if err != nil {
			return nil, errors.Wrapf(err, "error init eni resource manager")
		}
		if config.EnableEIPPool == conditionTrue {
			netSrv.eipResMgr = newEipResourceManager(ecs, netSrv.k8s, config.AllowEIPRob == conditionTrue)
		}
		netSrv.mgrForResource = map[string]ResourceManager{
			types.ResourceTypeENI: netSrv.eniResMgr,
			types.ResourceTypeEIP: netSrv.eipResMgr,
		}
	default:
		panic("unsupported daemon mode" + daemonMode)
	}

	//start gc loop
	netSrv.startGarbageCollectionLoop()
	period := poolCheckPeriod
	periodCfg := os.Getenv("POOL_CHECK_PERIOD_SECONDS")
	periodSeconds, err := strconv.Atoi(periodCfg)
	if err == nil {
		period = time.Duration(periodSeconds) * time.Second
	}

	go wait.JitterUntil(netSrv.startPeriodCheck, period, 1, true, wait.NeverStop)

	// register for tracing
	_ = tracing.Register(tracing.ResourceTypeNetworkService, "default", netSrv)
	tracing.RegisterResourceMapping(netSrv)
	tracing.RegisterEventRecorder(netSrv.k8s.RecordNodeEvent, netSrv.k8s.RecordPodEvent)

	return netSrv, nil
}

// restore local eni resources for old terway migration
func restoreLocalENIRes(ecs aliyun.ECS, pc *types.PoolConfig, k8s Kubernetes, resourceDB storage.Storage) error {
	resList, err := resourceDB.List()
	if err != nil {
		return errors.Wrapf(err, "error list resourceDB storage")
	}
	if len(resList) != 0 {
		log.Debugf("skip restore for upgraded")
		return nil
	}

	eniList, err := ecs.GetAttachedENIs(false)
	if err != nil {
		return errors.Wrapf(err, "error get attached eni for restore")
	}
	ipEniMap := map[string]*types.ENI{}
	for _, eni := range eniList {
		ipEniMap[eni.PrimaryIP.IPv4.String()] = eni
	}

	podList, err := k8s.GetLocalPods()
	if err != nil {
		return errors.Wrapf(err, "error get local pod for restore")
	}
	for _, pod := range podList {
		if pod.PodNetworkType != podNetworkTypeVPCENI {
			continue
		}
		log.Debugf("restore for local pod: %+v, enis: %+v", pod, ipEniMap)
		eni, ok := ipEniMap[pod.PodIPs.IPv4.String()]
		if ok {
			err = resourceDB.Put(podInfoKey(pod.Namespace, pod.Name), PodResources{
				PodInfo: pod,
				Resources: []ResourceItem{
					{
						ID:   eni.GetResourceID(),
						Type: eni.GetType(),
					},
				},
			})
			if err != nil {
				return errors.Wrapf(err, "error put resource into store")
			}
		} else {
			log.Warnf("error found pod relate eni, pod: %+v", pod)
		}
	}
	return nil
}

//setup default value
func setDefault(cfg *types.Configure) error {
	if cfg.EniCapRatio == 0 {
		cfg.EniCapRatio = 1
	}

	if cfg.HotPlug == "" {
		cfg.HotPlug = conditionTrue
	}

	if cfg.HotPlug == conditionFalse || cfg.HotPlug == "0" {
		cfg.HotPlug = conditionFalse
	}

	// Default policy for vswitch selection is random.
	if cfg.VSwitchSelectionPolicy == "" {
		cfg.VSwitchSelectionPolicy = types.VSwitchSelectionPolicyRandom
	}

	if cfg.IPStack == "" {
		cfg.IPStack = string(types.IPStackIPv4)
	}
	return nil
}

func validateConfig(cfg *types.Configure) error {
	switch cfg.IPStack {
	case "", string(types.IPStackIPv4), string(types.IPStackDual):
	default:
		return fmt.Errorf("unsupported ipStack %s in configMap", cfg.IPStack)
	}

	return nil
}

func getPoolConfig(cfg *types.Configure, ecs aliyun.ECS) (*types.PoolConfig, error) {
	poolConfig := &types.PoolConfig{
		MaxPoolSize:            cfg.MaxPoolSize,
		MinPoolSize:            cfg.MinPoolSize,
		MaxENI:                 cfg.MaxENI,
		MinENI:                 cfg.MinENI,
		AccessID:               cfg.AccessID,
		AccessSecret:           cfg.AccessSecret,
		HotPlug:                cfg.HotPlug == conditionTrue,
		EniCapRatio:            cfg.EniCapRatio,
		EniCapShift:            cfg.EniCapShift,
		SecurityGroup:          cfg.SecurityGroup,
		VSwitchSelectionPolicy: cfg.VSwitchSelectionPolicy,
		EnableENITrunking:      cfg.EnableENITrunking,
	}
	ins := aliyun.GetInstanceMeta()
	zone := ins.ZoneID
	if cfg.VSwitches != nil {
		zoneVswitchs, ok := cfg.VSwitches[zone]
		if ok && len(zoneVswitchs) > 0 {
			poolConfig.VSwitch = cfg.VSwitches[zone]
		}
	}
	if len(poolConfig.VSwitch) == 0 {
		poolConfig.VSwitch = []string{ins.VSwitchID}
	}
	poolConfig.ENITags = cfg.ENITags
	poolConfig.VPC = ins.VPCID
	poolConfig.InstanceID = ins.InstanceID

	return poolConfig, nil
}
