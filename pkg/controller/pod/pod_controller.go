/*
Copyright 2021-2022 Terway Authors.

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

package pod

import (
	"context"
	"fmt"
	"strings"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
	"github.com/AliyunContainerService/terway/types/daemon"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const controllerName = "pod"
const defaultInterface = "eth0"

func init() {
	register.Add(controllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		ctrlCtx.RegisterResource = append(ctrlCtx.RegisterResource, &corev1.Pod{})

		crdMode := controlplane.GetConfig().IPAMType == types.IPAMTypeCRD

		err := builder.ControllerManagedBy(mgr).
			Named(controllerName).
			WithOptions(controller.Options{
				MaxConcurrentReconciles: controlplane.GetConfig().PodMaxConcurrent,
			}).
			For(&corev1.Pod{}, builder.WithPredicates(&predicateForPodEvent{})).
			Watches(&v1beta1.PodENI{}, &handler.EnqueueRequestForObject{}).
			Complete(NewReconcilePod(mgr, ctrlCtx.AliyunClient, ctrlCtx.VSwitchPool, crdMode))

		return err
	}, true)
}

// ReconcilePod implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePod{}

// ReconcilePod reconciles a AutoRepair object
type ReconcilePod struct {
	client client.Client
	scheme *runtime.Scheme
	aliyun register.Interface

	swPool *vswitch.SwitchPool

	//record event recorder
	record record.EventRecorder

	trunkMode bool // use trunk mode or secondary eni mode
	// deprecated
	crdMode bool
}

// NewReconcilePod watch pod lifecycle events and sync to podENI resource
func NewReconcilePod(mgr manager.Manager, aliyunClient register.Interface, swPool *vswitch.SwitchPool, crdMode bool) *ReconcilePod {
	r := &ReconcilePod{
		client:    mgr.GetClient(),
		scheme:    mgr.GetScheme(),
		record:    mgr.GetEventRecorderFor("TerwayPodController"),
		aliyun:    aliyunClient,
		swPool:    swPool,
		trunkMode: *controlplane.GetConfig().EnableTrunk,
		crdMode:   crdMode,
	}
	return r
}

// Reconcile all pod events
// Pod create -> create PodENI
// Pod delete -> delete PodENI
// Fixed IP Pod delete -> mark PodENI status v1beta1.ENIPhaseDetaching
// before delete event is trigger will check pod phase make sure sandbox is terminated
func (m *ReconcilePod) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.V(5).Info("Reconcile")
	start := time.Now()
	pod := &corev1.Pod{}
	err := m.client.Get(ctx, request.NamespacedName, pod)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			result, err := m.podDelete(ctx, request.NamespacedName)
			m.recordPodDelete(pod, start, err)
			return result, err
		}
		return reconcile.Result{}, err
	}

	if utils.PodSandboxExited(pod) {
		result, err := m.podDelete(ctx, request.NamespacedName)
		m.recordPodDelete(pod, start, err)
		return result, err
	}

	// for pod is deleting we will wait it terminated
	if !pod.DeletionTimestamp.IsZero() {
		return reconcile.Result{RequeueAfter: 2 * time.Second}, nil
	}

	result, err := m.podCreate(ctx, pod)
	m.recordPodCreate(pod, start, err)
	return result, err
}

// NeedLeaderElection need election
func (m *ReconcilePod) NeedLeaderElection() bool {
	return true
}

// podCreate is the func when the pod is to be create or created
func (m *ReconcilePod) podCreate(ctx context.Context, pod *corev1.Pod) (reconcile.Result, error) {
	l := log.FromContext(ctx)

	// check pods
	if !processPod(pod) {
		return reconcile.Result{}, nil
	}

	node, err := m.getNode(ctx, pod.Spec.NodeName)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error get node %s, %w", pod.Spec.NodeName, err)
	}

	if !processNode(node) {
		return reconcile.Result{}, nil
	}

	if !types.PodUseENI(pod) &&
		types.NodeExclusiveENIMode(node.Labels) != types.ExclusiveENIOnly &&
		!m.crdMode {
		return reconcile.Result{}, nil
	}

	// 1. check podENI is existed
	prePodENI := &v1beta1.PodENI{}
	err = m.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, prePodENI)
	if err == nil {
		l.V(5).Info("podENI", "phase", prePodENI.Status.Phase)
		// for podENI is deleting , wait it down
		if !prePodENI.DeletionTimestamp.IsZero() {
			return reconcile.Result{Requeue: true}, nil
		}
		switch prePodENI.Status.Phase {
		case v1beta1.ENIPhaseUnbind:
			return m.reConfig(ctx, pod, prePodENI)
		case v1beta1.ENIPhaseBind:
			// check pod uid
			if prePodENI.Annotations[types.PodUID] == string(pod.UID) {
				return reconcile.Result{}, nil
			}
			// if using fixed ip , unbind it
			if prePodENI.Spec.HaveFixedIP() {
				prePodENICopy := prePodENI.DeepCopy()
				prePodENICopy.Status.Phase = v1beta1.ENIPhaseDetaching
				err = m.client.Status().Update(ctx, prePodENICopy)
				return reconcile.Result{RequeueAfter: 5 * time.Second}, err
			}
			return reconcile.Result{RequeueAfter: 5 * time.Second}, m.client.Delete(ctx, prePodENI)
		case v1beta1.ENIPhaseBinding, v1beta1.ENIPhaseDetaching:
			return reconcile.Result{RequeueAfter: time.Second}, nil
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// 2. cr is not found , so we will create new
	nodeInfo, allocs, err := m.parse(ctx, pod, node)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error parse config, %w", err)
	}

	l.Info("creating eni")

	if utils.ISLinJunNode(node.Labels) {
		crNode := &v1beta1.Node{}
		err = m.client.Get(ctx, k8stypes.NamespacedName{Name: node.Name}, crNode)
		if err != nil {
			return reconcile.Result{}, err
		}
		backend := aliyunClient.BackendAPIEFLO
		if crNode.Annotations[types.ENOApi] == "hdeni" {
			backend = aliyunClient.BackendAPIEFLOHDENI
		}
		ctx = aliyunClient.SetBackendAPI(ctx, backend)
	}

	podENI := &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Finalizers: []string{
				types.FinalizerPodENIV2,
			},
			Annotations: map[string]string{
				types.PodUID: string(pod.UID),
			},
			Labels: map[string]string{
				types.ENIRelatedNodeName: nodeInfo.NodeName,
			},
		},
	}

	defer func() {
		if err != nil {
			l.Error(err, "error ,will roll back all created eni")
			innerErr := m.deleteAllENI(ctx, podENI)
			if innerErr != nil {
				l.Error(innerErr, "error delete eni")
			}
		}
	}()

	podENI.Spec.Zone = nodeInfo.ZoneID

	// 2.2 create eni
	err = m.createENI(ctx, &allocs, pod, podENI, nodeInfo)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error batch create eni,%w", err)
	}

	// 2.3 create cr
	err = m.client.Create(ctx, podENI)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error create cr, %s", err)
	}

	// 2.4 wait cr created
	common.WaitCreated(ctx, m.client, &v1beta1.PodENI{}, pod.Namespace, pod.Name)

	return reconcile.Result{}, nil
}

func (m *ReconcilePod) recordPodCreate(pod *corev1.Pod, startTime time.Time, err error) {
	if err == nil || pod == nil {
		return
	}
	m.record.Eventf(pod, corev1.EventTypeWarning,
		"CniPodCreateError", fmt.Sprintf("PodCreateError: %s, elapsedTime: %s", err, time.Since(startTime)))
}

func (m *ReconcilePod) recordPodDelete(pod *corev1.Pod, startTime time.Time, err error) {
	if err == nil || pod == nil {
		return
	}
	m.record.Eventf(pod, corev1.EventTypeWarning,
		"CniPodDeleteError", fmt.Sprintf("CniPodDeleteError: %s, elapsedTime: %s", err, time.Since(startTime)))
}

// podDelete is proceed after pod is deleted
// for none fixed ip pod, will delete podENI resource and let podENI controller do remain gc
// for fixed ip pod , update v1beta1.PodENI status to v1beta1.ENIPhaseDetaching
func (m *ReconcilePod) podDelete(ctx context.Context, namespacedName client.ObjectKey) (reconcile.Result, error) {
	prePodENI := &v1beta1.PodENI{}
	err := m.client.Get(ctx, namespacedName, prePodENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
	}
	// already deleting
	if prePodENI.Status.Phase == v1beta1.ENIPhaseDeleting || !prePodENI.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	haveFixedIP := false
	for _, alloc := range prePodENI.Spec.Allocations {
		if alloc.AllocationType.Type == v1beta1.IPAllocTypeFixed {
			haveFixedIP = true
		}
	}
	if haveFixedIP {
		// for fixed ip , update podENI status to v1beta1.ENIPhaseDetaching
		if prePodENI.Status.Phase == v1beta1.ENIPhaseDetaching {
			return reconcile.Result{}, nil
		}
		prePodENICopy := prePodENI.DeepCopy()
		prePodENICopy.Status.Phase = v1beta1.ENIPhaseDetaching
		err = m.client.Status().Update(ctx, prePodENICopy)
		return reconcile.Result{}, err
	}

	// for non fixed ip, update status to v1beta1.ENIPhaseDeleting
	update := prePodENI.DeepCopy()
	update.Status.Phase = v1beta1.ENIPhaseDeleting
	err = m.client.Status().Update(ctx, update)

	return reconcile.Result{}, err
}

func (m *ReconcilePod) deleteAllENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	for _, alloc := range podENI.Spec.Allocations {
		if alloc.ENI.ID == "" {
			continue
		}
		err := common.Delete(ctx, m.client, &common.DeleteOption{
			NetworkInterfaceID: alloc.ENI.ID,
			IgnoreCache:        false,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *ReconcilePod) getNode(ctx context.Context, name string) (*corev1.Node, error) {
	node := &corev1.Node{}
	err := m.client.Get(ctx, k8stypes.NamespacedName{
		Name: name,
	}, node)
	return node, err
}

func (m *ReconcilePod) parse(ctx context.Context, pod *corev1.Pod, node *corev1.Node) (*common.NodeInfo, []*v1beta1.Allocation, error) {
	nodeInfo, err := common.NewNodeInfo(node)
	if err != nil {
		return nil, nil, err
	}

	// 2.1 fill config
	anno, err := controlplane.ParsePodNetworksFromAnnotation(pod)
	if err != nil {
		return nil, nil, fmt.Errorf("error parse pod annotation, %w", err)
	}

	allocs, err := m.ParsePodNetworksFromAnnotation(ctx, nodeInfo.ZoneID, anno)
	if err != nil {
		return nil, nil, fmt.Errorf("error parse pod annotation, %w", err)
	}

	if len(allocs) == 0 {
		// use config from podNetworking

		podNetwokingName := pod.Annotations[types.PodNetworking]
		if podNetwokingName == "" {
			if m.crdMode ||
				types.NodeExclusiveENIMode(node.Labels) == types.ExclusiveENIOnly {
				// fall back policy , if webhook not enabled

				cfg, err := daemon.ConfigFromConfigMap(ctx, m.client, "")
				if err != nil {
					return nil, nil, err
				}

				vsw, err := m.swPool.GetOne(ctx, m.aliyun, nodeInfo.ZoneID, cfg.GetVSwitchIDs())
				if err != nil {
					return nil, nil, fmt.Errorf("can not found available vSwitch for zone %s, %w", nodeInfo.ZoneID, err)
				}
				allocs = append(allocs, &v1beta1.Allocation{
					AllocationType: v1beta1.AllocationType{
						Type: v1beta1.IPAllocTypeElastic,
					},
					ENI: v1beta1.ENI{
						SecurityGroupIDs: cfg.GetSecurityGroups(),
						VSwitchID:        vsw.ID,
					},
					IPv4CIDR: vsw.IPv4CIDR,
					IPv6CIDR: vsw.IPv6CIDR,
				})

				return nodeInfo, allocs, nil
			}
			return nil, nil, fmt.Errorf("podNetworking is empty")
		}
		var podNetworking v1beta1.PodNetworking

		err = m.client.Get(ctx, k8stypes.NamespacedName{
			Name: podNetwokingName,
		}, &podNetworking)
		if err != nil {
			return nil, nil, fmt.Errorf("error get podNetworking %s, %w", podNetwokingName, err)
		}
		var vsw *vswitch.Switch
		vsw, err = m.swPool.GetOne(ctx, m.aliyun, nodeInfo.ZoneID, podNetworking.Spec.VSwitchOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("can not found available vSwitch for zone %s, %w", nodeInfo.ZoneID, err)
		}

		allocs = append(allocs, &v1beta1.Allocation{
			AllocationType: podNetworking.Spec.AllocationType,
			ENI: v1beta1.ENI{
				SecurityGroupIDs: podNetworking.Spec.SecurityGroupIDs,
				VSwitchID:        vsw.ID,
			},
			IPv4CIDR: vsw.IPv4CIDR,
			IPv6CIDR: vsw.IPv6CIDR,
		})

	}

	// set the attachment type
	lo.ForEach(allocs, func(item *v1beta1.Allocation, index int) {
		if item.ENI.AttachmentOptions.Trunk == nil {
			// user the cluster value
			trunk := false
			if m.trunkMode {
				if types.NodeExclusiveENIMode(node.Labels) == types.ExclusiveENIOnly {
					log.FromContext(ctx).Info("node is at exclusive eni mode, use eniOnly")
					trunk = false
				} else {
					trunk = true
				}
			} else {
				// eniOnly
				trunk = false
			}

			item.ENI.AttachmentOptions.Trunk = &trunk
		}
	})

	return nodeInfo, allocs, nil
}

// reConfig this phase will re-config the eni if possible
// 1. update pod uid
// 2. re-generate the target spec
func (m *ReconcilePod) reConfig(ctx context.Context, pod *corev1.Pod, prePodENI *v1beta1.PodENI) (reconcile.Result, error) {
	update := prePodENI.DeepCopy()

	if prePodENI.Labels[types.ENIRelatedNodeName] != "" {
		// ignore all create for eci pod
		node, err := m.getNode(ctx, pod.Spec.NodeName)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error get node %s, %w", node.Name, err)
		}
		if utils.ISVKNode(node) {
			return reconcile.Result{}, nil
		}
		if prePodENI.Labels[types.ENIRelatedNodeName] != node.Name {
			update.Labels[types.ENIRelatedNodeName] = node.Name
			err = m.client.Patch(ctx, update, client.MergeFrom(prePodENI))
			return reconcile.Result{Requeue: true}, err
		}
	}

	if prePodENI.Annotations[types.PodUID] == string(pod.UID) {
		update.Status.Phase = v1beta1.ENIPhaseBinding
		err := m.client.Status().Update(ctx, update)

		return reconcile.Result{}, err
	}

	if update.Annotations == nil {
		update.Annotations = make(map[string]string)
	}
	update.Annotations[types.PodUID] = string(pod.UID)

	err := m.client.Update(ctx, update)

	return reconcile.Result{Requeue: true}, err
}

func (m *ReconcilePod) createENI(ctx context.Context, allocs *[]*v1beta1.Allocation, pod *corev1.Pod, podENI *v1beta1.PodENI, nodeInfo *common.NodeInfo) error {
	if allocs == nil || len(*allocs) == 0 {
		return nil
	}
	l := log.FromContext(ctx)

	var err error
	defer func() {
		if err != nil {
			if k8sErr.IsConflict(err) {
				return
			}
			l.WithValues("eni", "").Error(err, "create fail")
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventCreateENIFailed, err.Error())
		} else {
			var ids []string
			for _, alloc := range podENI.Spec.Allocations {
				ids = append(ids, alloc.ENI.ID)
				l.WithValues("eni", alloc.ENI.ID).Info("created")
			}
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventCreateENISucceed, "create enis %s", strings.Join(ids, ","))
		}
	}()

	clusterID := controlplane.GetConfig().ClusterID
	vpcID := controlplane.GetConfig().VPCID

	ipv6Count := 0
	switch controlplane.GetConfig().IPStack {
	case "ipv6", "dual":
		ipv6Count = 1
	}

	ch := make(chan *v1beta1.Allocation)
	done := make(chan struct{})
	go func() {
		for alloc := range ch {
			podENI.Spec.Allocations = append(podENI.Spec.Allocations, *alloc)
		}
		done <- struct{}{}
	}()

	g, _ := errgroup.WithContext(context.Background())
	for i := range *allocs {
		ii := i
		g.Go(func() error {
			alloc := (*allocs)[ii]
			ctx := common.WithCtx(ctx, alloc)

			deleteENIOnECSRelease := true
			if alloc.AllocationType.Type == v1beta1.IPAllocTypeFixed {
				deleteENIOnECSRelease = false
			}
			bo := backoff.Backoff(backoff.ENICreate)
			option := &aliyunClient.CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
					Trunk:            false,
					ERDMA:            false,
					VSwitchID:        alloc.ENI.VSwitchID,
					SecurityGroupIDs: alloc.ENI.SecurityGroupIDs,
					ResourceGroupID:  alloc.ENI.ResourceGroupID,
					IPCount:          1,
					IPv6Count:        ipv6Count,
					Tags: map[string]string{
						types.TagKeyClusterID:               clusterID,
						types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
					},
					DeleteENIOnECSRelease: &deleteENIOnECSRelease,
					SourceDestCheck:       ptr.To(false),

					// eflo
					ZoneID:     podENI.Spec.Zone,
					VPCID:      vpcID,
					InstanceID: nodeInfo.InstanceID,
				},
				Backoff: &bo,
			}
			eni, err := m.aliyun.CreateNetworkInterfaceV2(ctx, option)
			if err != nil {

				if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough, apiErr.QuotaExceededPrivateIPAddress) {
					m.swPool.Block(alloc.ENI.VSwitchID)
				}

				return fmt.Errorf("create eni with openAPI err, %w", err)
			}

			// store cr
			cr := common.ToNetworkInterfaceCR(eni)
			controllerutil.AddFinalizer(cr, types.FinalizerENI)
			cr.Spec.PodENIRef = &corev1.ObjectReference{
				Kind:      "Pod",
				Name:      podENI.Name,
				Namespace: podENI.Namespace,
			}

			err = m.client.Create(ctx, cr)
			if err != nil {
				// delete the eni, as we can not store the eni info
				rollbackCtx := context.Background()

				innerErr := retry.OnError(backoff.Backoff(backoff.ENIRelease), func(err error) bool {
					return true
				}, func() error {
					innerErr := m.aliyun.DeleteNetworkInterfaceV2(rollbackCtx, eni.NetworkInterfaceID)
					return innerErr
				})
				if innerErr != nil {
					m.record.Eventf(pod, corev1.EventTypeWarning, types.EventCreateENIFailed, "rollbackErr %s, createErr %s", innerErr, err)
				}

				return fmt.Errorf("create eni cr err,rollbackErr %s %w", innerErr, err)
			}

			common.WaitCreated(ctx, m.client, &v1beta1.NetworkInterface{}, cr.Namespace, cr.Name)

			v6 := ""
			if len(eni.IPv6Set) > 0 {
				v6 = eni.IPv6Set[0].IPAddress
			}
			alloc.ENI = v1beta1.ENI{
				ID:               eni.NetworkInterfaceID,
				VPCID:            eni.VPCID,
				MAC:              eni.MacAddress,
				Zone:             eni.ZoneID,
				VSwitchID:        eni.VSwitchID,
				ResourceGroupID:  eni.ResourceGroupID,
				SecurityGroupIDs: eni.SecurityGroupIDs,
			}
			alloc.IPv4 = eni.PrivateIPAddress
			alloc.IPv6 = v6

			ch <- alloc
			return nil
		})
	}
	err = g.Wait()
	close(ch)
	<-done
	return err
}
