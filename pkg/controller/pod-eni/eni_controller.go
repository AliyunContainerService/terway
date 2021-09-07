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
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/spf13/viper"
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
			MaxConcurrentReconciles: viper.GetInt("podeni-max-concurrent-reconciles"),
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
		client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
		record: mgr.GetEventRecorderFor("PodENI"),
		aliyun: aliyunClient,
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
		return m.podENIDelete(ctx, request.NamespacedName, podENI)
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

func (m *ReconcilePodENI) podENICreate(ctx context.Context, namespacedName client.ObjectKey, podENI *v1beta1.PodENI) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.Info("podENI create")

	switch podENI.Status.Status {
	case v1beta1.ENIStatusBind:
		l.V(5).Info("already bind", "eni", podENI.Spec.Allocation.ENI.ID)
		return reconcile.Result{}, nil
	case v1beta1.ENIStatusUnbind:
		l.V(5).Info("already unbind", "eni", podENI.Spec.Allocation.ENI.ID)
		return reconcile.Result{}, nil
	case v1beta1.ENIStatusDeleting:
		// for pod require to unbind eni
		return m.detach(ctx, podENI)
	case v1beta1.ENIStatusInitial, v1beta1.ENIStatusBinding: // pod first create or rebind
		pod := &corev1.Pod{}
		err := m.client.Get(context.Background(), namespacedName, pod)
		if err != nil {
			return reconcile.Result{}, err
		}

		node, err := m.getNode(ctx, pod.Spec.NodeName)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error get node %s, %w", pod.Spec.NodeName, err)
		}

		podConf := &common.PodConf{}
		err = podConf.SetPodENIConf(podENI)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error parse podENI, %w", err)
		}
		err = podConf.SetNodeConf(node)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error parse node, %w", err)
		}

		attach := false
		if podENI.Status.Status == "" {
			attach = true
		} else {
			if utils.IsStsPod(pod) && podConf.UseFixedIP {
				attach = true
			}
		}
		if !attach {
			// user need delete podENI or wait GC finished
			return reconcile.Result{}, fmt.Errorf("found previous podENI, but pod is not using fixed ip")
		}

		podENICopy := podENI.DeepCopy()
		podENICopy.Status.InstanceID = podConf.InstanceID
		podENICopy.Status.TrunkENIID = podConf.TrunkENIID

		ll := l.WithValues("eni", podENICopy.Spec.Allocation.ENI.ID, "trunk", podENICopy.Status.TrunkENIID, "instance", podENICopy.Status.InstanceID)

		err = m.attachMemberENI(podENICopy)
		if err != nil {
			ll.Error(err, "attach eni failed")
			return reconcile.Result{}, err
		}
		ll.Info("attach")

		podENICopy.Status.Status = v1beta1.ENIStatusBind
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

func (m *ReconcilePodENI) podENIDelete(ctx context.Context, namespacedName client.ObjectKey, podENI *v1beta1.PodENI) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.Info("podENI delete")

	oldPodENI := &v1beta1.PodENI{}
	err := m.client.Get(context.Background(), namespacedName, oldPodENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	podENICopy := oldPodENI.DeepCopy()

	// detach eni
	err = m.detachMemberENI(podENICopy)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error detachMemberENI podENI status to %s", v1beta1.ENIStatusUnbind)
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
	enis, err := m.aliyun.DescribeNetworkInterface(context.Background(), viper.GetString("vpc-id"), nil, "", aliyun.ENITypeSecondary, aliyun.ENIStatusAvailable)
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
	enis, err := m.aliyun.DescribeNetworkInterface(context.Background(), viper.GetString("vpc-id"), nil, "", aliyun.ENITypeMember, aliyun.ENIStatusInUse)
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
	l.Info("checking")

	eniMap := make(map[string]*ecs.NetworkInterfaceSet, len(enis))

	// 1. filter out eni which is created by terway
	tagFilter := map[string]string{
		types.TagKeyClusterID:               viper.GetString("cluster-id"),
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
		_, ok := eniMap[podENI.Spec.Allocation.ENI.ID]
		if !ok {
			continue
		}
		delete(eniMap, podENI.Spec.Allocation.ENI.ID)
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
	l.Info("checking")

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
				ll.WithValues("eni", podENI.Spec.Allocation.ENI.ID).Info("prune eni pod is not using trunk")
				if err != nil {
					ll.Error(err, "error prune eni, %s")
				}
				continue
			}
			if utils.IsJobPod(p) && utils.PodSandboxExited(p) {
				// for Job kind pod remove after job is done
				err = m.client.Delete(context.Background(), &podENI)
				ll.WithValues("eni", podENI.Spec.Allocation.ENI.ID).Info("prune eni pod is exited")
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

		// pod not exist
		if podENI.Spec.Allocation.IPType.Type == v1beta1.IPAllocTypeFixed {
			switch podENI.Spec.Allocation.IPType.ReleaseStrategy {
			case v1beta1.ReleaseStrategyNever:
				continue
			case v1beta1.ReleaseStrategyTTL:
				duration, err := time.ParseDuration(podENI.Spec.Allocation.IPType.ReleaseAfter)
				if err != nil {
					ll.Error(err, "error parse ReleaseAfter, %s", msg)
					continue
				}
				if duration < 0 {
					ll.Error(err, "error parse ReleaseAfter %s, %s", duration.String(), msg)
					continue
				}
				now := time.Now()
				if podENI.Status.PodLastSeen.Add(duration).After(now) {
					continue
				}
				l.Info("fixed ip recycle", "lastSeen", podENI.Status.PodLastSeen.String(), "now", now.String())
			default:
				ll.Error(err, "unsupported ReleaseStrategy %s", podENI.Spec.Allocation.IPType.ReleaseStrategy)
				continue
			}
		}

		err = m.client.Delete(context.Background(), &podENI)
		ll.WithValues("eni", podENI.Spec.Allocation.ENI.ID).Info("prune eni")
		if err != nil {
			ll.Error(err, "error prune eni, %s")
		}
	}
}

// detach detach eni and set status to v1beta1.ENIStatusUnbind
func (m *ReconcilePodENI) detach(ctx context.Context, podENI *v1beta1.PodENI) (reconcile.Result, error) {
	podENICopy := podENI.DeepCopy()
	err := m.detachMemberENI(podENICopy)
	if err != nil {
		return reconcile.Result{}, err
	}
	podENICopy.Status.Status = v1beta1.ENIStatusUnbind
	podENICopy.Status.InstanceID = ""
	podENICopy.Status.TrunkENIID = ""
	_, err = common.SetPodENIStatus(ctx, m.client, podENICopy, podENI)
	return reconcile.Result{}, err
}

func (m *ReconcilePodENI) attachMemberENI(podENI *v1beta1.PodENI) (err error) {
	if podENI.Status.InstanceID == "" || podENI.Status.Status == v1beta1.ENIStatusBind {
		return nil
	}
	defer func() {
		if err != nil {
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventAttachENIFailed, err.Error())
		} else {
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventAttachENISucceed, fmt.Sprintf("attach eni %s", podENI.Spec.Allocation.ENI.ID))
		}
	}()
	err = m.aliyun.AttachNetworkInterface(context.Background(), podENI.Spec.Allocation.ENI.ID, podENI.Status.InstanceID, podENI.Status.TrunkENIID)
	if err != nil {
		return err
	}
	time.Sleep(aliyun.ENIOpBackoff.Duration)
	_, err = m.aliyun.WaitForNetworkInterface(context.Background(), podENI.Spec.Allocation.ENI.ID, aliyun.ENIStatusInUse, aliyun.ENIOpBackoff, false)
	return err
}

func (m *ReconcilePodENI) detachMemberENI(podENI *v1beta1.PodENI) (err error) {
	if podENI.Status.InstanceID == "" || podENI.Status.Status == v1beta1.ENIStatusUnbind {
		return nil
	}
	defer func() {
		if err != nil {
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventDetachENIFailed, err.Error())
		} else {
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventDetachENISucceed, fmt.Sprintf("detach eni %s", podENI.Spec.Allocation.ENI.ID))
		}
	}()
	err = m.aliyun.DetachNetworkInterface(context.Background(), podENI.Spec.Allocation.ENI.ID, podENI.Status.InstanceID, podENI.Status.TrunkENIID)
	if err != nil {
		return err
	}
	time.Sleep(aliyun.ENIOpBackoff.Duration)
	_, err = m.aliyun.WaitForNetworkInterface(context.Background(), podENI.Spec.Allocation.ENI.ID, aliyun.ENIStatusAvailable, aliyun.ENIOpBackoff, true)
	if errors.Is(err, apiErr.ErrNotFound) {
		return nil
	}
	return err
}

func (m *ReconcilePodENI) deleteMemberENI(podENI *v1beta1.PodENI) (err error) {
	if podENI.Spec.Allocation.ENI.ID == "" {
		return nil
	}
	defer func() {
		if err != nil {
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventDeleteENIFailed, err.Error())
		} else {
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventDeleteENISucceed, fmt.Sprintf("delete eni %s", podENI.Spec.Allocation.ENI.ID))
		}
	}()
	return m.aliyun.DeleteNetworkInterface(context.Background(), podENI.Spec.Allocation.ENI.ID)
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
