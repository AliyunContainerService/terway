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

package podeni

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

var ctrlLog = ctrl.Log.WithName(controllerName)

const controllerName = "pod-eni"
const layout = "2006-01-02T15:04:05Z"

func init() {
	register.Add(controllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		r := NewReconcilePod(mgr, ctrlCtx.DelegateClient)
		c, err := controller.NewUnmanaged(controllerName, mgr, controller.Options{
			Reconciler:              r,
			MaxConcurrentReconciles: controlplane.GetConfig().PodENIMaxConcurrent,
		})
		if err != nil {
			return err
		}

		w := &Wrapper{
			ctrl: c,
			r:    r,
		}
		err = mgr.Add(w)
		if err != nil {
			return err
		}

		return c.Watch(
			source.Kind(mgr.GetCache(), &v1beta1.PodENI{}),
			&handler.EnqueueRequestForObject{},
			&predicate.ResourceVersionChangedPredicate{},
			&predicateForPodENIEvent{},
		)
	}, true)
}

var (
	leakedENICheckPeriod = 10 * time.Minute
	podENICheckPeriod    = 1 * time.Minute
)

// ReconcilePodENI implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePodENI{}

// ReconcilePodENI reconciles a AutoRepair object
type ReconcilePodENI struct {
	client client.Client
	scheme *runtime.Scheme
	aliyun register.Interface

	//record event recorder
	record record.EventRecorder

	trunkMode bool // use trunk mode or secondary eni mode
	crdMode   bool
}

type Wrapper struct {
	ctrl controller.Controller
	r    *ReconcilePodENI
}

// Start the controller
func (w *Wrapper) Start(ctx context.Context) error {
	// start the gc process
	w.r.gc(ctx)

	err := w.ctrl.Start(ctx)
	if err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

// NeedLeaderElection need election
func (w *Wrapper) NeedLeaderElection() bool {
	return true
}

// NewReconcilePod watch pod lifecycle events and sync to podENI resource
func NewReconcilePod(mgr manager.Manager, aliyunClient register.Interface) *ReconcilePodENI {
	r := &ReconcilePodENI{
		client:    mgr.GetClient(),
		scheme:    mgr.GetScheme(),
		record:    mgr.GetEventRecorderFor("TerwayPodENIController"),
		aliyun:    aliyunClient,
		trunkMode: *controlplane.GetConfig().EnableTrunk,
		crdMode:   controlplane.GetConfig().IPAMType == types.IPAMTypeCRD,
	}
	return r
}

// Reconcile all podENI resource
// podENI create -> do attach to node and update status
// podENI delete -> detach podENI and delete
func (m *ReconcilePodENI) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.Info("Reconcile")
	start := time.Now()
	podENI := &v1beta1.PodENI{}
	err := m.client.Get(ctx, request.NamespacedName, podENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if controlplane.GetConfig().EnableENIPool {
		nodeName := podENI.Labels[types.ENIRelatedNodeName]
		if nodeName == "" {
			// for legacy
			nodes := &corev1.NodeList{}
			labelSelector, _ := labels.Parse("type!=virtual-kubelet")
			err = m.client.List(ctx, nodes, &client.ListOptions{
				LabelSelector: labelSelector,
			})
			if err != nil {
				return reconcile.Result{}, err
			}
			for _, n := range nodes.Items {
				nodeInfo, err := common.NewNodeInfo(&n)
				if err != nil {
					return reconcile.Result{}, err
				}
				if nodeInfo.InstanceID == podENI.Status.InstanceID {
					nodeName = n.Name
					break
				}
			}
		}
		if nodeName != "" {
			_, err = m.getNode(ctx, nodeName)
			if err != nil {
				if !k8sErr.IsNotFound(err) {
					return reconcile.Result{}, err
				}
				// for node has gone , use default client
			} else {
				// set node name in ctx
				l.Info("set ctx", "nodeName", nodeName)
				ctx = common.NodeNameWithCtx(ctx, nodeName)
			}
		}
		// use default client
	}

	if !podENI.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(podENI, types.FinalizerPodENI) {
			return reconcile.Result{}, nil
		}
		result, err := m.podENIDelete(ctx, podENI)
		m.recordPodENIDeleteErr(podENI, start, err)
		return result, err
	}
	result, err := m.podENICreate(ctx, request.NamespacedName, podENI)
	if err != nil {
		m.recordPodENICreateErr(podENI, start, err)
		return result, err
	}

	if err = common.SetPodAnnotation(ctx, m.client, podENI); err != nil {
		m.recordPodENICreateErr(podENI, start, err)
		return reconcile.Result{}, err
	}

	return result, nil
}

func (m *ReconcilePodENI) recordPodENIDeleteErr(podEni *v1beta1.PodENI, startTime time.Time, err error) {
	if err == nil {
		return
	}
	var pod = &corev1.Pod{}
	if getErr := m.client.Get(context.Background(), k8stypes.NamespacedName{Namespace: podEni.Namespace, Name: podEni.Name}, pod); getErr != nil {
		return
	}
	m.record.Eventf(pod, corev1.EventTypeWarning,
		"CniPodENIDeleteErr", fmt.Sprintf("CniPodENIDeleteErr: %s, elapsedTime: %s", err, time.Since(startTime)))
}

func (m *ReconcilePodENI) recordPodENICreateErr(podEni *v1beta1.PodENI, startTime time.Time, err error) {
	if err == nil {
		return
	}
	var pod = &corev1.Pod{}
	if getErr := m.client.Get(context.Background(), k8stypes.NamespacedName{Namespace: podEni.Namespace, Name: podEni.Name}, pod); getErr != nil {
		return
	}
	m.record.Eventf(pod, corev1.EventTypeWarning,
		"CniCreateENIError", fmt.Sprintf("CniCreateENIError: %s, elapsedTime: %s", err, time.Since(startTime)))
}

// NeedLeaderElection need election
func (m *ReconcilePodENI) NeedLeaderElection() bool {
	return true
}

// gc will handle following circumstances
// 1. cr podENI is leaked
// 2. release fixed ip resource by strategy
func (m *ReconcilePodENI) gc(ctx context.Context) {
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) {
		m.gcSecondaryENI(ctx)

		m.gcMemberENI(ctx)
	}, leakedENICheckPeriod, 1.1, true)

	go wait.JitterUntilWithContext(ctx, m.gcCRPodENIs, podENICheckPeriod, 1.1, true)
}

func (m *ReconcilePodENI) podENICreate(ctx context.Context, namespacedName client.ObjectKey, podENI *v1beta1.PodENI) (result reconcile.Result, err error) {
	l := log.FromContext(ctx)
	l.Info("podENI created")

	switch podENI.Status.Phase {
	case v1beta1.ENIPhaseBind:
		l.V(5).Info("already bind")
		return reconcile.Result{}, nil
	case v1beta1.ENIPhaseUnbind:
		l.V(5).Info("already unbind")
		return reconcile.Result{}, nil
	case v1beta1.ENIPhaseDetaching:
		// for pod require to unbind eni
		defer func() {
			if err != nil {
				l.Error(err, "detach failed")
				m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventDetachENIFailed, "%s", err.Error())
			}
		}()
		return m.detach(ctx, podENI)
	case v1beta1.ENIPhaseDeleting:
		err = m.client.Delete(ctx, podENI)
		if err != nil {
			if k8sErr.IsNotFound(err) {
				l.Info("cr resource not found")
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	case v1beta1.ENIPhaseInitial, v1beta1.ENIPhaseBinding: // pod first create or rebind
		// for pod require to unbind eni
		defer func() {
			if err != nil {
				m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventAttachENIFailed, "%s", err.Error())
			}
		}()
		if len(podENI.Spec.Allocations) == 0 {
			err = fmt.Errorf("alloction is empty")
			return reconcile.Result{}, err
		}
		pod := &corev1.Pod{}
		err = m.client.Get(ctx, namespacedName, pod)
		if err != nil {
			return reconcile.Result{}, err
		}

		node, err := m.getNode(ctx, pod.Spec.NodeName)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error get node %s, %w", pod.Spec.NodeName, err)
		}

		nodeInfo, err := common.NewNodeInfo(node)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error parse node info %s, %w", pod.Spec.NodeName, err)
		}

		if m.trunkMode && nodeInfo.TrunkENIID == "" {
			return reconcile.Result{}, fmt.Errorf("trunk eni id not found, this may due to terway agent is not started")
		} else if !m.trunkMode && nodeInfo.TrunkENIID != "" {
			nodeInfo.TrunkENIID = ""
		}

		needAttach := false
		if podENI.Status.Phase == "" {
			needAttach = true
		} else {
			if utils.IsStsPod(pod) {
				for _, alloc := range podENI.Spec.Allocations {
					if alloc.AllocationType.Type == v1beta1.IPAllocTypeFixed {
						needAttach = true
					}
				}
			}
		}
		if !needAttach {
			// user need delete podENI or wait GC finished
			return reconcile.Result{}, fmt.Errorf("found previous podENI, but pod is not using fixed ip")
		}

		podENICopy := podENI.DeepCopy()
		podENICopy.Status.InstanceID = nodeInfo.InstanceID
		podENICopy.Status.TrunkENIID = nodeInfo.TrunkENIID
		if podENICopy.Status.ENIInfos == nil {
			podENICopy.Status.ENIInfos = make(map[string]v1beta1.ENIInfo)
		}

		ll := l.WithValues("eni", podENICopy.Spec.Allocations[0].ENI.ID, "trunk", podENICopy.Status.TrunkENIID, "instance", podENICopy.Status.InstanceID)

		err = m.attachENI(ctx, podENICopy)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("attach eni failed, %w", err)
		}
		ll.Info("attached")

		podENICopy.Status.Phase = v1beta1.ENIPhaseBind
		if podENICopy.Spec.HaveFixedIP() {
			podENICopy.Status.PodLastSeen = metav1.Now()
		}

		// if attach succeed and update status failed , we can not store instance id
		// so in later detach , we are unable to detach the eni
		_, err = common.UpdatePodENIStatus(ctx, m.client, podENICopy)
		if err != nil {
			ll.Error(err, "update podENI")
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventUpdatePodENIFailed, "%s", err.Error())
		} else {
			ll.Info("update podENI")
		}
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (m *ReconcilePodENI) podENIDelete(ctx context.Context, podENI *v1beta1.PodENI) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.Info("podENI delete")

	podENICopy := podENI.DeepCopy()

	// detach eni
	err := m.detachMemberENI(ctx, podENICopy)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error detachMemberENI podENI status to %s", v1beta1.ENIPhaseUnbind)
	}
	// delete eni
	err = m.deleteMemberENI(ctx, podENICopy)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error delete eni, %w", err)
	}
	controllerutil.RemoveFinalizer(podENICopy, types.FinalizerPodENI)
	err = m.client.Update(ctx, podENICopy)
	if k8sErr.IsNotFound(err) {
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, err
}

func (m *ReconcilePodENI) gcSecondaryENI(ctx context.Context) {
	// 1. list all available enis ( which type is secondary)
	enis, err := m.aliyun.DescribeNetworkInterface(ctx, controlplane.GetConfig().VPCID, nil, "", aliyunClient.ENITypeSecondary, aliyunClient.ENIStatusAvailable, nil)
	if err != nil {
		ctrlLog.Error(err, "error list all member enis")
		return
	}
	if len(enis) == 0 {
		return
	}

	var networkInterfaces []*aliyunClient.NetworkInterface
	for _, networkInterface := range enis {
		if networkInterface.Type != aliyunClient.ENITypeSecondary {
			continue
		}
		// ignore eni with pool tag
		if controlplane.GetConfig().EnableENIPool {
			if m.eniFilter(networkInterface, map[string]string{
				types.TagENIAllocPolicy: "pool",
			}) {
				continue
			}
		}

		networkInterfaces = append(networkInterfaces, networkInterface)
	}

	err = m.gcENIs(ctx, networkInterfaces)
	if err != nil {
		ctrlLog.Error(err, "error gc enis")
		return
	}
}

func (m *ReconcilePodENI) gcMemberENI(ctx context.Context) {
	// 1. list all attached member eni
	enis, err := m.aliyun.DescribeNetworkInterface(ctx, controlplane.GetConfig().VPCID, nil, "", aliyunClient.ENITypeMember, aliyunClient.ENIStatusInUse, nil)
	if err != nil {
		ctrlLog.Error(err, "error list all member enis")
		return
	}
	if len(enis) == 0 {
		return
	}
	var networkInterfaces []*aliyunClient.NetworkInterface
	for _, networkInterface := range enis {
		if networkInterface.Type != aliyunClient.ENITypeMember {
			continue
		}
		// ignore eni with pool tag
		if controlplane.GetConfig().EnableENIPool {
			if m.eniFilter(networkInterface, map[string]string{
				types.TagENIAllocPolicy: "pool",
			}) {
				continue
			}
		}

		networkInterfaces = append(networkInterfaces, networkInterface)
	}

	err = m.gcENIs(ctx, networkInterfaces)
	if err != nil {
		ctrlLog.Error(err, "error gc enis")
		return
	}
}

func (m *ReconcilePodENI) gcENIs(ctx context.Context, enis []*aliyunClient.NetworkInterface) error {
	l := ctrl.Log.WithName("gc-enis")

	eniMap := make(map[string]*aliyunClient.NetworkInterface, len(enis))

	// 1. filter out eni which is created by terway
	tagFilter := map[string]string{
		types.TagKeyClusterID:               controlplane.GetConfig().ClusterID,
		types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
	}
	now := time.Now()
	for i, eni := range enis {
		if !m.eniFilter(eni, tagFilter) {
			continue
		}
		t, err := time.Parse(layout, eni.CreationTime)
		if err != nil {
			l.Error(err, "error parse eni create time")
			continue
		}
		// avoid conflict with create process
		if t.Add(10 * time.Minute).After(now) {
			continue
		}

		eniMap[eni.NetworkInterfaceID] = enis[i]
	}
	if len(eniMap) == 0 {
		return nil
	}

	// 2. list cr get all using eni
	podENIs := &v1beta1.PodENIList{}
	err := m.client.List(ctx, podENIs)
	if err != nil {
		l.Error(err, "error list cr pod enis")
		return err
	}

	// 3. range podENI and gc useless eni
	for _, podENI := range podENIs.Items {
		for _, alloc := range podENI.Spec.Allocations {
			_, ok := eniMap[alloc.ENI.ID]
			if !ok {
				continue
			}
			delete(eniMap, alloc.ENI.ID)
		}
	}

	// 4. the left eni is going to be deleted
	for _, eni := range eniMap {
		if eni.Type == aliyunClient.ENITypeMember && eni.Status == aliyunClient.ENIStatusInUse {
			l.Info("detach eni", "eni", eni.NetworkInterfaceID, "trunk-eni", eni.TrunkNetworkInterfaceID)
			err = m.aliyun.DetachNetworkInterface(ctx, eni.NetworkInterfaceID, eni.InstanceID, eni.TrunkNetworkInterfaceID) // still need delegate ? otherwise may break quota
			if err != nil {
				l.Error(err, fmt.Sprintf("errot detach eni %s", eni.NetworkInterfaceID))
			}
			// we continue here because we can delete eni in next check
			continue
		}
		if eni.Status == aliyunClient.ENIStatusAvailable {
			l.Info("delete eni", "eni", eni.NetworkInterfaceID)
			err = m.aliyun.DeleteNetworkInterface(ctx, eni.NetworkInterfaceID)
			if err != nil {
				l.Info(fmt.Sprintf("delete leaked eni %s, %s", eni.NetworkInterfaceID, err))
			}
			continue
		}
	}
	return nil
}

// gcCRPodENIs remove useless cr res
func (m *ReconcilePodENI) gcCRPodENIs(ctx context.Context) {
	l := ctrl.Log.WithName("gc-podENI")

	podENIs := &v1beta1.PodENIList{}
	err := m.client.List(ctx, podENIs)
	if err != nil {
		l.Error(err, "error list cr pod enis")
		return
	}

	// 1. found the pod relate to cr
	// 2. release res if pod is not present and not use fixed ip
	// 3. clean fixed ip cr
	for _, podENI := range podENIs.Items {
		func() {
			ctx, cancel := context.WithTimeout(ctx, 1*time.Minute)
			defer cancel()

			p := &corev1.Pod{}
			err = m.client.Get(ctx, k8stypes.NamespacedName{
				Namespace: podENI.Namespace,
				Name:      podENI.Name,
			}, p)
			if err != nil && !k8sErr.IsNotFound(err) {
				l.Error(err, "error get pod")
				return
			}
			ll := l.WithValues("pod", k8stypes.NamespacedName{
				Namespace: podENI.Namespace,
				Name:      podENI.Name,
			}.String())

			// pod exist just update timestamp
			if err == nil {
				if !podRequirePodENI(p, m.crdMode) {
					err = m.deletePodENI(ctx, &podENI)
					if err != nil {
						ll.Error(err, "error set podENI to ENIPhaseDeleting")
					}
					return
				}
				// for non fixed-ip pod no need to update timeStamp
				if !podENI.Spec.HaveFixedIP() {
					return
				}

				ll.V(5).Info("update pod lastSeen to now")
				update := podENI.DeepCopy()
				update.Status.PodLastSeen = metav1.Now()

				err = m.client.Status().Patch(ctx, update, client.MergeFrom(&podENI))
				if err != nil {
					ll.Error(err, "error update timestamp")
				}
				return
			}

			switch podENI.Status.Phase {
			case v1beta1.ENIPhaseDetaching, v1beta1.ENIPhaseDeleting, v1beta1.ENIPhaseBinding:
				ll.V(5).Info("resource is processing, wait next time", "phase", podENI.Status.Phase)
				return
			}
			// pod not exist so check all alloc
			keep := false
			for _, alloc := range podENI.Spec.Allocations {
				if alloc.AllocationType.Type == v1beta1.IPAllocTypeFixed {
					switch alloc.AllocationType.ReleaseStrategy {
					case v1beta1.ReleaseStrategyNever:
						keep = true
						continue
					case v1beta1.ReleaseStrategyTTL:
						duration, err := time.ParseDuration(alloc.AllocationType.ReleaseAfter)
						if err != nil {
							keep = true
							ll.Error(err, "error parse ReleaseAfter")
							continue
						}
						if duration < 0 {
							keep = true
							ll.Error(err, "error parse ReleaseAfter", "ReleaseAfter", duration.String())
							continue
						}
						now := time.Now()
						if podENI.Status.PodLastSeen.Add(duration).After(now) {
							keep = true
							continue
						}
						// some require to keep
						if keep {
							continue
						}
						ll.Info("fixed ip recycle", "lastSeen", podENI.Status.PodLastSeen.String(), "now", now.String())

						keep = false
					default:
						keep = true
						ll.Error(err, "unsupported ReleaseStrategy %s", alloc.AllocationType.ReleaseStrategy)
						continue
					}
				}
			}
			if keep {
				return
			}

			update := podENI.DeepCopy()
			update.Status.Phase = v1beta1.ENIPhaseDeleting
			_, err = common.UpdatePodENIStatus(ctx, m.client, update)
			if err != nil {
				ll.Error(err, "error prune eni, %s")
			}
		}()
	}
}

// detach detach eni and set status to v1beta1.ENIPhaseUnbind
func (m *ReconcilePodENI) detach(ctx context.Context, podENI *v1beta1.PodENI) (reconcile.Result, error) {
	podENICopy := podENI.DeepCopy()
	err := m.detachMemberENI(ctx, podENICopy)
	if err != nil {
		return reconcile.Result{}, err
	}
	podENICopy.Status.Phase = v1beta1.ENIPhaseUnbind
	podENICopy.Status.InstanceID = ""
	podENICopy.Status.TrunkENIID = ""
	for k, v := range podENICopy.Status.ENIInfos {
		if v.Status == v1beta1.ENIStatusBind {
			cp := v.DeepCopy()
			cp.Status = v1beta1.ENIPhaseUnbind
			podENICopy.Status.ENIInfos[k] = *cp
		}
	}
	_, err = common.UpdatePodENIStatus(ctx, m.client, podENICopy)
	return reconcile.Result{}, err
}

func (m *ReconcilePodENI) attachENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	var err error
	if podENI.Status.InstanceID == "" {
		return fmt.Errorf("instanceID missing")
	}
	if podENI.Status.Phase == v1beta1.ENIPhaseBind {
		return nil
	}
	defer func() {
		if err != nil {
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventAttachENIFailed, err.Error())
		} else {
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventAttachENISucceed, fmt.Sprintf("attached eni %s", strings.Join(allocIDs(podENI), ",")))
		}
	}()

	ch := make(chan *v1beta1.ENIInfo)
	done := make(chan struct{})
	go func() {
		// override the config
		podENI.Status.ENIInfos = make(map[string]v1beta1.ENIInfo)
		for info := range ch {
			podENI.Status.ENIInfos[info.ID] = *info
		}
		done <- struct{}{}
	}()

	g, _ := errgroup.WithContext(context.Background())
	for i := range podENI.Spec.Allocations {
		ii := i
		g.Go(func() error {
			alloc := podENI.Spec.Allocations[ii]
			ctx := common.WithCtx(ctx, &alloc)
			err := m.aliyun.AttachNetworkInterface(ctx, alloc.ENI.ID, podENI.Status.InstanceID, podENI.Status.TrunkENIID)
			if err != nil {
				return err
			}

			eni, err := m.aliyun.WaitForNetworkInterface(ctx, alloc.ENI.ID, aliyunClient.ENIStatusInUse, backoff.Backoff(backoff.WaitENIStatus), false)
			if err != nil {
				return err
			}

			ch <- &v1beta1.ENIInfo{
				ID:     eni.NetworkInterfaceID,
				Type:   v1beta1.ENIType(eni.Type),
				Vid:    eni.DeviceIndex,
				Status: v1beta1.ENIStatusBind,
			}
			return nil
		})
	}
	err = g.Wait()
	close(ch)
	<-done
	return err
}

func (m *ReconcilePodENI) detachMemberENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	var err error
	if podENI.Status.Phase == v1beta1.ENIPhaseUnbind {
		return nil
	}

	defer func() {
		if err != nil {
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventDetachENIFailed, err.Error())
		} else {
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventDetachENISucceed, fmt.Sprintf("detach eni %s", strings.Join(allocIDs(podENI), ",")))
		}
	}()
	for _, alloc := range podENI.Spec.Allocations {
		ctx := common.WithCtx(ctx, &alloc)
		instanceID := podENI.Status.InstanceID
		trunkENIID := podENI.Status.TrunkENIID
		if podENI.Status.InstanceID == "" {
			enis, err := m.aliyun.DescribeNetworkInterface(ctx, "", []string{alloc.ENI.ID}, "", "", "", nil)
			if err != nil {
				return err
			}
			if len(enis) == 0 {
				continue
			}
			if enis[0].InstanceID == "" {
				continue
			}
			instanceID = enis[0].InstanceID
			trunkENIID = enis[0].TrunkNetworkInterfaceID
		}

		err = m.aliyun.DetachNetworkInterface(ctx, alloc.ENI.ID, instanceID, trunkENIID)
		if err != nil {
			return err
		}

		_, err = m.aliyun.WaitForNetworkInterface(ctx, alloc.ENI.ID, aliyunClient.ENIStatusAvailable, backoff.Backoff(backoff.WaitENIStatus), true)
		if err == nil {
			continue
		}
		if errors.Is(err, apiErr.ErrNotFound) {
			continue
		}
		return err
	}

	return nil
}

func (m *ReconcilePodENI) deleteMemberENI(ctx context.Context, podENI *v1beta1.PodENI) (err error) {
	defer func() {
		if err != nil {
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventDeleteENIFailed, err.Error())
		} else {
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventDeleteENISucceed, fmt.Sprintf("delete eni %s", strings.Join(allocIDs(podENI), ",")))
		}
	}()

	for _, alloc := range podENI.Spec.Allocations {
		if alloc.ENI.ID == "" {
			continue
		}
		err = m.aliyun.DeleteNetworkInterface(common.WithCtx(ctx, &alloc), alloc.ENI.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

// eniFilter will compare eni tags with filter, if all filter match return true
func (m *ReconcilePodENI) eniFilter(eni *aliyunClient.NetworkInterface, filter map[string]string) bool {
	for k, v := range filter {
		found := false
		for _, tag := range eni.Tags {
			if tag.TagKey != k {
				continue
			}
			if tag.TagValue != v {
				return false
			}
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func (m *ReconcilePodENI) getNode(ctx context.Context, name string) (*corev1.Node, error) {
	node := &corev1.Node{}
	err := m.client.Get(ctx, k8stypes.NamespacedName{
		Name: name,
	}, node)
	return node, err
}

func (m *ReconcilePodENI) deletePodENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	update := podENI.DeepCopy()
	update.Status.Phase = v1beta1.ENIPhaseDeleting

	_, err := common.UpdatePodENIStatus(ctx, m.client, update)
	return err
}

func allocIDs(podENI *v1beta1.PodENI) []string {
	var ids []string
	for _, alloc := range podENI.Spec.Allocations {
		ids = append(ids, alloc.ENI.ID)
	}
	return ids
}

func podRequirePodENI(pod *corev1.Pod, ignore bool) bool {
	if utils.PodSandboxExited(pod) {
		return false
	}

	if ignore {
		return true
	}

	if !types.PodUseENI(pod) {
		return false
	}

	return true
}
