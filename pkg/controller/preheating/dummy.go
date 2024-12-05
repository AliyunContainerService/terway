package preheating

import (
	"context"
	"reflect"

	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const ControllerName = "no-op"

// ReconcilePod implements reconcile.Reconciler
var _ reconcile.Reconciler = &DummyReconcile{}

// DummyReconcile reconciles a AutoRepair object
type DummyReconcile struct {
	RegisterResource []client.Object
}

func (r *DummyReconcile) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (r *DummyReconcile) SetupWithManager(mgr manager.Manager) error {
	NeedLeaderElection := false

	builder := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{
			NeedLeaderElection: &NeedLeaderElection,
		}).
		Named(ControllerName)
	used := map[string]struct{}{}

	for _, v := range r.RegisterResource {
		t := reflect.TypeOf(v)
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		_, ok := used[t.Name()]
		if ok {
			continue
		}
		used[t.Name()] = struct{}{}
		builder = builder.Watches(v, &NoOp[client.Object, reconcile.Request]{})
	}
	return builder.Complete(&DummyReconcile{})
}

//var _ handler.TypedEventHandler[] = &NoOp{}

type NoOp[object any, request comparable] struct{}

func (n *NoOp[object, request]) Create(context.Context, event.TypedCreateEvent[object], workqueue.TypedRateLimitingInterface[request]) {
}

func (n *NoOp[object, request]) Update(context.Context, event.TypedUpdateEvent[object], workqueue.TypedRateLimitingInterface[request]) {
}

func (n *NoOp[object, request]) Delete(context.Context, event.TypedDeleteEvent[object], workqueue.TypedRateLimitingInterface[request]) {
}

func (n *NoOp[object, request]) Generic(context.Context, event.TypedGenericEvent[object], workqueue.TypedRateLimitingInterface[request]) {
}
