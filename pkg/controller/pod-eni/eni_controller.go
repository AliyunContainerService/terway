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

package podeni

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var ctrlLog = ctrl.Log.WithName(controllerName)

const controllerName = "pod-eni"

func init() {
	register.Add(controllerName, func(mgr manager.Manager, aliyunClient *aliyun.OpenAPI, swPool *vswitch.SwitchPool) error {
		r := NewReconcilePod(mgr, aliyunClient)
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
			&source.Kind{
				Type: &v1beta1.PodENI{},
			},
			&handler.EnqueueRequestForObject{},
			&predicate.ResourceVersionChangedPredicate{},
			&predicateForPodENIEvent{},
		)
	})
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
	aliyun *aliyun.OpenAPI

	//record event recorder
	record record.EventRecorder

	trunkMode bool // use trunk mode or secondary eni mode
}

type Wrapper struct {
	ctrl controller.Controller
	r    *ReconcilePodENI
}

// Start the controller
func (w *Wrapper) Start(ctx context.Context) error {
	// on start do clean up
	w.r.gcSecondaryENI(true)
	w.r.gcMemberENI(true)

	// start the gc process
	w.r.gc(ctx.Done())

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
func NewReconcilePod(mgr manager.Manager, aliyunClient *aliyun.OpenAPI) *ReconcilePodENI {
	r := &ReconcilePodENI{
		client:    mgr.GetClient(),
		scheme:    mgr.GetScheme(),
		record:    mgr.GetEventRecorderFor("PodENI"),
		aliyun:    aliyunClient,
		trunkMode: *controlplane.GetConfig().EnableTrunk,
	}
	return r
}

// Reconcile all podENI resource
// podENI create -> do attach to node and update status
// podENI delete -> detach podENI and delete
func (m *ReconcilePodENI) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.Info("Reconcile")

	podENI := &v1beta1.PodENI{}
	err := m.client.Get(context.Background(), request.NamespacedName, podENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !podENI.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(podENI, types.FinalizerPodENI) {
			return reconcile.Result{}, nil
		}
		return m.podENIDelete(ctx, podENI)
	}
	return m.podENICreate(ctx, request.NamespacedName, podENI)
}

// NeedLeaderElection need election
func (m *ReconcilePodENI) NeedLeaderElection() bool {
	return true
}

// gc will handle following circumstances
// 1. cr podENI is leaked
// 2. release fixed ip resource by strategy
func (m *ReconcilePodENI) gc(stopCh <-chan struct{}) {
	go wait.Until(func() {
		m.gcSecondaryENI(false)
	}, leakedENICheckPeriod, stopCh)

	go wait.Until(func() {
		m.gcMemberENI(false)
	}, leakedENICheckPeriod, stopCh)

	go wait.Until(m.gcCRPodENIs, podENICheckPeriod, stopCh)
}

func (m *ReconcilePodENI) podENICreate(ctx context.Context, namespacedName client.ObjectKey, podENI *v1beta1.PodENI) (result reconcile.Result, err error) {
	l := log.FromContext(ctx)
	l.Info("podENI create")

	switch podENI.Status.Phase {
	case v1beta1.ENIPhaseBind:
		l.V(5).Info("already bind")
		return reconcile.Result{}, nil
	case v1beta1.ENIPhaseUnbind:
		l.V(5).Info("already unbind")
		return reconcile.Result{}, nil
	case v1beta1.ENIPhaseDeleting:
		// for pod require to unbind eni
		defer func() {
			if err != nil {
				m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventDetachENIFailed, "%s", err.Error())
			}
		}()
		return m.detach(ctx, podENI)
	case v1beta1.ENIPhaseInitial, v1beta1.ENIPhaseBinding: // pod first create or rebind
		// for pod require to unbind eni
		defer func() {
			if err != nil {
				m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventAttachENIFailed, "%s", err.Error())
			}
		}()

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
			return reconcile.Result{}, fmt.Errorf("trunk eni id not found, this may dure to terway agent is not started")
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
		ll.Info("attach")

		podENICopy.Status.Phase = v1beta1.ENIPhaseBind
		podENICopy.Status.PodLastSeen = metav1.Now()

		_, err = common.SetPodENIStatus(ctx, m.client, podENICopy, podENI)
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
	err = m.deleteMemberENI(podENICopy)
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

func (m *ReconcilePodENI) gcSecondaryENI(force bool) {
	// 1. list all available enis ( which type is secondary)
	enis, err := m.aliyun.DescribeNetworkInterface(context.Background(), controlplane.GetConfig().VPCID, nil, "", aliyun.ENITypeSecondary, aliyun.ENIStatusAvailable)
	if err != nil {
		ctrlLog.Error(err, "error list all member enis")
		return
	}
	if len(enis) == 0 {
		return
	}

	err = m.gcENIs(enis, force)
	if err != nil {
		ctrlLog.Error(err, "error gc enis")
		return
	}
}

func (m *ReconcilePodENI) gcMemberENI(force bool) {
	// 1. list all attached member eni
	enis, err := m.aliyun.DescribeNetworkInterface(context.Background(), controlplane.GetConfig().VPCID, nil, "", aliyun.ENITypeMember, aliyun.ENIStatusInUse)
	if err != nil {
		ctrlLog.Error(err, "error list all member enis")
		return
	}
	if len(enis) == 0 {
		return
	}

	err = m.gcENIs(enis, force)
	if err != nil {
		ctrlLog.Error(err, "error gc enis")
		return
	}
}

func (m *ReconcilePodENI) gcENIs(enis []ecs.NetworkInterfaceSet, force bool) error {
	l := ctrl.Log.WithName("gc-enis")

	eniMap := make(map[string]*ecs.NetworkInterfaceSet, len(enis))

	// 1. filter out eni which is created by terway
	tagFilter := map[string]string{
		types.TagKeyClusterID:               controlplane.GetConfig().ClusterID,
		types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
	}
	layout := "2006-01-02T15:04:05Z"
	now := time.Now()
	for _, eni := range enis {
		if !m.eniFilter(eni, tagFilter) {
			continue
		}
		if !force {
			t, err := time.Parse(layout, eni.CreationTime)
			if err != nil {
				l.Error(err, "error parse eni create time")
				continue
			}
			// avoid conflict with create process
			if t.Add(10 * time.Minute).After(now) {
				continue
			}
		}
		eniMap[eni.NetworkInterfaceId] = &eni
	}
	if len(eniMap) == 0 {
		return nil
	}

	// 2. list cr get all using eni
	podENIs := &v1beta1.PodENIList{}
	err := m.client.List(context.Background(), podENIs)
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
	for id, eni := range eniMap {
		if eni.Type == string(aliyun.ENITypeMember) && eni.Status == string(aliyun.ENIStatusInUse) {
			l.Info("detach eni", "eni", id, "trunk-eni", eni.Attachment.TrunkNetworkInterfaceId)
			err = m.aliyun.DetachNetworkInterface(context.Background(), id, eni.Attachment.InstanceId, eni.Attachment.TrunkNetworkInterfaceId)
			if err != nil {
				l.Error(err, fmt.Sprintf("errot detach eni %s", id))
			}
			// we continue here because we can delete eni in next check
			continue
		}
		if eni.Status == string(aliyun.ENIStatusAvailable) {
			l.Info("delete eni", "eni", id)
			err = m.aliyun.DeleteNetworkInterface(context.Background(), id)
			if err != nil {
				l.Info(fmt.Sprintf("delete leaked eni %s, %s", id, err))
			}
			continue
		}
	}
	return nil
}

// gcCRPodENIs remove useless cr res
func (m *ReconcilePodENI) gcCRPodENIs() {
	l := ctrl.Log.WithName("gc-podENI")

	podENIs := &v1beta1.PodENIList{}
	err := m.client.List(context.Background(), podENIs)
	if err != nil {
		l.Error(err, "error list cr pod enis")
		return
	}

	// 1. found the pod relate to cr
	// 2. release res if pod is not present and not use fixed ip
	// 3. clean fixed ip cr
	for _, podENI := range podENIs.Items {
		msg := fmt.Sprintf("cr %s/%s", podENI.Namespace, podENI.Name)

		p := &corev1.Pod{}
		err = m.client.Get(context.Background(), k8stypes.NamespacedName{
			Namespace: podENI.Namespace,
			Name:      podENI.Name,
		}, p)
		if err != nil && !k8sErr.IsNotFound(err) {
			l.Error(err, "error get pod, %s")
			continue
		}
		ll := l.WithValues("pod", k8stypes.NamespacedName{
			Namespace: podENI.Namespace,
			Name:      podENI.Name,
		}.String())

		// pod exist just update timestamp
		if err == nil {
			if !types.PodUseENI(p) {
				// for pod not using pod ENI will delete it
				err = m.client.Delete(context.Background(), &podENI)
				ll.WithValues("eni", podENI.Spec.Allocations[0].ENI.ID).Info("prune eni pod is not using trunk")
				if err != nil {
					ll.Error(err, "error prune eni, %s")
				}
				continue
			}
			if utils.IsJobPod(p) && utils.PodSandboxExited(p) {
				// for Job kind pod remove after job is done
				err = m.client.Delete(context.Background(), &podENI)
				ll.WithValues("eni", strings.Join(allocIDs(&podENI), ",")).Info("prune eni pod is exited")
				if err != nil {
					ll.Error(err, "error prune eni, %s")
				}
				continue
			}

			ll.V(5).Info("update pod lastSeen to now")
			update := podENI.DeepCopy()
			update.Status.PodLastSeen = metav1.Now()
			_, err = common.SetPodENIStatus(context.Background(), m.client, update, &podENI)
			if err != nil {
				ll.Error(err, "error update timestamp, %s", msg)
			}
			continue
		}

		// fixme need clean up eni other than delete podENI
		keep := true
		// pod not exist
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
						ll.Error(err, "error parse ReleaseAfter, %s", msg)
						continue
					}
					if duration < 0 {
						keep = true
						ll.Error(err, "error parse ReleaseAfter %s, %s", duration.String(), msg)
						continue
					}
					now := time.Now()
					if podENI.Status.PodLastSeen.Add(duration).After(now) {
						keep = true
						continue
					}
					l.Info("fixed ip recycle", "lastSeen", podENI.Status.PodLastSeen.String(), "now", now.String())
				default:
					keep = true
					ll.Error(err, "unsupported ReleaseStrategy %s", alloc.AllocationType.ReleaseStrategy)
					continue
				}
			}
		}
		if keep {
			continue
		}
		err = m.client.Delete(context.Background(), &podENI)
		ll.WithValues("eni", strings.Join(allocIDs(&podENI), ",")).Info("prune eni")
		if err != nil {
			ll.Error(err, "error prune eni, %s")
		}
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
	_, err = common.SetPodENIStatus(ctx, m.client, podENICopy, podENI)
	return reconcile.Result{}, err
}

func (m *ReconcilePodENI) attachENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	var err error
	if podENI.Status.InstanceID == "" || podENI.Status.Phase == v1beta1.ENIPhaseBind {
		return nil
	}
	defer func() {
		if err != nil {
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventAttachENIFailed, err.Error())
		} else {
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventAttachENISucceed, fmt.Sprintf("attach eni %s", strings.Join(allocIDs(podENI), ",")))
		}
	}()

	for _, alloc := range podENI.Spec.Allocations {
		err = m.aliyun.AttachNetworkInterface(ctx, alloc.ENI.ID, podENI.Status.InstanceID, podENI.Status.TrunkENIID)
		if err != nil {
			return err
		}

		time.Sleep(aliyun.ENIOpBackoff.Duration)

		var eni *ecs.NetworkInterfaceSet
		eni, err = m.aliyun.WaitForNetworkInterface(ctx, alloc.ENI.ID, aliyun.ENIStatusInUse, aliyun.ENIOpBackoff, false)
		if err != nil {
			return err
		}

		podENI.Status.ENIInfos[eni.NetworkInterfaceId] = v1beta1.ENIInfo{
			ID:     eni.NetworkInterfaceId,
			Type:   v1beta1.ENIType(eni.Type),
			Vid:    eni.Attachment.DeviceIndex,
			Status: v1beta1.ENIStatusBind,
		}
	}
	return nil
}

func (m *ReconcilePodENI) detachMemberENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	var err error
	if podENI.Status.InstanceID == "" || podENI.Status.Phase == v1beta1.ENIPhaseUnbind {
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
		err = m.aliyun.DetachNetworkInterface(ctx, alloc.ENI.ID, podENI.Status.InstanceID, podENI.Status.TrunkENIID)
		if err != nil {
			return err
		}
		time.Sleep(aliyun.ENIOpBackoff.Duration)

		_, err = m.aliyun.WaitForNetworkInterface(ctx, alloc.ENI.ID, aliyun.ENIStatusAvailable, aliyun.ENIOpBackoff, true)
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

func (m *ReconcilePodENI) deleteMemberENI(podENI *v1beta1.PodENI) (err error) {
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

		err = m.aliyun.DeleteNetworkInterface(context.Background(), alloc.ENI.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

// eniFilter will compare eni tags with filter, if all filter match return true
func (m *ReconcilePodENI) eniFilter(eni ecs.NetworkInterfaceSet, filter map[string]string) bool {
	for k, v := range filter {
		found := false
		for _, tag := range eni.Tags.Tag {
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

func allocIDs(podENI *v1beta1.PodENI) []string {
	var ids []string
	for _, alloc := range podENI.Spec.Allocations {
		ids = append(ids, alloc.ENI.ID)
	}
	return ids
}
