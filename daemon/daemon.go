package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/eni"
	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/pkg/k8s"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	gcPeriod = 5 * time.Minute

	networkServiceName       = "default"
	tracingKeyName           = "name"
	tracingKeyDaemonMode     = "daemon_mode"
	tracingKeyConfigFilePath = "config_file_path"

	tracingKeyPendingPodsCount = "pending_pods_count"

	commandMapping = "mapping"
	commandResDB   = "resdb"

	IfEth0 = "eth0"

	envEFLO = "eflo"
)

type networkService struct {
	daemonMode     string
	configFilePath string

	k8s        k8s.Kubernetes
	resourceDB storage.Storage

	eniMgr      *eni.Manager
	pendingPods sync.Map
	sync.RWMutex

	enableIPv4, enableIPv6 bool

	ipamType types.IPAMType

	wg sync.WaitGroup

	gcRulesOnce sync.Once

	rpc.UnimplementedTerwayBackendServer
}

var serviceLog = logf.Log.WithName("server")

var _ rpc.TerwayBackendServer = (*networkService)(nil)

// return resource relation in db, or return nil.
func (n *networkService) getPodResource(info *daemon.PodInfo) (daemon.PodResources, error) {
	obj, err := n.resourceDB.Get(utils.PodInfoKey(info.Namespace, info.Name))
	if err == nil {
		return obj.(daemon.PodResources), nil
	}
	if errors.Is(err, storage.ErrNotFound) {
		return daemon.PodResources{}, nil
	}

	return daemon.PodResources{}, err
}

func (n *networkService) deletePodResource(info *daemon.PodInfo) error {
	key := utils.PodInfoKey(info.Namespace, info.Name)
	return n.resourceDB.Delete(key)
}

func (n *networkService) AllocIP(ctx context.Context, r *rpc.AllocIPRequest) (*rpc.AllocIPReply, error) {
	podID := utils.PodInfoKey(r.K8SPodNamespace, r.K8SPodName)
	l := logf.FromContext(ctx)
	l.Info("alloc ip req")

	_, exist := n.pendingPods.LoadOrStore(podID, struct{}{})
	if exist {
		return nil, &types.Error{
			Code: types.ErrPodIsProcessing,
			Msg:  fmt.Sprintf("Pod %s request is processing", podID),
		}
	}
	defer func() {
		n.pendingPods.Delete(podID)
	}()

	n.RLock()
	defer n.RUnlock()
	var (
		start = time.Now()
		err   error
	)

	reply := &rpc.AllocIPReply{
		Success: true,
		IPv4:    n.enableIPv4,
		IPv6:    n.enableIPv6,
	}

	defer func() {
		metric.RPCLatency.WithLabelValues("AllocIP", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

		l.Info("alloc ip", "reply", fmt.Sprintf("%v", reply), "err", fmt.Sprintf("%v", err))
	}()

	// 0. Get pod Info
	pod, err := n.k8s.GetPod(ctx, r.K8SPodNamespace, r.K8SPodName, true)
	if err != nil {
		return nil, &types.Error{
			Code: types.ErrInvalidArgsErrCode,
			Msg:  err.Error(),
			R:    err,
		}
	}

	// 1. Init Context

	cni := &daemon.CNI{
		PodName:      r.K8SPodName,
		PodNamespace: r.K8SPodNamespace,
		PodID:        podID,
		PodUID:       pod.PodUID,
		NetNSPath:    r.Netns,
	}

	// 2. Find old resource info
	oldRes, err := n.getPodResource(pod)
	if err != nil {
		return nil, &types.Error{
			Code: types.ErrInternalError,
			Msg:  err.Error(),
			R:    err,
		}
	}

	if !n.verifyPodNetworkType(pod.PodNetworkType) {
		return nil, &types.Error{
			Code: types.ErrInvalidArgsErrCode,
			Msg:  "Unexpected network type, maybe daemon mode changed",
		}
	}

	var resourceRequests []eni.ResourceRequest

	var netConf []*rpc.NetConf
	// 3. Allocate network resource for pod
	switch pod.PodNetworkType {
	case daemon.PodNetworkTypeENIMultiIP:
		reply.IPType = rpc.IPType_TypeENIMultiIP

		if pod.PodENI {
			resourceRequests = append(resourceRequests, &eni.RemoteIPRequest{})
		} else {
			req := &eni.LocalIPRequest{}
			if pod.ERdma {
				req.LocalIPType = eni.LocalIPTypeERDMA
			}
			if len(oldRes.GetResourceItemByType(daemon.ResourceTypeENIIP)) == 1 {
				old := oldRes.GetResourceItemByType(daemon.ResourceTypeENIIP)[0]

				setRequest(req, old)
			}

			resourceRequests = append(resourceRequests, req)
		}
	case daemon.PodNetworkTypeVPCENI:
		reply.IPType = rpc.IPType_TypeVPCENI

		if pod.PodENI || n.ipamType == types.IPAMTypeCRD {
			resourceRequests = append(resourceRequests, &eni.RemoteIPRequest{})
		} else {
			req := &eni.LocalIPRequest{}

			if len(oldRes.GetResourceItemByType(daemon.ResourceTypeENI)) == 1 {
				old := oldRes.GetResourceItemByType(daemon.ResourceTypeENI)[0]

				setRequest(req, old)
			}
			resourceRequests = append(resourceRequests, req)
		}
	case daemon.PodNetworkTypeVPCIP:
		reply.IPType = rpc.IPType_TypeVPCIP
		resourceRequests = append(resourceRequests, &eni.VethRequest{})
	default:
		return nil, &types.Error{
			Code: types.ErrInternalError,
			Msg:  "Unknown pod network type",
		}
	}

	var networkResource []daemon.ResourceItem

	resp, err := n.eniMgr.Allocate(ctx, cni, &eni.AllocRequest{
		ResourceRequests: resourceRequests,
	})
	if err != nil {
		_ = n.eniMgr.Release(ctx, cni, &eni.ReleaseRequest{
			NetworkResources: resp,
		})
		return nil, err
	}

	for _, res := range resp {
		netConf = append(netConf, res.ToRPC()...)
		networkResource = append(networkResource, res.ToStore()...)
	}

	for _, c := range netConf {
		if c.BasicInfo == nil {
			c.BasicInfo = &rpc.BasicInfo{}
		}
		c.BasicInfo.ServiceCIDR = n.k8s.GetServiceCIDR().ToRPC()
		if pod.PodNetworkType == daemon.PodNetworkTypeVPCIP {
			c.BasicInfo.PodCIDR = n.k8s.GetNodeCidr().ToRPC()
		}
		c.Pod = &rpc.Pod{
			Ingress:         pod.TcIngress,
			Egress:          pod.TcEgress,
			NetworkPriority: pod.NetworkPriority,
		}
	}

	err = defaultForNetConf(netConf)
	if err != nil {
		return nil, err
	}

	out, err := json.Marshal(netConf)
	if err != nil {
		return nil, &types.Error{
			Code: types.ErrInternalError,
			R:    err,
		}
	}

	ips := getPodIPs(netConf)
	if len(ips) > 0 {
		_ = n.k8s.PatchPodIPInfo(pod, strings.Join(ips, ","))
	}

	// 4. Record resource info
	newRes := daemon.PodResources{
		PodInfo:     pod,
		Resources:   networkResource,
		NetNs:       &r.Netns,
		ContainerID: &r.K8SPodInfraContainerId,
		NetConf:     string(out),
	}

	err = n.resourceDB.Put(podID, newRes)
	if err != nil {
		return nil, err
	}

	reply.NetConfs = netConf
	reply.Success = true

	return reply, nil
}

func (n *networkService) ReleaseIP(ctx context.Context, r *rpc.ReleaseIPRequest) (*rpc.ReleaseIPReply, error) {
	podID := utils.PodInfoKey(r.K8SPodNamespace, r.K8SPodName)
	l := logf.FromContext(ctx)
	l.Info("release ip req")

	_, exist := n.pendingPods.LoadOrStore(podID, struct{}{})
	if exist {
		return nil, &types.Error{
			Code: types.ErrPodIsProcessing,
			Msg:  fmt.Sprintf("Pod %s request is processing", podID),
		}
	}
	defer func() {
		n.pendingPods.Delete(podID)
	}()

	n.RLock()
	defer n.RUnlock()
	var (
		start = time.Now()
		err   error
	)

	reply := &rpc.ReleaseIPReply{
		Success: true,
		IPv4:    n.enableIPv4,
		IPv6:    n.enableIPv6,
	}

	defer func() {
		metric.RPCLatency.WithLabelValues("ReleaseIP", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		l.Info("release ip", "reply", fmt.Sprintf("%v", reply), "err", fmt.Sprintf("%v", err))
	}()

	// 0. Get pod Info
	pod, err := n.k8s.GetPod(ctx, r.K8SPodNamespace, r.K8SPodName, true)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reply, nil
		}
		return nil, err
	}

	cni := &daemon.CNI{
		PodName:      r.K8SPodName,
		PodNamespace: r.K8SPodNamespace,
		PodID:        podID,
		PodUID:       pod.PodUID,
	}

	// 1. Init Context

	oldRes, err := n.getPodResource(pod)
	if err != nil {
		return nil, err
	}

	if !n.verifyPodNetworkType(pod.PodNetworkType) {
		return nil, fmt.Errorf("unexpect pod network type allocate, maybe daemon mode changed: %+v", pod.PodNetworkType)
	}

	if oldRes.ContainerID != nil {
		if r.K8SPodInfraContainerId != *oldRes.ContainerID {
			l.Info("cni request not match stored resource, ignored", "old", *oldRes.ContainerID)
			return reply, nil
		}
	}
	if pod.IPStickTime == 0 {
		for _, resource := range oldRes.Resources {
			res := parseNetworkResource(resource)
			if res == nil {
				continue
			}

			err = n.eniMgr.Release(ctx, cni, &eni.ReleaseRequest{
				NetworkResources: []eni.NetworkResource{res},
			})
			if err != nil {
				return nil, err
			}
		}
		err = n.deletePodResource(pod)
		if err != nil {
			return nil, fmt.Errorf("error delete pod resource: %w", err)
		}
	}

	return reply, nil
}

func (n *networkService) GetIPInfo(ctx context.Context, r *rpc.GetInfoRequest) (*rpc.GetInfoReply, error) {
	podID := utils.PodInfoKey(r.K8SPodNamespace, r.K8SPodName)
	log := logf.FromContext(ctx)
	log.Info("get ip req")

	_, exist := n.pendingPods.LoadOrStore(podID, struct{}{})
	if exist {
		return nil, &types.Error{
			Code: types.ErrPodIsProcessing,
			Msg:  fmt.Sprintf("Pod %s request is processing", podID),
		}
	}
	defer func() {
		n.pendingPods.Delete(podID)
	}()

	n.RLock()
	defer n.RUnlock()

	var err error

	// 0. Get pod Info
	pod, err := n.k8s.GetPod(ctx, r.K8SPodNamespace, r.K8SPodName, true)
	if err != nil {
		return nil, &types.Error{
			Code: types.ErrInvalidArgsErrCode,
			Msg:  err.Error(),
			R:    err,
		}
	}

	// 1. Init Context
	reply := &rpc.GetInfoReply{
		Success: true,
		IPv4:    n.enableIPv4,
		IPv6:    n.enableIPv6,
	}

	switch pod.PodNetworkType {
	case daemon.PodNetworkTypeENIMultiIP:
		reply.IPType = rpc.IPType_TypeENIMultiIP
	case daemon.PodNetworkTypeVPCIP:
		reply.IPType = rpc.IPType_TypeVPCIP
	case daemon.PodNetworkTypeVPCENI:
		reply.IPType = rpc.IPType_TypeVPCENI

	default:
		return nil, &types.Error{
			Code: types.ErrInternalError,
			Msg:  "Unknown pod network type",
		}
	}

	// 2. Find old resource info
	oldRes, err := n.getPodResource(pod)
	if err != nil {
		return nil, &types.Error{
			Code: types.ErrInternalError,
			Msg:  err.Error(),
			R:    err,
		}
	}

	if !n.verifyPodNetworkType(pod.PodNetworkType) {
		return nil, &types.Error{
			Code: types.ErrInvalidArgsErrCode,
			Msg:  "Unexpected network type, maybe daemon mode changed",
		}
	}
	if oldRes.ContainerID != nil {
		if r.K8SPodInfraContainerId != *oldRes.ContainerID {
			log.Info("cni request not match stored resource, ignored", "old", *oldRes.ContainerID)
			return reply, nil
		}
	}
	netConf := make([]*rpc.NetConf, 0)

	err = json.Unmarshal([]byte(oldRes.NetConf), &netConf)
	if err != nil {
		// ignore for not found
	} else {
		reply.NetConfs = netConf
	}

	reply.Success = true

	log.Info("get info reply", "reply", reply)
	return reply, nil
}

func (n *networkService) RecordEvent(_ context.Context, r *rpc.EventRequest) (*rpc.EventReply, error) {
	eventType := corev1.EventTypeNormal
	if r.EventType == rpc.EventType_EventTypeWarning {
		eventType = corev1.EventTypeWarning
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
	return (n.daemonMode == daemon.ModeVPC && //vpc
		(podNetworkMode == daemon.PodNetworkTypeVPCENI || podNetworkMode == daemon.PodNetworkTypeVPCIP)) ||
		// eni-multi-ip
		(n.daemonMode == daemon.ModeENIMultiIP && podNetworkMode == daemon.PodNetworkTypeENIMultiIP) ||
		// eni-only
		(n.daemonMode == daemon.ModeENIOnly && podNetworkMode == daemon.PodNetworkTypeVPCENI)
}

func (n *networkService) startGarbageCollectionLoop(ctx context.Context) {
	_ = wait.PollUntilContextCancel(ctx, gcPeriod, true, func(ctx context.Context) (done bool, err error) {
		err = n.gcPods(ctx)
		if err != nil {
			serviceLog.Error(err, "error garbage collection")
		}
		return false, nil
	})
}

func (n *networkService) gcPods(ctx context.Context) error {
	n.Lock()
	defer n.Unlock()

	pods, err := n.k8s.GetLocalPods()
	if err != nil {
		return err
	}
	exist := make(map[string]bool)

	existIPs := sets.Set[string]{}

	for _, pod := range pods {
		if !pod.SandboxExited {
			exist[utils.PodInfoKey(pod.Namespace, pod.Name)] = true
			if pod.PodIPs.IPv4 != nil {
				existIPs.Insert(pod.PodIPs.IPv4.String())
			}
		}
	}

	objList, err := n.resourceDB.List()
	if err != nil {
		return err
	}
	podResources := getPodResources(objList)

	for _, podRes := range podResources {
		if podRes.PodInfo != nil {
			if podRes.PodInfo.PodIPs.IPv4 != nil {
				existIPs.Insert(podRes.PodInfo.PodIPs.IPv4.String())
			}
		}

		podID := utils.PodInfoKey(podRes.PodInfo.Namespace, podRes.PodInfo.Name)
		if _, ok := exist[podID]; ok {
			continue
		}
		// check kube-api again
		ok, err := n.k8s.PodExist(podRes.PodInfo.Namespace, podRes.PodInfo.Name)
		if err != nil || ok {
			continue
		}

		// that is old logic ... keep it
		if podRes.PodInfo.IPStickTime != 0 {
			podRes.PodInfo.IPStickTime = 0

			err = n.resourceDB.Put(podID, podRes)
			if err != nil {
				return err
			}
			continue
		}

		for _, resource := range podRes.Resources {
			res := parseNetworkResource(resource)
			if res == nil {
				continue
			}
			err = n.eniMgr.Release(ctx, &daemon.CNI{
				PodName:      podRes.PodInfo.Name,
				PodNamespace: podRes.PodInfo.Namespace,
				PodID:        podID,
				PodUID:       podRes.PodInfo.PodUID,
			}, &eni.ReleaseRequest{
				NetworkResources: []eni.NetworkResource{res},
			})
			if err != nil {
				return err
			}
		}

		err = n.deletePodResource(podRes.PodInfo)
		if err != nil {
			return err
		}
		serviceLog.Info("removed pod", "pod", podID)
	}

	if os.Getenv("TERWAY_GC_RULES") == "true" {
		n.gcRulesOnce.Do(func() {
			gcLeakedRules(existIPs)
		})
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
		res := v.(daemon.PodResources)

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
	case commandResDB:
		n.RLock()
		defer n.RUnlock()
		objList, err := n.resourceDB.List()
		if err != nil {
			message <- fmt.Sprintf("%s\n", err)
		} else {
			out, _ := json.Marshal(objList)
			message <- string(out)
		}

	default:
		message <- "can't recognize command\n"
	}

	close(message)
}

func (n *networkService) GetResourceMapping() ([]*rpc.ResourceMapping, error) {
	var mapping []*rpc.ResourceMapping
	for _, status := range n.eniMgr.Status() {
		mapping = append(mapping, toRPCMapping(status))
	}
	return mapping, nil
}

func newNetworkService(ctx context.Context, configFilePath, daemonMode string) (*networkService, error) {
	serviceLog.Info("start network service", "config", configFilePath, "daemonMode", daemonMode)

	globalConfig, err := daemon.GetConfigFromFileWithMerge(configFilePath, nil)
	if err != nil {
		return nil, err
	}

	if daemonMode == daemon.ModeENIMultiIP && globalConfig.IPAMType == types.IPAMTypeCRD {
		// current the logic is for eniip
		return newCRDV2Service(ctx, configFilePath, daemonMode)
	} else {
		return newLegacyService(ctx, configFilePath, daemonMode)
	}
}

func checkInstance(limit *client.Limits, daemonMode string, config *daemon.Config) (bool, bool) {
	var enableIPv4, enableIPv6 bool
	switch config.IPStack {
	case "ipv4":
		enableIPv4 = true
	case "dual":
		enableIPv4 = true
		enableIPv6 = true
	case "ipv6":
		enableIPv6 = true
	}

	if enableIPv6 {
		if !limit.SupportIPv6() {
			enableIPv6 = false
			serviceLog.Info("instance is not support ipv6")
		} else if daemonMode == daemon.ModeENIMultiIP && !limit.SupportMultiIPIPv6() {
			enableIPv6 = false
			serviceLog.Info("instance is not support multi ipv6")
		}
	}

	if config.EnableENITrunking && limit.TrunkPod() <= 0 {
		config.EnableENITrunking = false
		serviceLog.Info("instance is not support trunk")
	}

	if config.EnableERDMA {
		if limit.ERDMARes() <= 0 {
			config.EnableERDMA = false
			serviceLog.Info("instance is not support erdma")
		} else {
			ok := nodecap.GetNodeCapabilities(nodecap.NodeCapabilityERDMA)
			if ok == "" {
				config.EnableERDMA = false
				serviceLog.Info("os is not support erdma")
			}
		}
	}
	return enableIPv4, enableIPv6
}

// initTrunk to ensure trunk eni is present. Return eni id if found.
func initTrunk(config *daemon.Config, poolConfig *types.PoolConfig, k8sClient k8s.Kubernetes, f factory.Factory) (string, error) {
	var err error

	// get eni id form node annotation
	preferTrunkID := k8sClient.GetTrunkID()

	if config.WaitTrunkENI {
		// at this mode , we retreat id ONLY by node annotation
		if preferTrunkID == "" {
			preferTrunkID, err = k8sClient.WaitTrunkReady()
			if err != nil {
				return "", fmt.Errorf("error wait trunk ready, %w", err)
			}
		}
		return preferTrunkID, nil
	}

	// already exclude the primary eni
	enis, err := f.GetAttachedNetworkInterface(preferTrunkID)
	if err != nil {
		return "", fmt.Errorf("error get attached eni, %w", err)
	}

	// get attached trunk eni
	preferred := lo.Filter(enis, func(ni *daemon.ENI, idx int) bool { return ni.Trunk && ni.ID == preferTrunkID })
	if len(preferred) > 0 {
		// found the eni
		trunk := preferred[0]
		if trunk.ERdma {
			serviceLog.Info("erdma eni on trunk mode, disable erdma")
			config.EnableERDMA = false
		}

		return trunk.ID, nil
	}

	// choose one
	attachedTrunk := lo.Filter(enis, func(ni *daemon.ENI, idx int) bool { return ni.Trunk })
	if len(attachedTrunk) > 0 {
		trunk := attachedTrunk[0]
		if trunk.ERdma {
			serviceLog.Info("erdma eni on trunk mode, disable erdma")
			config.EnableERDMA = false
		}

		return trunk.ID, nil
	}

	// we have to create one if possible
	if poolConfig.MaxENI <= len(enis) {
		config.EnableENITrunking = false
		return "", nil
	}

	v6 := 0
	if poolConfig.EnableIPv6 {
		v6 = 1
	}
	trunk, _, _, err := f.CreateNetworkInterface(1, v6, "trunk")
	if err != nil {
		if trunk != nil {
			_ = f.DeleteNetworkInterface(trunk.ID)
		}

		return "", fmt.Errorf("error create trunk eni, %w", err)
	}

	return trunk.ID, nil
}

func runDevicePlugin(daemonMode string, config *daemon.Config, poolConfig *types.PoolConfig) {
	switch daemonMode {
	case daemon.ModeVPC, daemon.ModeENIOnly:
		dp := deviceplugin.NewENIDevicePlugin(poolConfig.MaxENI, deviceplugin.ENITypeENI)
		go dp.Serve()
	case daemon.ModeENIMultiIP:
		if config.EnableENITrunking {
			dp := deviceplugin.NewENIDevicePlugin(poolConfig.MaxMemberENI, deviceplugin.ENITypeMember)
			go dp.Serve()
		}
	}

	if config.EnableERDMA {
		if !config.DisableDevicePlugin {
			res := deviceplugin.ENITypeERDMA
			capacity := poolConfig.ERdmaCapacity
			if capacity > 0 {
				dp := deviceplugin.NewENIDevicePlugin(capacity, res)
				go dp.Serve()
			}
		}
	}
}

func getPodResources(list []interface{}) []daemon.PodResources {
	var res []daemon.PodResources
	for _, resObj := range list {
		res = append(res, resObj.(daemon.PodResources))
	}
	return res
}

func parseNetworkResource(item daemon.ResourceItem) eni.NetworkResource {
	switch item.Type {
	case daemon.ResourceTypeENIIP, daemon.ResourceTypeENI:
		var v4, v6 netip.Addr
		if item.IPv4 != "" {
			v4, _ = netip.ParseAddr(item.IPv4)
		}
		if item.IPv6 != "" {
			v6, _ = netip.ParseAddr(item.IPv6)
		}

		return &eni.LocalIPResource{
			ENI: daemon.ENI{
				ID:  item.ENIID,
				MAC: item.ENIMAC,
			},
			IP: types.IPSet2{
				IPv4: v4,
				IPv6: v6,
			},
		}
	}
	return nil
}

func extractIPs(old daemon.ResourceItem) (ipv4, ipv6 netip.Addr, eniID string) {
	ipv4, _ = netip.ParseAddr(old.IPv4)
	ipv6, _ = netip.ParseAddr(old.IPv6)
	eniID = old.ENIID
	return ipv4, ipv6, eniID
}

func setRequest(req *eni.LocalIPRequest, old daemon.ResourceItem) {
	ipv4, ipv6, eniID := extractIPs(old)
	req.IPv4 = ipv4
	req.IPv6 = ipv6
	req.NetworkInterfaceID = eniID
}

func toRPCMapping(res eni.Status) *rpc.ResourceMapping {
	rMapping := rpc.ResourceMapping{
		NetworkInterfaceID:   res.NetworkInterfaceID,
		MAC:                  res.MAC,
		Type:                 res.Type,
		AllocInhibitExpireAt: res.AllocInhibitExpireAt,
		Status:               res.Status,
	}

	for _, v := range res.Usage {
		rMapping.Info = append(rMapping.Info, strings.Join(v, "  "))
	}

	return &rMapping
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

func getPodIPs(netConfs []*rpc.NetConf) []string {
	var ips []string
	for _, netConf := range netConfs {
		if !defaultIf(netConf.IfName) {
			continue
		}
		if netConf.BasicInfo == nil || netConf.BasicInfo.PodIP == nil {
			continue
		}
		if netConf.BasicInfo.PodIP.IPv4 != "" {
			ips = append(ips, netConf.BasicInfo.PodIP.IPv4)
		}
		if netConf.BasicInfo.PodIP.IPv6 != "" {
			ips = append(ips, netConf.BasicInfo.PodIP.IPv6)
		}
	}
	return ips
}

func filterENINotFound(podResources []daemon.PodResources, attachedENIID map[string]*daemon.ENI) []daemon.PodResources {
	for i := range podResources {
		for j := 0; j < len(podResources[i].Resources); j++ {
			if podResources[i].Resources[j].Type == daemon.ResourceTypeENI ||
				podResources[i].Resources[j].Type == daemon.ResourceTypeENIIP {

				eniID := podResources[i].Resources[j].ENIID
				if eniID == "" {
					list := strings.SplitN(podResources[i].Resources[j].ID, ".", 2)
					if len(list) == 0 {
						continue
					}
					mac := list[0]

					found := false
					for _, eni := range attachedENIID {
						if eni.MAC == mac {
							// found
							found = true
							break
						}
					}
					if !found {
						podResources[i].Resources = append(podResources[i].Resources[:j], podResources[i].Resources[j+1:]...)
					}
				} else {
					if _, ok := attachedENIID[eniID]; !ok {
						podResources[i].Resources = append(podResources[i].Resources[:j], podResources[i].Resources[j+1:]...)
					}
				}
			}
		}
	}
	return podResources
}
