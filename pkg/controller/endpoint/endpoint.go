package endpoint

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/utils"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var log = ctrl.Log.WithName("endpoint")

const controllerName = "endpoint"

func init() {
	register.Add(controllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		ipStr := os.Getenv("MY_POD_IP")
		if ipStr == "" {
			return fmt.Errorf("podIP is not found")
		}
		// if enable Service name should equal cfg.ControllerName
		ep := New(ctrlCtx.Config.ControllerName, ctrlCtx.Config.ControllerNamespace, ipStr, int32(ctrlCtx.Config.WebhookPort))
		return mgr.Add(ep)
	}, false)
}

// ReconcilePodNetworking implements reconcile.Reconciler
var _ manager.Runnable = &Endpoint{}

// Endpoint reconciles a AutoRepair object
type Endpoint struct {
	PodIP     string
	Name      string
	Namespace string
	Port      int32
}

func New(name, namespace, podIP string, port int32) *Endpoint {
	return &Endpoint{
		PodIP:     podIP,
		Name:      name,
		Namespace: namespace,
		Port:      port,
	}
}

func (m *Endpoint) Start(ctx context.Context) error {
	wait.Until(func() {
		err := m.RegisterEndpoints()
		if err != nil {
			log.Error(err, "error sync endpoint")
		}
	}, time.Minute, ctx.Done())
	return fmt.Errorf("endpoint sync exited")
}

// NeedLeaderElection need election
func (m *Endpoint) NeedLeaderElection() bool {
	return true
}

// RegisterEndpoints to endpoint
func (m *Endpoint) RegisterEndpoints() error {
	newEPSubnet := []v1.EndpointSubset{
		{
			Addresses: []v1.EndpointAddress{
				{
					IP: m.PodIP,
				},
			},
			Ports: []v1.EndpointPort{
				{
					Name:     "https",
					Port:     m.Port,
					Protocol: "TCP",
				},
			},
		}}
	ctx := context.Background()
	oldEP, err := utils.K8sClient.CoreV1().Endpoints(m.Namespace).Get(ctx, m.Name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		_, err = utils.K8sClient.CoreV1().Endpoints(m.Namespace).Create(ctx, &v1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: m.Namespace,
				Name:      m.Name,
			},
			Subsets: newEPSubnet,
		}, metav1.CreateOptions{})
		log.Info("register endpoint", "ip", m.PodIP)
		return err
	}

	if reflect.DeepEqual(&oldEP.Subsets, &newEPSubnet) {
		return nil
	}
	copyEP := oldEP.DeepCopy()
	copyEP.Subsets = newEPSubnet
	_, err = utils.K8sClient.CoreV1().Endpoints(m.Namespace).Update(ctx, copyEP, metav1.UpdateOptions{})
	log.Info("register endpoint", "ip", m.PodIP)
	return err
}
