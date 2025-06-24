package eni

import (
	"context"
	"fmt"
	"strings"

	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

// ReconcileNetworkInterface reconciles a AutoRepair object
type ReconcileNetworkInterface struct {
	client client.Client
	scheme *runtime.Scheme
	aliyun aliyunClient.OpenAPI

	//record event recorder
	record record.EventRecorder

	resourceBackoff *BackoffManager
}

const controllerName = "eni"

func init() {
	register.Add(controllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		ctrlCtx.RegisterResource = append(ctrlCtx.RegisterResource, &v1beta1.NetworkInterface{})

		err := builder.ControllerManagedBy(mgr).
			Named(controllerName).
			WithOptions(controller.Options{
				MaxConcurrentReconciles: controlplane.GetConfig().ENIMaxConcurrent,
				LogConstructor: func(request *reconcile.Request) logr.Logger {
					log := mgr.GetLogger()
					if request != nil {
						log = log.WithValues("name", request.Name)
					}
					return log
				},
			}).
			// may be use watch event
			Watches(&v1beta1.NetworkInterface{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(&predicate.ResourceVersionChangedPredicate{})).
			Complete(&ReconcileNetworkInterface{
				client:          mgr.GetClient(),
				scheme:          mgr.GetScheme(),
				aliyun:          ctrlCtx.AliyunClient, // use direct client
				record:          mgr.GetEventRecorderFor("TerwayENetworkInterfaceontroller"),
				resourceBackoff: NewBackoffManager(),
			})

		return err

	}, true)
}

func (r *ReconcileNetworkInterface) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	eni := &v1beta1.NetworkInterface{}
	err := r.client.Get(ctx, request.NamespacedName, eni)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			r.resourceBackoff.Del(request.String())
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	l := logr.FromContextOrDiscard(ctx)
	l.Info("reconcile networkInterface", "status", eni.Status.Phase)
	// nb(l1b0k): v1beta1.ENIPhaseInitial means do nothing

	if eni.Status.Phase == v1beta1.ENIPhaseDeleting {
		if eni.DeletionTimestamp.IsZero() {
			err = r.client.Delete(ctx, eni)
			if err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
	}

	// Phase may change from Deleting -> Unbind, this is expected, as DeletionTimestamp is always set first
	if eni.Status.Phase == v1beta1.ENIPhaseDetaching ||
		eni.Status.Phase == v1beta1.ENIPhaseDeleting ||
		!eni.DeletionTimestamp.IsZero() {
		result, err := r.detach(ctx, eni)
		if err != nil {
			return reconcile.Result{}, err
		}
		if !result.IsZero() {
			return result, nil
		}
	}

	if !eni.DeletionTimestamp.IsZero() {
		err = r.delete(ctx, eni)
		if err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	if eni.Status.Phase == v1beta1.ENIPhaseBinding {
		result, err := r.attach(ctx, eni)
		if err != nil {
			return reconcile.Result{}, err
		}
		return result, nil
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileNetworkInterface) attach(ctx context.Context, networkInterface *v1beta1.NetworkInterface) (reconcile.Result, error) {
	var err error

	if networkInterface.Status.InstanceID != "" {
		var resp []*aliyunClient.NetworkInterface
		if strings.HasPrefix(networkInterface.Name, "leni-") || strings.HasPrefix(networkInterface.Name, "hdeni-") {
			// nb(l1b0k): for now len and hdeni share nearly same status
			if strings.HasPrefix(networkInterface.Name, "leni-") {
				ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIEFLO)
			} else {
				ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIEFLOHDENI)
			}

			resp, err = r.aliyun.DescribeNetworkInterfaceV2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{networkInterface.Name},
				RawStatus:           ptr.To(true),
			})
			if err != nil {
				return reconcile.Result{}, err
			}
			if len(resp) == 0 {
				return reconcile.Result{}, fmt.Errorf("network interface %s not found", networkInterface.Name)
			}

			switch resp[0].Status {
			case aliyunClient.LENIStatusAvailable:
			case aliyunClient.LENIStatusUnattached, aliyunClient.LENIStatusAttachFailed:
				//	"Code": "1017",  Attaching Available 不允许操作
				err = r.aliyun.AttachNetworkInterfaceV2(ctx, &aliyunClient.AttachNetworkInterfaceOptions{
					NetworkInterfaceID:     toPtr(networkInterface.Name),
					InstanceID:             toPtr(networkInterface.Status.InstanceID),
					TrunkNetworkInstanceID: toPtr(networkInterface.Status.TrunkENIID),
					NetworkCardIndex:       networkInterface.Status.NetworkCardIndex,
				})
				if err != nil {
					return reconcile.Result{}, err
				}
				fallthrough
			case aliyunClient.LENIStatusExecuting, aliyunClient.LENIStatusAttaching, aliyunClient.LENIStatusDetaching:
				du, err := r.resourceBackoff.Get(networkInterface.Name, backoff.Backoff(backoff.WaitLENIStatus))
				if err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{RequeueAfter: du}, nil
			case aliyunClient.LENIStatusCreateFailed:
				// release this eni, this status should be on first create
				r.record.Eventf(networkInterface, corev1.EventTypeWarning, types.EventCreateENIFailed, "backend create failed, will delete")
				return reconcile.Result{}, r.rollBackPodENI(ctx, networkInterface)
			case aliyunClient.LENIStatusDetachFailed, aliyunClient.LENIStatusDeleteFailed, aliyunClient.LENIStatusDeleting:
				return reconcile.Result{}, fmt.Errorf("unsupported status on attach %s", resp[0].Status)
			default:
				return reconcile.Result{}, fmt.Errorf("unknown status %s", resp[0].Status)
			}

		} else {
			err = r.aliyun.AttachNetworkInterfaceV2(ctx, &aliyunClient.AttachNetworkInterfaceOptions{
				NetworkInterfaceID:     toPtr(networkInterface.Name),
				InstanceID:             toPtr(networkInterface.Status.InstanceID),
				TrunkNetworkInstanceID: toPtr(networkInterface.Status.TrunkENIID),
				NetworkCardIndex:       networkInterface.Status.NetworkCardIndex,
			})
			if err != nil {
				return reconcile.Result{}, err
			}

			resp, err = r.aliyun.DescribeNetworkInterfaceV2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{networkInterface.Name},
				RawStatus:           ptr.To(true),
			})
			if err != nil {
				return reconcile.Result{}, err
			}

			if len(resp) != 1 || resp[0].Status != aliyunClient.ENIStatusInUse {
				// wait next time
				du, err := r.resourceBackoff.Get(networkInterface.Name, backoff.Backoff(backoff.WaitENIStatus))
				if err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{RequeueAfter: du}, nil
			}
		}

		r.resourceBackoff.Del(networkInterface.Name)

		remote := resp[0]

		oldSpec := networkInterface.Spec.DeepCopy()

		networkInterface.Spec.ENI = v1beta1.ENI{
			ID:               remote.NetworkInterfaceID,
			VPCID:            remote.VPCID,
			MAC:              remote.MacAddress,
			Zone:             remote.ZoneID,
			VSwitchID:        remote.VSwitchID,
			ResourceGroupID:  remote.ResourceGroupID,
			SecurityGroupIDs: remote.SecurityGroupIDs,
		}
		networkInterface.Spec.IPv4 = remote.PrivateIPAddress
		if len(remote.IPv6Set) > 0 {
			networkInterface.Spec.IPv6 = remote.IPv6Set[0].IPAddress
		}

		if !cmp.Equal(*oldSpec, networkInterface.Spec, cmpopts.EquateEmpty()) {

			logr.FromContextOrDiscard(ctx).Info("spec not equal", "old", oldSpec, "new", networkInterface.Spec, "diff",
				cmp.Diff(*oldSpec, networkInterface.Spec, cmpopts.EquateEmpty()),
			)

			oldRv := networkInterface.GetResourceVersion()
			err = r.client.Update(ctx, networkInterface)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("update eni failed, %w", err)
			}

			// networkInterface should be updated
			_ = common.WaitRVChanged(ctx, r.client, networkInterface, networkInterface.Namespace, networkInterface.Name, oldRv)
		}

		networkInterface.Status.ENIInfo = v1beta1.ENIInfo{
			ID:               remote.NetworkInterfaceID,
			Type:             v1beta1.ENIType(remote.Type),
			Vid:              remote.DeviceIndex,
			NetworkCardIndex: ptr.To(remote.NetworkCardIndex),
			Status:           v1beta1.ENIStatusBind,
			VfID:             remote.VfID,
		}
		networkInterface.Status.Phase = v1beta1.ENIPhaseBind

		err = r.client.Status().Update(ctx, networkInterface)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("update eni status failed, %w", err)
		}
	}

	// add node label
	if networkInterface.Labels[types.ENIRelatedNodeName] != networkInterface.Status.NodeName {
		update := networkInterface.DeepCopy()
		if update.Labels == nil {
			update.Labels = make(map[string]string)
		}
		update.Labels[types.ENIRelatedNodeName] = networkInterface.Status.NodeName
		err = r.client.Patch(ctx, update, client.MergeFrom(networkInterface))
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to patch network interface labels: %w", err)
		}
	}

	return reconcile.Result{}, nil
}

// put a stand alone detach flow
func (r *ReconcileNetworkInterface) detach(ctx context.Context, networkInterface *v1beta1.NetworkInterface) (reconcile.Result, error) {
	var err error

	if networkInterface.Status.InstanceID != "" {
		if strings.HasPrefix(networkInterface.Name, "leni-") || strings.HasPrefix(networkInterface.Name, "hdeni-") {
			if strings.HasPrefix(networkInterface.Name, "leni-") {
				ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIEFLO)
			} else {
				ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIEFLOHDENI)
			}
			resp, err := r.aliyun.DescribeNetworkInterfaceV2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{networkInterface.Name},
				RawStatus:           ptr.To(true),
			})
			if err != nil {
				return reconcile.Result{}, err
			}
			if len(resp) > 0 {
				switch resp[0].Status {
				case aliyunClient.LENIStatusAvailable, aliyunClient.LENIStatusDetachFailed:
					// networkInterface.Spec.ENI.ID, networkInterface.Status.InstanceID, networkInterface.Status.TrunkENIID
					err = r.aliyun.DetachNetworkInterfaceV2(ctx, &aliyunClient.DetachNetworkInterfaceOptions{
						NetworkInterfaceID: toPtr(networkInterface.Name),
						InstanceID:         toPtr(networkInterface.Status.InstanceID),
					})
					if err != nil {
						return reconcile.Result{}, err
					}
					fallthrough
				case aliyunClient.LENIStatusExecuting, aliyunClient.LENIStatusDetaching:
					du, err := r.resourceBackoff.Get(networkInterface.Name, backoff.Backoff(backoff.WaitLENIStatus))
					if err != nil {
						return reconcile.Result{}, err
					}
					return reconcile.Result{RequeueAfter: du}, nil
				case aliyunClient.LENIStatusUnattached, aliyunClient.LENIStatusCreateFailed, aliyunClient.LENIStatusDeleting, aliyunClient.LENIStatusDeleteFailed, aliyunClient.LENIStatusAttachFailed:
					// ignore this status. detach assume succeed.
				default:
					return reconcile.Result{}, fmt.Errorf("unknown status %s", resp[0].Status)
				}
			}

		} else {
			err = r.aliyun.DetachNetworkInterfaceV2(ctx, &aliyunClient.DetachNetworkInterfaceOptions{
				NetworkInterfaceID: toPtr(networkInterface.Name),
				InstanceID:         toPtr(networkInterface.Status.InstanceID),
				TrunkID:            toPtr(networkInterface.Status.TrunkENIID),
			})
			if err != nil {
				return reconcile.Result{}, err
			}

			enis, err := r.aliyun.DescribeNetworkInterfaceV2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{networkInterface.Name},
				RawStatus:           ptr.To(true),
			})
			if err != nil {
				return reconcile.Result{}, err
			}

			if len(enis) > 0 && enis[0].Status != aliyunClient.ENIStatusAvailable {
				// wait next time
				du, err := r.resourceBackoff.Get(networkInterface.Name, backoff.Backoff(backoff.WaitENIStatus))
				if err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{RequeueAfter: du}, nil
			}
		}

		// always clean up the backoff, as we will update the cr status, so we will not go here again
		r.resourceBackoff.Del(networkInterface.Name)

		networkInterface.Status.ENIInfo.Status = v1beta1.ENIPhaseUnbind
		networkInterface.Status.ENIInfo.Vid = 0
		networkInterface.Status.ENIInfo.VfID = nil
		networkInterface.Status.ENIInfo.NetworkCardIndex = nil

		networkInterface.Status.InstanceID = ""
		networkInterface.Status.TrunkENIID = ""
		networkInterface.Status.NodeName = ""
		networkInterface.Status.NetworkCardIndex = nil
		networkInterface.Status.Phase = v1beta1.ENIPhaseUnbind
		err = r.client.Status().Update(ctx, networkInterface)

		if err != nil {
			return reconcile.Result{}, fmt.Errorf("update network interface status to %s failed, %w", v1beta1.ENIPhaseUnbind, err)
		}
	}

	// remove node label
	if networkInterface.Labels[types.ENIRelatedNodeName] != "" {
		update := networkInterface.DeepCopy()
		delete(update.Labels, types.ENIRelatedNodeName)
		err = r.client.Patch(ctx, update, client.MergeFrom(networkInterface))
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to remove network interface labels: %w", err)
		}
	}
	return reconcile.Result{}, nil
}

// put a stand alone detach flow
func (r *ReconcileNetworkInterface) delete(ctx context.Context, networkInterface *v1beta1.NetworkInterface) error {

	if strings.HasPrefix(networkInterface.Name, "leni-") {
		ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIEFLO)
	} else if strings.HasPrefix(networkInterface.Name, "hdeni-") {
		ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIEFLOHDENI)
	} else {
		ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIECS)
	}

	err := r.aliyun.DeleteNetworkInterfaceV2(ctx, networkInterface.Name)
	if err != nil {
		return err
	}
	r.resourceBackoff.Del(networkInterface.Name)

	update := networkInterface.DeepCopy()
	controllerutil.RemoveFinalizer(update, types.FinalizerENI)
	err = r.client.Patch(ctx, update, client.MergeFrom(networkInterface))
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove finalizer failed, %w", err)
	}
	// wait gone
	common.WaitDeleted(ctx, r.client, &v1beta1.NetworkInterface{}, networkInterface.Namespace, networkInterface.Name)
	return nil
}

func (r *ReconcileNetworkInterface) rollBackPodENI(ctx context.Context, networkInterface *v1beta1.NetworkInterface) error {
	if networkInterface.Spec.PodENIRef == nil || networkInterface.Spec.PodENIRef.Name == "" {
		return nil
	}

	l := logr.FromContextOrDiscard(ctx)

	podENI := &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkInterface.Spec.PodENIRef.Name,
			Namespace: networkInterface.Spec.PodENIRef.Namespace,
		},
	}

	err := r.client.Get(ctx, client.ObjectKeyFromObject(podENI), podENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return nil
		}
		return err
	}

	if podENI.DeletionTimestamp.IsZero() {
		l.Info("eni create failed, delete podENI")
		return r.client.Delete(ctx, podENI)
	}
	return nil
}

func toPtr(in string) *string {
	if in == "" {
		return nil
	}
	return &in
}
