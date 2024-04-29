package daemon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/sets"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	eni2 "github.com/AliyunContainerService/terway/pkg/aliyun/eni"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	"github.com/AliyunContainerService/terway/pkg/apis/alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/apis/crds"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/eni"
	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/pkg/factory/aliyun"
	"github.com/AliyunContainerService/terway/pkg/k8s"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/retry"
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

	netConf := make([]*rpc.NetConf, 0)

	err = json.Unmarshal([]byte(oldRes.NetConf), &netConf)
	if err != nil {
		// ignore for not found
	} else {
		reply.NetConfs = netConf
	}

	reply.Success = true

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

func gcLeakedRules(existIP sets.Set[string]) {
	links, err := netlink.LinkList()
	if err != nil {
		serviceLog.Error(err, "error list links")
		return
	}

	ipvlLinks := lo.Filter(links, func(item netlink.Link, index int) bool {
		_, ok := item.(*netlink.IPVlan)
		return ok
	})

	gcRoutes(ipvlLinks, existIP)

	normalLinks := lo.Filter(links, func(item netlink.Link, index int) bool {
		_, ok := item.(*netlink.Device)
		return ok
	})

	gcTCFilters(normalLinks, existIP)
}

func (n *networkService) migrateEIP(ctx context.Context, objs []interface{}) error {
	once := sync.Once{}

	for _, resObj := range objs {
		podRes, ok := resObj.(daemon.PodResources)
		if !ok {
			continue
		}
		if podRes.PodInfo == nil || !podRes.PodInfo.EipInfo.PodEip {
			continue
		}
		for _, eipRes := range podRes.Resources {
			if eipRes.Type != daemon.ResourceTypeEIP {
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
				err = crds.CreateOrUpdateCRD(ctx, n.k8s.GetClient(), crds.CRDPodEIP)
			})
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)

			//l := serviceLog.WithField("name", fmt.Sprintf("%s/%s", podRes.PodInfo.Namespace, podRes.PodInfo.Name))

			c := n.k8s.GetClient()
			podEIP := &v1beta1.PodEIP{}
			err = c.Get(ctx, k8stypes.NamespacedName{Namespace: podRes.PodInfo.Namespace, Name: podRes.PodInfo.Name}, podEIP)
			if err == nil {
				cancel()
				//l.Info("skip create podEIP, already exist")
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

				//l.Infof("create podEIP for %v", podRes)

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

	netSrv := &networkService{
		configFilePath: configFilePath,
		pendingPods:    sync.Map{},
	}
	if daemonMode == daemon.ModeENIMultiIP || daemonMode == daemon.ModeVPC || daemonMode == daemon.ModeENIOnly {
		netSrv.daemonMode = daemonMode
	} else {
		return nil, fmt.Errorf("unsupport daemon mode")
	}

	var err error

	globalConfig, err := daemon.GetConfigFromFileWithMerge(configFilePath, nil)
	if err != nil {
		return nil, err
	}

	netSrv.k8s, err = k8s.NewK8S(daemonMode, globalConfig)
	if err != nil {
		return nil, fmt.Errorf("error init k8s: %w", err)
	}

	// load dynamic config
	dynamicCfg, _, err := getDynamicConfig(ctx, netSrv.k8s)
	if err != nil {
		//serviceLog.Warnf("get dynamic config error: %s. fallback to default config", err.Error())
		dynamicCfg = ""
	}

	config, err := daemon.GetConfigFromFileWithMerge(configFilePath, []byte(dynamicCfg))
	if err != nil {
		return nil, fmt.Errorf("failed parse config: %v", err)
	}

	config.Populate()
	err = config.Validate()
	if err != nil {
		return nil, err
	}

	serviceLog.Info("got config", "config", fmt.Sprintf("%+v", config))

	backoff.OverrideBackoff(config.BackoffOverride)
	_ = netSrv.k8s.SetCustomStatefulWorkloadKinds(config.CustomStatefulWorkloadKinds)
	netSrv.ipamType = config.IPAMType

	if os.Getenv("TERWAY_DEPLOY_ENV") == envEFLO {
		instance.SetPopulateFunc(instance.EfloPopulate)
		client.SetGetLimit(client.EfloGetLimit)
	}

	meta := instance.GetInstanceMeta()

	var (
		trunkENIID      = ""
		nodeAnnotations = map[string]string{}
	)

	var providers []credential.Interface
	if string(config.AccessID) != "" && string(config.AccessSecret) != "" {
		providers = append(providers, credential.NewAKPairProvider(string(config.AccessID), string(config.AccessSecret)))
	}
	providers = append(providers, credential.NewEncryptedCredentialProvider(utils.NormalizePath(config.CredentialPath), "", ""))
	providers = append(providers, credential.NewMetadataProvider())

	clientSet, err := credential.NewClientMgr(meta.RegionID, providers...)
	if err != nil {
		return nil, err
	}

	aliyunClient, err := client.New(clientSet,
		flowcontrol.NewTokenBucketRateLimiter(8, 10),
		flowcontrol.NewTokenBucketRateLimiter(4, 5))
	if err != nil {
		return nil, err
	}

	instanceType := meta.InstanceType
	if os.Getenv("TERWAY_DEPLOY_ENV") == envEFLO {
		instanceType = meta.InstanceID
	}
	limit, err := client.GetLimit(aliyunClient, instanceType)
	if err != nil {
		return nil, fmt.Errorf("upable get instance limit, %w", err)
	}

	enableIPv4, enableIPv6 := checkInstance(limit, daemonMode, config)

	netSrv.enableIPv4 = enableIPv4
	netSrv.enableIPv6 = enableIPv6

	eniConfig := getENIConfig(config)
	eniConfig.EnableIPv4 = enableIPv4
	eniConfig.EnableIPv6 = enableIPv6

	// fall back to use primary eni's sg
	if len(eniConfig.SecurityGroupIDs) == 0 {
		enis, err := aliyunClient.DescribeNetworkInterface(ctx, "", nil, eniConfig.InstanceID, "Primary", "", nil)
		if err != nil {
			return nil, err
		}
		if len(enis) == 0 {
			return nil, fmt.Errorf("no primary eni found")
		}
		eniConfig.SecurityGroupIDs = enis[0].SecurityGroupIDs
	}

	// get pool config
	poolConfig, err := getPoolConfig(config, daemonMode, limit)
	if err != nil {
		return nil, err
	}

	poolConfig.EnableIPv4 = enableIPv4
	poolConfig.EnableIPv6 = enableIPv6

	serviceLog.Info("pool config", "pool", fmt.Sprintf("%+v", poolConfig))

	vswPool, err := vswpool.NewSwitchPool(100, "10m")
	if err != nil {
		return nil, fmt.Errorf("error init vsw pool, %w", err)
	}

	var factory factory.Factory
	if os.Getenv("TERWAY_DEPLOY_ENV") == envEFLO {
		factory = aliyun.NewEflo(ctx, aliyunClient, vswPool, eniConfig)
	} else {
		factory = aliyun.NewAliyun(ctx, aliyunClient, eni2.NewENIMetadata(enableIPv4, enableIPv6), vswPool, eniConfig)
	}

	if config.EnableENITrunking {
		trunkENIID, err = initTrunk(config, poolConfig, netSrv.k8s, factory)
		if err != nil {
			return nil, err
		}
		if trunkENIID == "" {
			serviceLog.Info("no trunk eni found, fallback to non-trunk mode")
		} else {
			nodeAnnotations[types.TrunkOn] = trunkENIID
			nodeAnnotations[string(types.MemberENIIPTypeIPs)] = strconv.Itoa(poolConfig.MaxMemberENI)
		}
	}

	if daemonMode != daemon.ModeVPC {
		nodeAnnotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(poolConfig.Capacity)
	}

	attached, err := factory.GetAttachedNetworkInterface(trunkENIID)
	if err != nil {
		return nil, err
	}

	realRdmaCount := limit.ERDMARes()
	if config.EnableERDMA && len(attached) >= limit.Adapters-1-limit.ERdmaAdapters {
		attachedERdma := lo.Filter(attached, func(ni *daemon.ENI, idx int) bool { return ni.ERdma })
		if len(attachedERdma) <= 0 {
			// turn off only when no one use it
			serviceLog.Info(fmt.Sprintf("node has no enough free eni slot to attach more erdma to achieve erdma res: %d", limit.ERDMARes()))
			config.EnableERDMA = false
		}
		// reset the cap to the actual using
		realRdmaCount = min(realRdmaCount, len(attachedERdma))
	}

	if config.EnableERDMA {
		if daemonMode == daemon.ModeENIMultiIP {
			nodeAnnotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(poolConfig.Capacity - realRdmaCount*limit.IPv4PerAdapter)
			nodeAnnotations[string(types.ERDMAIPTypeIPs)] = strconv.Itoa(realRdmaCount * limit.IPv4PerAdapter)
			poolConfig.ERdmaCapacity = realRdmaCount * limit.IPv4PerAdapter
		} else if daemonMode == daemon.ModeENIOnly {
			nodeAnnotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(poolConfig.Capacity - realRdmaCount)
			nodeAnnotations[string(types.ERDMAIPTypeIPs)] = strconv.Itoa(realRdmaCount)
			poolConfig.ERdmaCapacity = realRdmaCount
		}
	}

	runDevicePlugin(daemonMode, config, poolConfig)

	// ensure node annotations
	err = netSrv.k8s.PatchNodeAnnotations(nodeAnnotations)
	if err != nil {
		return nil, fmt.Errorf("error patch node annotations, %w", err)
	}

	netSrv.resourceDB, err = storage.NewDiskStorage(
		resDBName, utils.NormalizePath(resDBPath), json.Marshal, func(bytes []byte) (interface{}, error) {
			resourceRel := &daemon.PodResources{}
			err = json.Unmarshal(bytes, resourceRel)
			if err != nil {
				return nil, err
			}
			return *resourceRel, nil
		})
	if err != nil {
		return nil, err
	}

	objList, err := netSrv.resourceDB.List()
	if err != nil {
		return nil, err
	}

	attachedENIID := lo.SliceToMap(attached, func(item *daemon.ENI) (string, struct{}) {
		return item.ID, struct{}{}
	})
	podResources := getPodResources(objList)
	serviceLog.Info(fmt.Sprintf("loaded pod res, %v", podResources))

	podResources = filterENINotFound(podResources, attachedENIID)

	if config.EnableEIPMigrate {
		err = netSrv.migrateEIP(ctx, objList)
		if err != nil {
			return nil, err
		}
		serviceLog.Info("eip migrate finished")
	}

	err = preStartResourceManager(daemonMode, netSrv.k8s)
	if err != nil {
		return nil, err
	}

	var eniList []eni.NetworkInterface

	if daemonMode == daemon.ModeVPC {
		eniList = append(eniList, &eni.Veth{})
	}

	if daemonMode == daemon.ModeENIOnly {
		if config.IPAMType == types.IPAMTypeCRD {
			if !config.EnableENITrunking {
				eniList = append(eniList, eni.NewRemote(netSrv.k8s.GetClient(), nil))
			} else {
				for _, ni := range attached {
					if !ni.Trunk {
						continue
					}
					lo := eni.NewLocal(ni, "trunk", factory, poolConfig)
					eniList = append(eniList, eni.NewTrunk(netSrv.k8s.GetClient(), lo))
				}
			}
		} else {
			var (
				normalENICount int
				erdmaENICount  int
			)
			// the legacy mode
			for _, ni := range attached {
				if config.EnableERDMA && ni.ERdma {
					erdmaENICount++
					eniList = append(eniList, eni.NewLocal(ni, "erdma", factory, poolConfig))
				} else {
					normalENICount++
					eniList = append(eniList, eni.NewLocal(ni, "secondary", factory, poolConfig))
				}
			}
			normalENINeeded := poolConfig.MaxENI - normalENICount
			if config.EnableERDMA {
				normalENINeeded = poolConfig.MaxENI - limit.ERdmaAdapters - normalENICount
				for i := 0; i < limit.ERdmaAdapters-erdmaENICount; i++ {
					eniList = append(eniList, eni.NewLocal(nil, "erdma", factory, poolConfig))
				}
			}

			for i := 0; i < normalENINeeded; i++ {
				eniList = append(eniList, eni.NewLocal(nil, "secondary", factory, poolConfig))
			}
		}
	} else {
		var (
			normalENICount int
			erdmaENICount  int
		)
		for _, ni := range attached {
			serviceLog.V(5).Info("found attached eni", "eni", ni)
			if config.EnableENITrunking && ni.Trunk && trunkENIID == ni.ID {
				lo := eni.NewLocal(ni, "trunk", factory, poolConfig)
				eniList = append(eniList, eni.NewTrunk(netSrv.k8s.GetClient(), lo))
			} else if config.EnableERDMA && ni.ERdma {
				erdmaENICount++
				eniList = append(eniList, eni.NewLocal(ni, "erdma", factory, poolConfig))
			} else {
				normalENICount++
				eniList = append(eniList, eni.NewLocal(ni, "secondary", factory, poolConfig))
			}
		}
		normalENINeeded := poolConfig.MaxENI - normalENICount
		if config.EnableERDMA {
			normalENINeeded = poolConfig.MaxENI - limit.ERdmaAdapters - normalENICount
			for i := 0; i < limit.ERdmaAdapters-erdmaENICount; i++ {
				eniList = append(eniList, eni.NewLocal(nil, "erdma", factory, poolConfig))
			}
		}

		for i := 0; i < normalENINeeded; i++ {
			eniList = append(eniList, eni.NewLocal(nil, "secondary", factory, poolConfig))
		}
	}

	eniManager := eni.NewManager(poolConfig.MinPoolSize, poolConfig.MaxPoolSize, poolConfig.Capacity, 30*time.Second, eniList, netSrv.k8s)
	netSrv.eniMgr = eniManager
	err = eniManager.Run(ctx, &netSrv.wg, podResources)
	if err != nil {
		return nil, err
	}

	if config.IPAMType != types.IPAMTypeCRD {
		//start gc loop
		go netSrv.startGarbageCollectionLoop(ctx)
	}

	// register for tracing
	_ = tracing.Register(tracing.ResourceTypeNetworkService, "default", netSrv)
	tracing.RegisterResourceMapping(netSrv)
	tracing.RegisterEventRecorder(netSrv.k8s.RecordNodeEvent, netSrv.k8s.RecordPodEvent)

	return netSrv, nil
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

func filterENINotFound(podResources []daemon.PodResources, attachedENIID map[string]struct{}) []daemon.PodResources {
	for i := range podResources {
		for j := 0; j < len(podResources[i].Resources); j++ {
			if podResources[i].Resources[j].Type == daemon.ResourceTypeENI ||
				podResources[i].Resources[j].Type == daemon.ResourceTypeENIIP {
				if _, ok := attachedENIID[podResources[i].Resources[j].ENIID]; !ok {
					podResources[i].Resources = append(podResources[i].Resources[:j], podResources[i].Resources[j+1:]...)
				}
			}
		}
	}
	return podResources
}

func gcRoutes(links []netlink.Link, existIP sets.Set[string]) {
	for _, link := range links {
		routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
		if err != nil {
			serviceLog.Error(err, "gc list route", "link", link)
			return
		}
		for _, route := range routes {
			if route.Dst == nil {
				continue
			}
			// if not found
			if existIP.Has(route.Dst.IP.String()) {
				continue
			}

			serviceLog.Info("gc del route", "route", route)
			err = netlink.RouteDel(&route)
			if err != nil {
				serviceLog.Error(err, "gc del route", "route", route)
				return
			}
		}
	}
}

func gcTCFilters(links []netlink.Link, existIP sets.Set[string]) {
	// map ip to u32
	toU32List := lo.Map(existIP.UnsortedList(), func(item string, index int) uint32 {
		ip := net.ParseIP(item)
		if ip == nil {
			return 0
		}
		return binary.BigEndian.Uint32(ip.To4())
	})
	u32IPMap := lo.SliceToMap(toU32List, func(item uint32) (uint32, struct{}) {
		return item, struct{}{}
	})

	for _, link := range links {
		filters, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		if err != nil {
			serviceLog.Error(err, "gc list filter", "link", link)
			continue
		}
		for _, filter := range filters {
			u32, ok := filter.(*netlink.U32)
			if !ok {
				continue
			}
			if u32.Priority != 50001 {
				continue
			}

			if u32.Sel == nil || len(u32.Sel.Keys) != 1 {
				continue
			}
			if len(u32.Actions) != 1 {
				continue
			}
			_, ok = u32.Actions[0].(*netlink.VlanAction)
			if !ok {
				continue
			}
			if u32.Sel.Keys[0].Off != 12 {
				continue
			}
			_, ok = u32IPMap[u32.Sel.Keys[0].Val]
			if ok {
				continue
			}

			serviceLog.Info("gc tc filter", "filter", filter)
			err = netlink.FilterDel(filter)
			if err != nil {
				serviceLog.Error(err, "gc list filter", "link", link)
			}
		}
	}
}
