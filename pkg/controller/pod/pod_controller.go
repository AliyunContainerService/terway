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
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"

	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
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
// Fixed IP Pod delete -> mark PodENI status v1beta1.ENIStatusDeleting
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
func (m *ReconcilePod) podCreate(ctx context.Context, pod *corev1.Pod) (r reconcile.Result, err error) {
	l := log.FromContext(ctx)

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
		switch prePodENI.Status.Status {
		case v1beta1.ENIStatusUnbind:
			prePodENICopy := prePodENI.DeepCopy()
			prePodENICopy.Status.Status = v1beta1.ENIStatusBinding
			_, err = common.SetPodENIStatus(ctx, m.client, prePodENICopy, prePodENI)
			return reconcile.Result{}, err
		case v1beta1.ENIStatusBind:
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
		},
	}

	defer func() {
		if err != nil {
			l.WithValues("eni", "").Error(err, "create fail")
			m.record.Eventf(podENI, corev1.EventTypeWarning, types.EventCreateENIFailed, err.Error())
		} else {
			l.WithValues("eni", podENI.Spec.Allocation.ENI.ID).Info("create")
			m.record.Eventf(podENI, corev1.EventTypeNormal, types.EventCreateENISucceed, "create eni %s", podENI.Spec.Allocation.ENI.ID)
		}
	}()

	// 2.1 fill config
	podConf := &common.PodConf{}
	node, err := m.getNode(ctx, pod.Spec.NodeName)
	if err != nil {
		e := fmt.Errorf("error get node %s, %w", node.Name, err)
		return reconcile.Result{}, e
	}
	err = podConf.SetNodeConf(node)
	if err != nil {
		e := fmt.Errorf("error parse config from node %s, %w", node.Name, err)
		return reconcile.Result{}, e
	}

	podNetwokingName := pod.Annotations[types.PodNetworking]

	podNetworking := &v1beta1.PodNetworking{}
	err = m.client.Get(ctx, k8stypes.NamespacedName{
		Name: podNetwokingName,
	}, podNetworking)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error get podNetworking %s, %w", podNetwokingName, err)
	}

	err = podConf.SetPodNetworkingConf(podNetworking)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error parse podNetworking %s, %w", podNetwokingName, err)
	}

	vsw, err := m.swPool.GetOne(podConf.Zone, sets.NewString(podNetworking.Spec.VSwitchIDs...))
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("can not found available vSwitch for zone %s, %w", podConf.Zone, err)
	}
	podConf.VSwitchID = vsw

	// 2.2 create eni
	eni, err := m.aliyun.CreateNetworkInterface(context.Background(), aliyun.ENITypeSecondary, podConf.VSwitchID, podConf.SecurityGroups, 1, 0, map[string]string{
		types.TagKeyClusterID:               viper.GetString("cluster-id"),
		types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
		types.TagKubernetesPodName:          utils.TrimStr(pod.Name, 120),
		types.TagKubernetesPodNamespace:     utils.TrimStr(pod.Namespace, 120),
	})
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("create eni with openAPI err, %w", err)
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
			innerErr := m.deleteMemberENI(podENI)
			if innerErr != nil {
				l.Error(innerErr, "error delete eni %w")
			}
		}
	}()

	// 2.5 create cr
	err = m.client.Create(ctx, podENI)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error create cr, %s", err)
	}

	return reconcile.Result{}, nil
}

func (m *ReconcilePod) podDelete(ctx context.Context, namespacedName client.ObjectKey) (reconcile.Result, error) {
	l := log.FromContext(ctx)

	prePodENI := &v1beta1.PodENI{}
	err := m.client.Get(ctx, namespacedName, prePodENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
	}

	switch prePodENI.Spec.Allocation.IPType.Type {
	case v1beta1.IPAllocTypeFixed:
		// for fixed ip , update podENI status to deleting
		if prePodENI.Status.Status == v1beta1.ENIStatusDeleting {
			return reconcile.Result{}, nil
		}

		prePodENICopy := prePodENI.DeepCopy()
		prePodENICopy.Status.Status = v1beta1.ENIStatusDeleting
		_, err := common.SetPodENIStatus(ctx, m.client, prePodENICopy, prePodENI)
		return reconcile.Result{}, err
	default:
		// for non fixed ip, delete cr resource
		err = m.client.Delete(context.Background(), prePodENI)
		if err != nil {
			if k8sErr.IsNotFound(err) {
				l.Info("cr resource not found")
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
}

func (m *ReconcilePod) deleteMemberENI(podENI *v1beta1.PodENI) error {
	if podENI.Spec.Allocation.ENI.ID == "" {
		return nil
	}
	return m.aliyun.DeleteNetworkInterface(context.Background(), podENI.Spec.Allocation.ENI.ID)
}

func (m *ReconcilePod) getNode(ctx context.Context, name string) (*corev1.Node, error) {
	node := &corev1.Node{}
	err := m.client.Get(ctx, k8stypes.NamespacedName{
		Name: name,
	}, node)
	return node, err
}
