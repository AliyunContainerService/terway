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
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
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
		r := NewReconcilePod(mgr, aliyunClient, swPool)
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

		err = c.Watch(
			&source.Kind{
				Type: &corev1.Pod{},
			},
			&handler.EnqueueRequestForObject{},
			&predicate.ResourceVersionChangedPredicate{},
			&predicateForPodEvent{},
		)
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

// ReconcilePod implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePod{}

// ReconcilePod reconciles a AutoRepair object
type ReconcilePod struct {
	client client.Client
	scheme *runtime.Scheme
	aliyun *aliyun.OpenAPI

	swPool *vswitch.SwitchPool

	//record event recorder
	record record.EventRecorder
}

type Wrapper struct {
	ctrl controller.Controller
	r    *ReconcilePod
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
func NewReconcilePod(mgr manager.Manager, aliyunClient *aliyun.OpenAPI, swPool *vswitch.SwitchPool) *ReconcilePod {
	r := &ReconcilePod{
		client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
		record: mgr.GetEventRecorderFor("PodENI"),
		aliyun: aliyunClient,
		swPool: swPool,
	}
	return r
}

func (m *ReconcilePod) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.Info("Reconcile")

	pod := &corev1.Pod{}
	err := m.client.Get(context.Background(), request.NamespacedName, pod)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, m.podDelete(ctx, request.NamespacedName)
		}
		return reconcile.Result{}, err
	}
	if pod.DeletionTimestamp != nil {
		return reconcile.Result{}, m.podDelete(ctx, request.NamespacedName)
	}

	return reconcile.Result{}, m.podCreate(ctx, pod)
}

// NeedLeaderElection need election
func (m *ReconcilePod) NeedLeaderElection() bool {
	return true
}

// gc will handle following circumstances
// 1. cr podENI is leaked
// 2. release fixed ip resource by strategy
func (m *ReconcilePod) gc(stopCh <-chan struct{}) {
	go wait.Until(func() {
		m.gcSecondaryENI(false)
	}, leakedENICheckPeriod, stopCh)

	go wait.Until(func() {
		m.gcMemberENI(false)
	}, leakedENICheckPeriod, stopCh)

	go wait.Until(m.gcCRPodENIs, podENICheckPeriod, stopCh)
}

// podCreate is the func when the pod is to be create or created
func (m *ReconcilePod) podCreate(ctx context.Context, pod *corev1.Pod) error {
	l := log.FromContext(ctx)
	l.Info("pod create")

	// 1. check podENI status

	prePodENI := &v1beta1.PodENI{}
	err := m.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, prePodENI)

	if err == nil {
		p := prePodENI.DeepCopy()
		switch p.Status.Status {
		case v1beta1.ENIStatusBind:
			l.Info("already bind", "eni", p.Spec.Allocation.ENI.ID)
			return nil
		case v1beta1.ENIStatusDeleting:
			l.Info(fmt.Sprintf("found podENI status %s, prune now", v1beta1.ENIStatusDeleting))
			err = m.pruneENI(ctx, p)
		case "":
			l.Info("found podENI without status, prune now")
			err = m.pruneENI(ctx, p)
		case v1beta1.ENIStatusUnbind:
			node, err := m.getNode(ctx, pod.Spec.NodeName)
			if err != nil {
				return fmt.Errorf("error get node %s, %w", pod.Spec.NodeName, err)
			}
			podConf := &PodConf{}
			err = podConf.SetPodENIConf(prePodENI)
			if err != nil {
				return fmt.Errorf("error parse podENI, %w", err)
			}
			err = podConf.SetNodeConf(node)
			if err != nil {
				return fmt.Errorf("error parse node, %w", err)
			}

			// for pod with fixed ip ,reuse previous eni
			if utils.IsStsPod(pod) && podConf.UseFixedIP {
				l.Info("reuse eni", "eni", podConf.TrunkENIID)

				p.Status.InstanceID = podConf.InstanceID
				p.Status.TrunkENIID = podConf.TrunkENIID
				err = m.attachMemberENI(p)
				if err != nil {
					l.Error(err, "error attach eni")
					return err
				}
				p.Status.Status = v1beta1.ENIStatusBind
				_, err = m.setPodENIStatus(ctx, p, prePodENI)

				return err
			}
			return fmt.Errorf("found previous podENI, but pod is not using fixed ip")
		default:
			return fmt.Errorf("found cr with unknown status %s", prePodENI.Status.Status)
		}
	}
	if err != nil {
		if !k8sErr.IsNotFound(err) {
			return err
		}
	}

	// 2. cr is not found , so we will create new
	l.Info("creating eni")

	// 2.1 fill config
	podConf := &PodConf{}
	node, err := m.getNode(ctx, pod.Spec.NodeName)
	if err != nil {
		return fmt.Errorf("error get node %s, %w", pod.Spec.NodeName, err)
	}
	err = podConf.SetNodeConf(node)
	if err != nil {
		return fmt.Errorf("error parse node, %w", err)
	}

	podNetwokingName := pod.Annotations[types.PodNetworking]

	podNetworking := &v1beta1.PodNetworking{}
	err = m.client.Get(ctx, k8stypes.NamespacedName{
		Name: podNetwokingName,
	}, podNetworking)
	if err != nil {
		return fmt.Errorf("error get podNetworking %s", podNetwokingName)
	}

	err = podConf.SetPodNetworkingConf(podNetworking)
	if err != nil {
		return err
	}

	vsw, err := m.swPool.GetOne(podConf.Zone, sets.NewString(podNetworking.Spec.VSwitchIDs...))
	if err != nil {
		return fmt.Errorf("cat not found available vSwitch for zone %s", podConf.Zone)
	}
	podConf.VSwitchID = vsw

	podENI := &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}

	// 2.2 create eni
	eni, err := m.aliyun.CreateNetworkInterface(aliyun.ENITypeSecondary, podConf.VSwitchID, podConf.SecurityGroups, 1, map[string]string{
		types.TagKeyClusterID:               viper.GetString("cluster-id"),
		types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
		types.TagKubernetesPodName:          utils.TrimStr(pod.Name, 120),
		types.TagKubernetesPodNamespace:     utils.TrimStr(pod.Namespace, 120),
	})
	if err != nil {
		return err
	}

	podENI.Spec.Allocation = v1beta1.Allocation{
		ENI: v1beta1.ENI{
			ID:   eni.NetworkInterfaceId,
			MAC:  eni.MacAddress,
			Zone: eni.ZoneId,
		},
		IPv4: eni.PrivateIpAddress,
		IPv6: "",
		IPType: v1beta1.IPType{
			Type:            podConf.IPAllocType,
			ReleaseStrategy: podConf.ReleaseStrategy,
			ReleaseAfter:    podConf.FixedIPReleaseAfter.String(),
		},
	}

	defer func() {
		if err != nil {
			podENI.Status.Status = v1beta1.ENIStatusUnbind
			innerErr := m.deleteMemberENI(podENI)
			if innerErr != nil {
				l.Error(innerErr, "error delete eni %w")
			}
		}
	}()
	l.Info("attaching eni")

	// 2.3 attach eni
	err = m.aliyun.AttachNetworkInterface(eni.NetworkInterfaceId, podConf.InstanceID, podConf.TrunkENIID)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			innerErr := m.detachMemberENI(podENI)
			if innerErr != nil {
				l.Error(innerErr, "error detach eni %w")
			}
		}
	}()

	// 2.4 wait attached
	_, err = m.aliyun.WaitForNetworkInterface(eni.NetworkInterfaceId, aliyun.ENIStatusInUse, aliyun.ENIOpBackoff, false)
	if err != nil {
		l.Error(err, "error wait eni status")
		return err
	}

	// 2.5 create cr & update status
	// 1. del cr and roll back eni failed , which status is empty
	// 2. del cr failed and roll back eni success,
	err = m.client.Create(ctx, podENI)
	if err != nil {
		return fmt.Errorf("error create cr, %s", err)
	}
	defer func() {
		if err != nil {
			innerErr := m.client.Delete(ctx, podENI)
			if innerErr != nil {
				if k8sErr.IsNotFound(innerErr) {
					return
				}
				l.Error(innerErr, fmt.Sprintf("error delete cr %s/%s", pod.Namespace, pod.Name))
			}
		}
	}()

	old := podENI.DeepCopy()
	podENI.Status = v1beta1.PodENIStatus{
		Status:      v1beta1.ENIStatusBind,
		InstanceID:  podConf.InstanceID,
		TrunkENIID:  podConf.TrunkENIID,
		PodLastSeen: metav1.Now(),
	}
	_, err = m.setPodENIStatus(ctx, podENI, old)
	if err != nil {
		l.Error(err, "error update podENI status")
		return nil
	}
	l.WithValues("eni", eni.NetworkInterfaceId).Info("success associate eni")
	return nil
}

func (m *ReconcilePod) podDelete(ctx context.Context, namespacedName client.ObjectKey) error {
	l := log.FromContext(ctx)
	l.Info("pod delete")
	oldPodENI := &v1beta1.PodENI{}
	err := m.client.Get(context.Background(), namespacedName, oldPodENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			l.Info("cr resource not found")
			return nil
		}
		return err
	}
	podENICopy := oldPodENI.DeepCopy()

	podConf := &PodConf{}
	err = podConf.SetPodENIConf(podENICopy)
	if err != nil {
		return fmt.Errorf("error parse podENI, %w", err)
	}
	// for fixed ip ,we will not delete eni
	if podConf.UseFixedIP {
		err := m.detachMemberENI(podENICopy)
		if err != nil {
			return err
		}
		podENICopy.Status.Status = v1beta1.ENIStatusUnbind
		_, err = m.setPodENIStatus(ctx, podENICopy, oldPodENI)
		return err
	}

	// for non-fixed ip, we will detach then delete eni
	return m.pruneENI(ctx, podENICopy)
}

// pruneENI is the func release eni resource and cr
func (m *ReconcilePod) pruneENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	old := podENI.DeepCopy()
	podENI.Status.PodLastSeen = metav1.Unix(0, 0) // avoid del failed
	podENI.Status.Status = v1beta1.ENIStatusDeleting
	_, err := m.setPodENIStatus(ctx, podENI, old)
	if err != nil {
		return err
	}
	// detach eni
	err = m.detachMemberENI(podENI)
	if err != nil {
		return fmt.Errorf("error detachMemberENI podENI status to %s", v1beta1.ENIStatusUnbind)
	}
	// delete eni
	err = m.deleteMemberENI(podENI)
	if err != nil {
		return fmt.Errorf("error delete eni, %w", err)
	}
	err = m.client.Delete(ctx, podENI)
	if k8sErr.IsNotFound(err) {
		return nil
	}
	return err
}

func (m *ReconcilePod) gcSecondaryENI(force bool) {
	// 1. list all available enis ( which type is secondary)
	enis, err := m.aliyun.DescribeNetworkInterface(viper.GetString("vpc-id"), nil, "", aliyun.ENITypeSecondary, aliyun.ENIStatusAvailable)
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

func (m *ReconcilePod) gcMemberENI(force bool) {
	// 1. list all attached member eni
	enis, err := m.aliyun.DescribeNetworkInterface(viper.GetString("vpc-id"), nil, "", aliyun.ENITypeMember, aliyun.ENIStatusInUse)
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

func (m *ReconcilePod) gcENIs(enis []ecs.NetworkInterfaceSet, force bool) error {
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
			err = m.aliyun.DetachNetworkInterface(id, eni.Attachment.InstanceId, eni.Attachment.TrunkNetworkInterfaceId)
			if err != nil {
				l.Error(err, fmt.Sprintf("errot detach eni %s", id))
			}
			// we continue here because we can delete eni in next check
			continue
		}
		if eni.Status == string(aliyun.ENIStatusAvailable) {
			l.Info("delete eni", "eni", id)
			err = m.aliyun.DeleteNetworkInterface(id)
			if err != nil {
				l.Info(fmt.Sprintf("delete leaked eni %s, %s", id, err))
			}
			continue
		}
	}
	return nil
}

// gcCRPodENIs remove useless cr res
func (m *ReconcilePod) gcCRPodENIs() {
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
			ll.V(5).Info("update pod lastSeen to now")
			update := podENI.DeepCopy()
			update.Status.PodLastSeen = metav1.Now()
			_, err = m.setPodENIStatus(context.Background(), update, &podENI)
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

		ll.WithValues("eni", podENI.Spec.Allocation.ENI.ID).Info("prune eni")
		err := m.pruneENI(context.Background(), &podENI)
		if err != nil {
			ll.Error(err, "error prune eni, %s")
		}
	}
}

func (m *ReconcilePod) attachMemberENI(podENI *v1beta1.PodENI) error {
	if podENI.Status.InstanceID == "" || podENI.Status.Status == v1beta1.ENIStatusBind {
		return nil
	}
	err := m.aliyun.AttachNetworkInterface(podENI.Spec.Allocation.ENI.ID, podENI.Status.InstanceID, podENI.Status.TrunkENIID)
	if err != nil {
		return err
	}
	time.Sleep(aliyun.ENIOpBackoff.Duration)
	_, err = m.aliyun.WaitForNetworkInterface(podENI.Spec.Allocation.ENI.ID, aliyun.ENIStatusInUse, aliyun.ENIOpBackoff, false)
	return err
}

func (m *ReconcilePod) detachMemberENI(podENI *v1beta1.PodENI) error {
	if podENI.Status.InstanceID == "" || podENI.Status.Status == v1beta1.ENIStatusUnbind {
		return nil
	}
	err := m.aliyun.DetachNetworkInterface(podENI.Spec.Allocation.ENI.ID, podENI.Status.InstanceID, podENI.Status.TrunkENIID)
	if err != nil {
		return err
	}
	time.Sleep(aliyun.ENIOpBackoff.Duration)
	_, err = m.aliyun.WaitForNetworkInterface(podENI.Spec.Allocation.ENI.ID, aliyun.ENIStatusAvailable, aliyun.ENIOpBackoff, true)
	if errors.Is(err, apiErr.ErrNotFound) {
		return nil
	}
	return err
}

func (m *ReconcilePod) deleteMemberENI(podENI *v1beta1.PodENI) error {
	if podENI.Spec.Allocation.ENI.ID == "" {
		return nil
	}
	return m.aliyun.DeleteNetworkInterface(podENI.Spec.Allocation.ENI.ID)
}

// setPodENIStatus set ccr status
func (m *ReconcilePod) setPodENIStatus(ctx context.Context, update, old *v1beta1.PodENI) (*v1beta1.PodENI, error) {
	var eni *v1beta1.PodENI
	var err error
	for i := 0; i < 2; i++ {
		err = m.client.Status().Patch(ctx, update, client.MergeFrom(old))
		if err != nil {
			continue
		}
		return eni, nil
	}
	return nil, err
}

// eniFilter will compare eni tags with filter, if all filter match return true
func (m *ReconcilePod) eniFilter(eni ecs.NetworkInterfaceSet, filter map[string]string) bool {
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

func (m *ReconcilePod) getNode(ctx context.Context, name string) (*corev1.Node, error) {
	node := &corev1.Node{}
	err := m.client.Get(ctx, k8stypes.NamespacedName{
		Name: name,
	}, node)
	return node, err
}
