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

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
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
	register.Add(controllerName, func(mgr manager.Manager, aliyunClient *aliyun.OpenAPI, swPool *vswitch.SwitchPool) error {
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
		record: mgr.GetEventRecorderFor("Pod"),
		aliyun: aliyunClient,
		swPool: swPool,
	}
	return r
}

// Reconcile all pod events
// Pod create -> create PodENI
// Pod delete -> delete PodENI
// Fixed IP Pod delete -> mark PodENI status v1beta1.ENIPhaseDeleting
// before delete event is trigger will check pod phase make sure sandbox is terminated
func (m *ReconcilePod) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.V(5).Info("Reconcile")

	pod := &corev1.Pod{}
	err := m.client.Get(context.Background(), request.NamespacedName, pod)
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
	// for pod we will wait it terminated
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
			// TODO check and update podENI spec
			// make sure all eni is exist
			prePodENICopy := prePodENI.DeepCopy()
			prePodENICopy.Status.Phase = v1beta1.ENIPhaseBinding
			_, err = common.SetPodENIStatus(ctx, m.client, prePodENICopy, prePodENI)
			return reconcile.Result{}, err
		case v1beta1.ENIPhaseBind:
			return reconcile.Result{}, nil
		}
		return reconcile.Result{Requeue: true}, nil
	}

	node, err := m.getNode(ctx, pod.Spec.NodeName)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error get node %s, %w", node.Name, err)
	}
	nodeInfo, err := common.NewNodeInfo(node)
	if err != nil {
		return reconcile.Result{}, err
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
		},
		Spec: v1beta1.PodENISpec{
			Zone: nodeInfo.Zone,
		},
	}

	defer func() {
		if err != nil {
			l.WithValues("eni", "").Error(err, "create fail")
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventCreateENIFailed, err.Error())
		} else {
			var ids []string
			for _, alloc := range podENI.Spec.Allocations {
				ids = append(ids, alloc.ENI.ID)
				l.WithValues("eni", alloc.ENI.ID).Info("create")
			}
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventCreateENISucceed, "create enis %s", strings.Join(ids, ","))
		}
	}()

	// 2.1 fill config
	allocType := &v1beta1.AllocationType{Type: v1beta1.IPAllocTypeElastic}

	anno, err := controlplane.ParsePodNetworksFromAnnotation(pod)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error parse pod annotation, %w", err)
	}

	allocs, err := m.ParsePodNetworksFromAnnotation(ctx, nodeInfo.Zone, anno)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error parse pod annotation, %w", err)
	}

	if len(allocs) == 0 {
		// use config from podNetworking

		podNetwokingName := pod.Annotations[types.PodNetworking]
		if podNetwokingName == "" {
			return reconcile.Result{}, fmt.Errorf("podNetworking is empty")
		}
		var podNetworking v1beta1.PodNetworking

		err = m.client.Get(ctx, k8stypes.NamespacedName{
			Name: podNetwokingName,
		}, &podNetworking)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error get podNetworking %s, %w", podNetwokingName, err)
		}
		vsw, err := m.swPool.GetOne(ctx, m.aliyun, nodeInfo.Zone, podNetworking.Spec.VSwitchIDs, false)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("can not found available vSwitch for zone %s, %w", nodeInfo.Zone, err)
		}

		allocs = append(allocs, &v1beta1.Allocation{
			ENI: v1beta1.ENI{
				SecurityGroupIDs: podNetworking.Spec.SecurityGroupIDs,
				VSwitchID:        vsw.ID,
			},
			IPv4CIDR: vsw.IPv4CIDR,
			IPv6CIDR: vsw.IPv6CIDR,
		})

		if podNetworking.Spec.AllocationType.Type == v1beta1.IPAllocTypeFixed {
			allocType.Type = v1beta1.IPAllocTypeFixed
		}

		allocType.ReleaseStrategy = v1beta1.ReleaseStrategyTTL
		if podNetworking.Spec.AllocationType.ReleaseStrategy != "" {
			allocType.ReleaseStrategy = podNetworking.Spec.AllocationType.ReleaseStrategy
		}

		allocType.ReleaseAfter = "10m"
		if podNetworking.Spec.AllocationType.ReleaseAfter != "" {
			_, err := time.ParseDuration(podNetworking.Spec.AllocationType.ReleaseAfter)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("error parse ReleaseAfter, %w", err)
			}
			allocType.ReleaseAfter = podNetworking.Spec.AllocationType.ReleaseAfter
		}
	} else {
		// try get v1beta1.PodAllocType from annotation
		allocType, err = controlplane.ParsePodIPTypeFromAnnotation(pod)
		if err != nil {
			return reconcile.Result{}, err
		}
	}
	if allocType == nil {
		return reconcile.Result{}, fmt.Errorf("IPType is nil")
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

	// 2.2 create eni
	clusterID := controlplane.GetConfig().ClusterID

	ipv6Count := 0
	switch controlplane.GetConfig().IPStack {
	case "ipv6", "dual":
		ipv6Count = 1
	}
	for _, alloc := range allocs {
		var eni *ecs.CreateNetworkInterfaceResponse

		ctx := common.WithCtx(ctx, alloc)
		realClient, _, err := common.Became(ctx, m.aliyun)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("get client failed, %w", err)
		}

		eni, err = realClient.CreateNetworkInterface(ctx, aliyun.ENITypeSecondary, alloc.ENI.VSwitchID, alloc.ENI.SecurityGroupIDs, 1, ipv6Count, map[string]string{
			types.TagKeyClusterID:               clusterID,
			types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
			types.TagKubernetesPodName:          utils.TrimStr(pod.Name, 120),
			types.TagKubernetesPodNamespace:     utils.TrimStr(pod.Namespace, 120),
		})
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("create eni with openAPI err, %w", err)
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

		podENI.Spec.Allocations = append(podENI.Spec.Allocations, *alloc)
		err = m.PostENICreate(ctx, realClient, alloc)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	// 2.5 create cr
	err = m.client.Create(ctx, podENI)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error create cr, %s", err)
	}

	return reconcile.Result{}, nil
}

// podDelete is proceed after pod is deleted
// for none fixed ip pod, will delete podENI resource and let podENI controller do remain gc
// for fixed ip pod , update v1beta1.PodENI status to v1beta1.ENIPhaseDeleting
func (m *ReconcilePod) podDelete(ctx context.Context, namespacedName client.ObjectKey) (reconcile.Result, error) {
	l := log.FromContext(ctx)

	prePodENI := &v1beta1.PodENI{}
	err := m.client.Get(ctx, namespacedName, prePodENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
	}

	haveFixedIP := false
	for _, alloc := range prePodENI.Spec.Allocations {
		if alloc.AllocationType.Type == v1beta1.IPAllocTypeFixed {
			haveFixedIP = true
		}
	}
	if haveFixedIP {
		// for fixed ip , update podENI status to deleting
		if prePodENI.Status.Phase == v1beta1.ENIPhaseDeleting {
			return reconcile.Result{}, nil
		}
		prePodENICopy := prePodENI.DeepCopy()
		prePodENICopy.Status.Phase = v1beta1.ENIPhaseDeleting
		_, err = common.SetPodENIStatus(ctx, m.client, prePodENICopy, prePodENI)
		return reconcile.Result{}, err
	}

	// for non fixed ip, delete cr resource
	err = m.client.Delete(ctx, prePodENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			l.Info("cr resource not found")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil

}

func (m *ReconcilePod) deleteAllENI(ctx context.Context, podENI *v1beta1.PodENI) error {
	for _, alloc := range podENI.Spec.Allocations {
		if alloc.ENI.ID == "" {
			continue
		}
		ctx := common.WithCtx(context.Background(), &alloc)
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
