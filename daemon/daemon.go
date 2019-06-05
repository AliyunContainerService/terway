package daemon

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	daemonModeVPC        = "VPC"
	daemonModeENIMultiIP = "ENIMultiIP"
	daemonModeENIOnly    = "ENIOnly"

	gcPeriod = 5 * time.Minute

	conditionFalse = "false"
)

type networkService struct {
	daemonMode  string
	k8s         Kubernetes
	resourceDB  storage.Storage
	vethResMgr  ResourceManager
	eniResMgr   ResourceManager
	eniIPResMgr ResourceManager
	//networkResourceMgr ResourceManager
	mgrForResource map[string]ResourceManager
	sync.RWMutex
}

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
	oldVethRes := old.GetResourceItemByType(types.ResourceTypeENIIP)
	oldVethID := ""
	if old.PodInfo != nil {
		if len(oldVethRes) == 0 {
			ctx.Log().Debugf("eniip for pod %s is zero", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else if len(oldVethRes) > 1 {
			ctx.Log().Warnf("eniip for pod %s more than one", podInfoKey(old.PodInfo.Namespace, old.PodInfo.Name))
		} else {
			oldVethID = oldVethRes[0].ID
		}
	}

	res, err := networkService.eniIPResMgr.Allocate(ctx, oldVethID)
	if err != nil {
		return nil, err
	}
	return res.(*types.ENIIP), nil
}

func (networkService *networkService) AllocIP(grpcContext context.Context, r *rpc.AllocIPRequest) (*rpc.AllocIPReply, error) {
	log.Infof("alloc ip request: %+v", r)
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
		Context:    grpcContext,
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
				mgr := networkService.getResourceManagerForRes(res.Type)
				if mgr == nil {
					networkContext.Log().Warnf("error cleanup allocated network resource %s, %s: %v", res.ID, res.Type, err)
					continue
				}
				mgr.Release(networkContext, res.ID)
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
		}

		err = networkService.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
		if err != nil {
			return nil, errors.Wrapf(err, "error put resource into store")
		}

		allocIPReply.IPType = rpc.IPType_TypeENIMultiIP
		allocIPReply.Success = true
		allocIPReply.NetworkInfo = &rpc.AllocIPReply_ENIMultiIP{
			ENIMultiIP: &rpc.ENIMultiIP{
				EniConfig: &rpc.ENI{
					IPv4Addr:        eniMultiIP.SecAddress.String(),
					IPv4Subnet:      eniMultiIP.Eni.Address.String(),
					MacAddr:         eniMultiIP.Eni.MAC,
					Gateway:         eniMultiIP.Eni.Gateway.String(),
					DeviceNumber:    eniMultiIP.Eni.DeviceNumber,
					PrimaryIPv4Addr: eniMultiIP.PrimaryIP.String(),
				},
				PodConfig: &rpc.Pod{
					Ingress: podinfo.TcIngress,
					Egress:  podinfo.TcEgress,
				},
				ServiceCidr: networkService.k8s.GetServiceCidr().String(),
			},
		}
	case podNetworkTypeVPCENI:
		var vpcEni *types.ENI
		vpcEni, err = networkService.allocateENI(networkContext, &oldRes)
		if err != nil {
			return nil, fmt.Errorf("error get allocated vpc ENI ip for: %+v, result: %+v", podinfo, err)
		}
		newRes := PodResources{
			PodInfo: podinfo,
			Resources: []ResourceItem{
				{
					ID:   vpcEni.GetResourceID(),
					Type: vpcEni.GetType(),
				},
			},
		}

		err = networkService.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
		if err != nil {
			return nil, errors.Wrapf(err, "error put resource into store")
		}
		allocIPReply.IPType = rpc.IPType_TypeVPCENI
		allocIPReply.Success = true
		allocIPReply.NetworkInfo = &rpc.AllocIPReply_VpcEni{
			VpcEni: &rpc.VPCENI{
				EniConfig: &rpc.ENI{
					IPv4Addr:        vpcEni.Address.IP.String(),
					IPv4Subnet:      vpcEni.Address.String(),
					MacAddr:         vpcEni.MAC,
					Gateway:         vpcEni.Gateway.String(),
					DeviceNumber:    vpcEni.DeviceNumber,
					PrimaryIPv4Addr: vpcEni.Address.IP.String(),
				},
				PodConfig: &rpc.Pod{
					Ingress: podinfo.TcIngress,
					Egress:  podinfo.TcEgress,
				},
				ServiceCidr: networkService.k8s.GetServiceCidr().String(),
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
		}

		err = networkService.resourceDB.Put(podInfoKey(podinfo.Namespace, podinfo.Name), newRes)
		if err != nil {
			return nil, errors.Wrapf(err, "error put resource into store")
		}
		allocIPReply.IPType = rpc.IPType_TypeVPCIP
		allocIPReply.Success = true
		allocIPReply.NetworkInfo = &rpc.AllocIPReply_VpcIp{
			VpcIp: &rpc.VPCIP{
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

	// 3. grpc connection
	if grpcContext.Err() != nil {
		err = grpcContext.Err()
		return nil, errors.Wrapf(err, "error on grpc connection")
	}

	// 4. return allocate result
	return allocIPReply, err
}

func (networkService *networkService) ReleaseIP(grpcContext context.Context, r *rpc.ReleaseIPRequest) (*rpc.ReleaseIPReply, error) {
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
		Context:    grpcContext,
		resources:  []ResourceItem{},
		pod:        podinfo,
		k8sService: networkService.k8s,
	}
	releaseReply := &rpc.ReleaseIPReply{
		Success: true,
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
		if err = mgr.Release(networkContext, res.ID); err != nil && err != pool.ErrInvalidState {
			return nil, errors.Wrapf(err, "error release request network resource for: %+v", r)
		}

		if err = networkService.deletePodResource(podinfo); err != nil {
			return nil, errors.Wrapf(err, "error delete resource from db: %+v", r)
		}
	}

	if networkContext.Err() != nil {
		err = grpcContext.Err()
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

	// 2. return network info for pod
	switch podinfo.PodNetworkType {
	case podNetworkTypeENIMultiIP:
		getIPInfoResult = &rpc.GetInfoReply{
			IPType: rpc.IPType_TypeENIMultiIP,
			PodConfig: &rpc.Pod{
				Ingress: podinfo.TcIngress,
				Egress:  podinfo.TcEgress,
			},
			PodIP: podinfo.PodIP,
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
		getIPInfoResult = &rpc.GetInfoReply{
			IPType: rpc.IPType_TypeVPCENI,
			PodConfig: &rpc.Pod{
				Ingress: podinfo.TcIngress,
				Egress:  podinfo.TcEgress,
			},
		}
		return getIPInfoResult, nil
	default:
		return getIPInfoResult, errors.Errorf("unknown or unsupport network type for: %v", r)
	}
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
				podKeyMap[podInfoKey(pod.Namespace, pod.Name)] = true
			}

			var (
				inUseSet         = make(map[string]map[string]interface{})
				expireSet        = make(map[string]map[string]interface{})
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
					relateExpireList = append(relateExpireList, podInfoKey(resRelate.PodInfo.Namespace, resRelate.PodInfo.Name))
				}
				for _, res := range resRelate.Resources {
					if _, ok := inUseSet[res.Type]; !ok {
						inUseSet[res.Type] = make(map[string]interface{}, 0)
						expireSet[res.Type] = make(map[string]interface{}, 0)
					}
					// already in use by others
					if _, ok := inUseSet[res.Type][res.ID]; ok {
						continue
					}
					if podExist {
						inUseSet[res.Type][res.ID] = struct{}{}
					} else {
						expireSet[res.Type][res.ID] = struct{}{}
					}
				}
			}
			gcDone := true
			for mgrType := range inUseSet {
				mgr, ok := networkService.mgrForResource[mgrType]
				if ok {
					err = mgr.GarbageCollection(inUseSet[mgrType], expireSet[mgrType])
					if err != nil {
						log.Warnf("error do garbage collection for %+v, inuse: %v, expire: %v, err: %v", mgrType, inUseSet, expireSet, err)
						gcDone = false
					}
				}
			}
			if gcDone {
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

func newNetworkService(configFilePath, kubeconfig, master, daemonMode string) (rpc.TerwayBackendServer, error) {
	log.Debugf("start network service with: %s, %s", configFilePath, daemonMode)
	netSrv := &networkService{}
	if daemonMode == daemonModeENIMultiIP || daemonMode == daemonModeVPC || daemonMode == daemonModeENIOnly {
		netSrv.daemonMode = daemonMode
	} else {
		return nil, fmt.Errorf("unsupport daemon mode")
	}

	config := &types.Configure{}

	f, err := os.Open(configFilePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed open config file")
	}

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed read file %s: %v", configFilePath, err)
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed parse config: %v", err)
	}

	log.Infof("got config: %+v from: %+v", config, configFilePath)

	if err := validateConfig(config); err != nil {
		return nil, err
	}

	if err := setDefault(config); err != nil {
		return nil, err
	}

	regionID, err := aliyun.GetLocalRegion()
	if err != nil {
		return nil, errors.Wrapf(err, "error get region-id")
	}

	ecs, err := aliyun.NewECS(config.AccessID, config.AccessSecret, regionID)
	if err != nil {
		return nil, errors.Wrapf(err, "error get region-id")
	}

	k8sRestConfig, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		return nil, err
	}
	k8sClient, err := kubernetes.NewForConfig(k8sRestConfig)
	if err != nil {
		return nil, err
	}

	var ipnet *net.IPNet
	if config.ServiceCIDR != "" {
		_, ipnet, err = net.ParseCIDR(config.ServiceCIDR)
		if err != nil {
			return nil, errors.Wrapf(err, "error parse service cidr: %s", config.ServiceCIDR)
		}
	}

	netSrv.k8s, err = newK8S(k8sClient, ipnet, daemonMode)
	if err != nil {
		return nil, errors.Wrapf(err, "error init k8s service")
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

	// get pool config
	poolConfig, err := getPoolConfig(config, ecs)
	if err != nil {
		return nil, errors.Wrapf(err, "error get pool config")
	}
	log.Infof("init pool config: %+v", poolConfig)

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
		netSrv.eniIPResMgr, err = newENIIPResourceManager(poolConfig, ecs, localResource[types.ResourceTypeENIIP])
		if err != nil {
			return nil, errors.Wrapf(err, "error init ENI ip resource manager")
		}
		netSrv.mgrForResource = map[string]ResourceManager{
			types.ResourceTypeENIIP: netSrv.eniIPResMgr,
		}
	case daemonModeENIOnly:
		//init eni
		netSrv.eniResMgr, err = newENIResourceManager(poolConfig, ecs, localResource[types.ResourceTypeENI])
		if err != nil {
			return nil, errors.Wrapf(err, "error init eni resource manager")
		}
		netSrv.mgrForResource = map[string]ResourceManager{
			types.ResourceTypeENI: netSrv.eniResMgr,
		}
	default:
		panic("unsupported daemon mode" + daemonMode)
	}

	//start gc loop
	netSrv.startGarbageCollectionLoop()

	return netSrv, nil
}

//setup default value
func setDefault(cfg *types.Configure) error {
	if cfg.EniCapRatio == 0 {
		cfg.EniCapRatio = 1
	}

	if cfg.HotPlug == "" {
		cfg.HotPlug = "true"
	}

	if cfg.HotPlug == conditionFalse || cfg.HotPlug == "0" {
		cfg.HotPlug = conditionFalse
	}

	return nil
}

func validateConfig(cfg *types.Configure) error {
	return nil
}

func getPoolConfig(cfg *types.Configure, ecs aliyun.ECS) (*types.PoolConfig, error) {
	poolConfig := &types.PoolConfig{
		MaxPoolSize:   cfg.MaxPoolSize,
		MinPoolSize:   cfg.MinPoolSize,
		AccessID:      cfg.AccessID,
		AccessSecret:  cfg.AccessSecret,
		HotPlug:       cfg.HotPlug == "true",
		EniCapRatio:   cfg.EniCapRatio,
		EniCapShift:   cfg.EniCapShift,
		SecurityGroup: cfg.SecurityGroup,
	}

	zone, err := aliyun.GetLocalZone()
	if err != nil {
		return nil, err
	}
	if cfg.VSwitches != nil {
		zoneVswitchs, ok := cfg.VSwitches[zone]
		if ok && len(zoneVswitchs) > 0 {
			poolConfig.VSwitch = cfg.VSwitches[zone]
		}
	}
	if len(poolConfig.VSwitch) == 0 {
		vSwitch, err := aliyun.GetLocalVswitch()
		if err != nil {
			return nil, err
		}
		poolConfig.VSwitch = []string{vSwitch}
	}

	if poolConfig.Region, err = aliyun.GetLocalRegion(); err != nil {
		return nil, err
	}

	if poolConfig.VPC, err = aliyun.GetLocalVPC(); err != nil {
		return nil, err
	}

	if poolConfig.InstanceID, err = aliyun.GetLocalInstanceID(); err != nil {
		return nil, err
	}

	return poolConfig, nil
}
