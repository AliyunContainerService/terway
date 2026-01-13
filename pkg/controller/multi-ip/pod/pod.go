package pod

import (
	"context"

	"github.com/AliyunContainerService/terway/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/multi-ip/node"
)

const ControllerName = "multi-ip-pod"

func init() {
	register.Add(ControllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		ctrlCtx.RegisterResource = append(ctrlCtx.RegisterResource, &corev1.Pod{})
		return ctrl.NewControllerManagedBy(mgr).
			Named(ControllerName).
			WithOptions(controller.Options{
				MaxConcurrentReconciles: ctrlCtx.Config.MultiIPPodMaxConcurrent,
			}).
			For(&corev1.Pod{}, builder.WithPredicates(&predicateForPodEvent{})).
			Complete(&ReconcilePod{
				client: mgr.GetClient(),
				scheme: mgr.GetScheme(),
				record: mgr.GetEventRecorderFor(utils.EventName(ControllerName)),
			})
	}, false)
}

// ReconcilePod implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePod{}

// ReconcilePod reconciles a AutoRepair object
type ReconcilePod struct {
	client client.Client
	scheme *runtime.Scheme

	record record.EventRecorder
}

func (r *ReconcilePod) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	pod := &corev1.Pod{}
	err := r.client.Get(ctx, client.ObjectKey{
		Namespace: request.Namespace,
		Name:      request.Name,
	}, pod)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if !needProcess(pod) {
		return reconcile.Result{}, nil
	}

	node.Notify(ctx, pod.Spec.NodeName)

	return reconcile.Result{}, nil
}
