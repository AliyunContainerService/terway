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

package pod

import (
	"context"
	"fmt"
	"strings"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName = "pod"

func init() {
	register.Add(controllerName, func(mgr manager.Manager, aliyunClient register.Interface, swPool *vswitch.SwitchPool) error {
		r := NewReconcilePod(mgr, aliyunClient, swPool)
		c, err := controller.NewUnmanaged(controllerName, mgr, controller.Options{
			Reconciler:              r,
			MaxConcurrentReconciles: controlplane.GetConfig().PodMaxConcurrent,
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
				Type: &corev1.Pod{},
			},
			&handler.EnqueueRequestForObject{},
			&predicate.ResourceVersionChangedPredicate{},
			&predicateForPodEvent{},
		)
	})
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
}

type Wrapper struct {
	ctrl controller.Controller
	r    *ReconcilePod
}

// Start the controller
func (w *Wrapper) Start(ctx context.Context) error {
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
func NewReconcilePod(mgr manager.Manager, aliyunClient register.Interface, swPool *vswitch.SwitchPool) *ReconcilePod {
	r := &ReconcilePod{
		client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
		record: mgr.GetEventRecorderFor("Pod"),
		aliyun: aliyunClient,
		swPool: swPool,
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

	pod := &corev1.Pod{}
	err := m.client.Get(ctx, request.NamespacedName, pod)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return m.podDelete(ctx, request.NamespacedName)
		}
		return reconcile.Result{}, err
	}
	if utils.IsJobPod(pod) {
		if utils.PodSandboxExited(pod) {
			return m.podDelete(ctx, request.NamespacedName)
		}
	}
	// for pod is deleting we will wait it terminated
	if !pod.DeletionTimestamp.IsZero() {
		return reconcile.Result{RequeueAfter: 2 * time.Second}, nil
	}

	return m.podCreate(ctx, pod)
}

// NeedLeaderElection need election
func (m *ReconcilePod) NeedLeaderElection() bool {
	return true
}

// podCreate is the func when the pod is to be create or created
func (m *ReconcilePod) podCreate(ctx context.Context, pod *corev1.Pod) (reconcile.Result, error) {
	l := log.FromContext(ctx)

	var err error
	// 1. check podENI is existed
	prePodENI := &v1beta1.PodENI{}
	err = m.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, prePodENI)
	if err == nil {
		// for podENI is deleting , wait it down
		if !prePodENI.DeletionTimestamp.IsZero() {
			return reconcile.Result{Requeue: true}, nil
		}
		switch prePodENI.Status.Phase {
		case v1beta1.ENIPhaseUnbind:
			return m.reConfig(ctx, pod, prePodENI)
		case v1beta1.ENIPhaseBind:
			return reconcile.Result{}, nil
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// 2. cr is not found , so we will create new
	l.Info("creating eni")

	podENI := &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Finalizers: []string{
				types.FinalizerPodENI,
			},
			Annotations: map[string]string{
				types.PodUID: string(pod.UID),
			},
		},
	}

	nodeInfo, allocType, allocs, err := m.parse(ctx, pod)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error parse config, %w", err)
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

	podENI.Spec.Zone = nodeInfo.Zone

	// 2.2 create eni
	err = m.createENI(ctx, &allocs, allocType, pod, podENI)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error batch create eni,%w", err)
	}

	// 2.3 create cr
	err = m.client.Create(ctx, podENI)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error create cr, %s", err)
	}

	return reconcile.Result{}, nil
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
	if prePodENI.Status.Phase == v1beta1.ENIPhaseDeleting {
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
		_, err = common.UpdatePodENIStatus(ctx, m.client, prePodENICopy)
		return reconcile.Result{}, err
	}

	// for non fixed ip, update status to v1beta1.ENIPhaseDeleting
	update := prePodENI.DeepCopy()
	update.Status.Phase = v1beta1.ENIPhaseDeleting
	_, err = common.UpdatePodENIStatus(ctx, m.client, update)

	return reconcile.Result{}, err
}

func (m *ReconcilePod) deleteAllENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	for _, alloc := range podENI.Spec.Allocations {
		if alloc.ENI.ID == "" {
			continue
		}
		ctx := common.WithCtx(ctx, &alloc)
		realClient, _, err := common.Became(ctx, m.aliyun)
		if err != nil {
			return err
		}
		err = realClient.DeleteNetworkInterface(ctx, alloc.ENI.ID)
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

func (m *ReconcilePod) parse(ctx context.Context, pod *corev1.Pod) (*common.NodeInfo, *v1beta1.AllocationType, []*v1beta1.Allocation, error) {
	node, err := m.getNode(ctx, pod.Spec.NodeName)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error get node %s, %w", node.Name, err)
	}
	nodeInfo, err := common.NewNodeInfo(node)
	if err != nil {
		return nil, nil, nil, err
	}

	// 2.1 fill config
	var allocType *v1beta1.AllocationType

	anno, err := controlplane.ParsePodNetworksFromAnnotation(pod)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error parse pod annotation, %w", err)
	}

	allocs, err := m.ParsePodNetworksFromAnnotation(ctx, nodeInfo.Zone, anno)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error parse pod annotation, %w", err)
	}

	if len(allocs) == 0 {
		// use config from podNetworking

		podNetwokingName := pod.Annotations[types.PodNetworking]
		if podNetwokingName == "" {
			return nil, nil, nil, fmt.Errorf("podNetworking is empty")
		}
		var podNetworking v1beta1.PodNetworking

		err = m.client.Get(ctx, k8stypes.NamespacedName{
			Name: podNetwokingName,
		}, &podNetworking)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("error get podNetworking %s, %w", podNetwokingName, err)
		}
		var vsw *vswitch.Switch
		vsw, err = m.swPool.GetOne(ctx, m.aliyun, nodeInfo.Zone, podNetworking.Spec.VSwitchOptions, false)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("can not found available vSwitch for zone %s, %w", nodeInfo.Zone, err)
		}

		allocs = append(allocs, &v1beta1.Allocation{
			ENI: v1beta1.ENI{
				SecurityGroupIDs: podNetworking.Spec.SecurityGroupIDs,
				VSwitchID:        vsw.ID,
			},
			IPv4CIDR: vsw.IPv4CIDR,
			IPv6CIDR: vsw.IPv6CIDR,
		})

		allocType, err = controlplane.ParseAllocationType(&podNetworking.Spec.AllocationType)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("error parse ReleaseAfter, %w", err)
		}
	} else {
		// try get v1beta1.PodAllocType from annotation
		allocType, err = controlplane.ParsePodIPTypeFromAnnotation(pod)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if allocType == nil {
		return nil, nil, nil, fmt.Errorf("allocType is nil")
	}

	return nodeInfo, allocType, allocs, nil
}

// reConfig this phase will re-config the eni if possible
// 1. update pod uid
// 2. re-generate the target spec
func (m *ReconcilePod) reConfig(ctx context.Context, pod *corev1.Pod, prePodENI *v1beta1.PodENI) (reconcile.Result, error) {
	l := log.FromContext(ctx).WithName("re-config")

	update := prePodENI.DeepCopy()

	if prePodENI.Annotations[types.PodUID] == string(pod.UID) {
		update.Status.Phase = v1beta1.ENIPhaseBinding
		_, err := common.UpdatePodENIStatus(ctx, m.client, update)
		return reconcile.Result{}, err
	}

	if update.Annotations == nil {
		update.Annotations = make(map[string]string)
	}
	update.Annotations[types.PodUID] = string(pod.UID)

	if prePodENI.Annotations[types.PodNetworking] != "" {
		l.V(5).Info("using podNetworking will not re-config", types.PodNetworking, prePodENI.Annotations[types.PodNetworking])
		_, err := common.UpdatePodENI(ctx, m.client, update)
		return reconcile.Result{}, err
	}

	// TODO check and update podENI spec
	anno, err := controlplane.ParsePodNetworksFromAnnotation(pod)
	if err != nil {
		return reconcile.Result{}, err
	}

	targets := make(map[string]int, len(anno.PodNetworks))
	for i, n := range anno.PodNetworks {
		name := n.Interface
		if name == "" {
			name = "eth0"
		}
		targets[name] = i
	}

	// del unexpected config
	for i := range update.Spec.Allocations {
		alloc := update.Spec.Allocations[i]
		name := alloc.Interface
		if name == "" {
			name = "eth0"
		}

		if _, ok := targets[name]; ok {
			delete(targets, name)
			continue
		}
		delete(targets, name)
		l.Info("changed remove eni", "if", name, "eni", alloc.ENI.ID)

		if alloc.ENI.ID != "" {
			ctx := common.WithCtx(context.Background(), &alloc)
			realClient, _, err := common.Became(ctx, m.aliyun)
			if err != nil {
				m.record.Eventf(prePodENI, corev1.EventTypeWarning, types.EventDeleteENIFailed, err.Error())
				return reconcile.Result{}, err
			}
			err = realClient.DeleteNetworkInterface(context.Background(), alloc.ENI.ID)
			if err != nil {
				m.record.Eventf(prePodENI, corev1.EventTypeWarning, types.EventDeleteENIFailed, err.Error())
				return reconcile.Result{}, err
			}
		}
		update.Spec.Allocations = append(update.Spec.Allocations[:i], update.Spec.Allocations[i+1:]...)
	}

	allocType, err := controlplane.ParsePodIPTypeFromAnnotation(pod)
	if err != nil {
		return reconcile.Result{}, err
	}
	// add new config
	newAnno := &controlplane.PodNetworksAnnotation{}
	for _, i := range targets {
		newAnno.PodNetworks = append(newAnno.PodNetworks, anno.PodNetworks[i])
	}

	node, err := m.getNode(ctx, pod.Spec.NodeName)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error get node %s, %w", node.Name, err)
	}
	nodeInfo, err := common.NewNodeInfo(node)
	if err != nil {
		return reconcile.Result{}, err
	}

	allocs, err := m.ParsePodNetworksFromAnnotation(ctx, nodeInfo.Zone, newAnno)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = m.createENI(ctx, &allocs, allocType, pod, update)
	if err != nil {
		return reconcile.Result{}, err
	}
	_, err = common.UpdatePodENI(ctx, m.client, update)
	return reconcile.Result{Requeue: true}, err
}

func (m *ReconcilePod) createENI(ctx context.Context, allocs *[]*v1beta1.Allocation, allocType *v1beta1.AllocationType, pod *corev1.Pod, podENI *v1beta1.PodENI) error {
	if allocs == nil || len(*allocs) == 0 {
		return nil
	}
	l := log.FromContext(ctx)

	var err error
	defer func() {
		if err != nil {
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
			realClient, _, err := common.Became(ctx, m.aliyun)
			if err != nil {
				return fmt.Errorf("get client failed, %w", err)
			}

			eni, err := realClient.CreateNetworkInterface(ctx, aliyunClient.ENITypeSecondary, alloc.ENI.VSwitchID, alloc.ENI.SecurityGroupIDs, 1, ipv6Count, map[string]string{
				types.TagKeyClusterID:               clusterID,
				types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
				types.TagKubernetesPodName:          utils.TrimStr(pod.Name, 120),
				types.TagKubernetesPodNamespace:     utils.TrimStr(pod.Namespace, 120),
			})
			if err != nil {
				return fmt.Errorf("create eni with openAPI err, %w", err)
			}

			v6 := ""
			if len(eni.Ipv6Sets.Ipv6Set) > 0 {
				v6 = eni.Ipv6Sets.Ipv6Set[0].Ipv6Address
			}
			alloc.ENI = v1beta1.ENI{
				ID:               eni.NetworkInterfaceId,
				MAC:              eni.MacAddress,
				Zone:             eni.ZoneId,
				VSwitchID:        eni.VSwitchId,
				SecurityGroupIDs: eni.SecurityGroupIds.SecurityGroupId,
			}
			alloc.IPv4 = eni.PrivateIpAddress
			alloc.IPv6 = v6
			alloc.AllocationType = *allocType

			ch <- alloc
			return m.PostENICreate(ctx, realClient, alloc)
		})
	}
	err = g.Wait()
	close(ch)
	<-done
	return err
}
