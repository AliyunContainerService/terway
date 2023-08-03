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

package podnetworking

import (
	"context"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
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

const controllerName = "pod-networking"

func init() {
	register.Add(controllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		c, err := controller.New(controllerName, mgr, controller.Options{
			Reconciler:              NewReconcilePodNetworking(mgr, ctrlCtx.AliyunClient, ctrlCtx.VSwitchPool),
			MaxConcurrentReconciles: 1,
		})
		if err != nil {
			return err
		}

		return c.Watch(
			source.Kind(mgr.GetCache(), &v1beta1.PodNetworking{}),
			&handler.EnqueueRequestForObject{},
			&predicate.ResourceVersionChangedPredicate{},
			&predicateForPodnetwokringEvent{},
		)
	}, true)
}

// ReconcilePodNetworking implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePodNetworking{}

// ReconcilePodNetworking reconciles a AutoRepair object
type ReconcilePodNetworking struct {
	client       client.Client
	scheme       *runtime.Scheme
	aliyunClient aliyunClient.VSwitch
	swPool       *vswitch.SwitchPool

	//record event recorder
	record record.EventRecorder
}

// NewReconcilePodNetworking watch pod lifecycle events and sync to podENI resource
func NewReconcilePodNetworking(mgr manager.Manager, aliyunClient aliyunClient.VSwitch, swPool *vswitch.SwitchPool) *ReconcilePodNetworking {
	r := &ReconcilePodNetworking{
		client:       mgr.GetClient(),
		scheme:       mgr.GetScheme(),
		record:       mgr.GetEventRecorderFor("PodNetworking"),
		aliyunClient: aliyunClient,
		swPool:       swPool,
	}
	return r
}

// Reconcile podNetworking when user create or vSwitch fields changed
func (m *ReconcilePodNetworking) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.Info("Reconcile")

	old := &v1beta1.PodNetworking{}
	err := m.client.Get(ctx, request.NamespacedName, old)
	if err != nil {
		if errors.IsNotFound(err) {
			l.Info("not found")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	update := old.DeepCopy()
	update.Status.UpdateAt = metav1.Now()

	var statusVSW []v1beta1.VSwitch
	err = func() error {
		for _, id := range old.Spec.VSwitchOptions {
			sw, innerErr := m.swPool.GetByID(ctx, m.aliyunClient, id)
			if innerErr != nil {
				return innerErr
			}
			statusVSW = append(statusVSW, v1beta1.VSwitch{
				ID:   sw.ID,
				Zone: sw.Zone,
			})
		}
		return nil
	}()
	if err == nil {
		update.Status.VSwitches = statusVSW
		update.Status.Status = v1beta1.NetworkingStatusReady
		update.Status.Message = ""
		m.record.Eventf(update, corev1.EventTypeNormal, types.EventSyncPodNetworkingSucceed, "Synced")
	} else {
		update.Status.Status = v1beta1.NetworkingStatusFail
		update.Status.Message = err.Error()
		m.record.Eventf(update, corev1.EventTypeWarning, types.EventSyncPodNetworkingFailed, "Sync failed %s", err.Error())
	}

	err2 := m.updateStatus(ctx, update, old)
	if err != nil {
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return reconcile.Result{}, err2
}

// NeedLeaderElection need election
func (m *ReconcilePodNetworking) NeedLeaderElection() bool {
	return true
}

func (m *ReconcilePodNetworking) updateStatus(ctx context.Context, update, old *v1beta1.PodNetworking) error {
	err := wait.ExponentialBackoff(utils.DefaultPatchBackoff, func() (done bool, err error) {
		innerErr := m.client.Status().Patch(ctx, update, client.MergeFrom(old))
		if innerErr != nil {
			if errors.IsNotFound(innerErr) {
				l := log.FromContext(ctx)
				l.Info("podNetworking is not found")
				return true, nil
			}
			return false, err
		}
		return true, nil
	})
	return err
}
