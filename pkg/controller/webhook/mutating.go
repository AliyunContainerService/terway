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

package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
	"github.com/AliyunContainerService/terway/types/daemon"

	"gomodules.xyz/jsonpatch/v2"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var log = ctrl.Log.WithName("mutating-webhook")

const eth0 = "eth0"

// MutatingHook MutatingHook
func MutatingHook(client client.Client, config *controlplane.Config) *webhook.Admission {
	return &webhook.Admission{
		Handler: admission.HandlerFunc(func(ctx context.Context, req webhook.AdmissionRequest) webhook.AdmissionResponse {
			switch req.Kind.Kind {
			case "Pod":
				return podWebhook(ctx, &req, client, config)
			case "PodNetworking":
				return podNetworkingWebhook(ctx, req, client)
			}
			return webhook.Allowed("not care")
		}),
	}
}

func podWebhook(ctx context.Context, req *webhook.AdmissionRequest, client client.Client, config *controlplane.Config) webhook.AdmissionResponse {
	original := req.Object.Raw
	pod := &corev1.Pod{}
	err := json.Unmarshal(original, pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed decoding pod: %s, %w", string(original), err))
	}
	l := log.WithName(k8stypes.NamespacedName{
		Namespace: req.Namespace,
		Name:      req.Name, // for non sts pod the name is empty
	}.String())
	l.V(5).Info("checking pod")

	if pod.Spec.HostNetwork {
		return webhook.Allowed("host network")
	}

	if len(pod.Spec.Containers) == 0 {
		return webhook.Allowed("pod do not have containers")
	}

	if types.IgnoredByTerway(pod.Labels) {
		return webhook.Allowed("pod is not managed by terway")
	}

	_, hasPodNetworks := pod.Annotations[types.PodNetworks]
	_, hasPodNetworksRequest := pod.Annotations[types.PodNetworksRequest]
	_, hasPodNetworking := pod.Annotations[types.PodNetworking]

	if (hasPodNetworks && hasPodNetworksRequest) || (hasPodNetworks && hasPodNetworking) || (hasPodNetworksRequest && hasPodNetworking) {
		return webhook.Denied(fmt.Sprintf("pod annotations %s, %s, and %s must be mutually exclusive", types.PodNetworks, types.PodNetworksRequest, types.PodNetworking))
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	// 2. get pod previous zone
	previousZone, err := getPreviousZone(ctx, client, pod)
	if err != nil {
		msg := fmt.Sprintf("error get previous podENI conf, %s", err)
		l.Error(err, msg)
		return webhook.Errored(1, errors.New(msg))
	}

	// 1. pod annotation config
	// 2. pod network requests
	// 3. pod match podNetworking
	// 4. write default config from eni-config
	networks, err := controlplane.ParsePodNetworksFromAnnotation(pod)
	if err != nil {
		return webhook.Denied(fmt.Sprintf("unable parse annotation field %s, %s", types.PodNetworks, err))
	}

	prevZone := sets.NewString()
	vSwitchZone := sets.NewString()

	if len(networks.PodNetworks) == 0 {
		podNetworks, zones, err := getPodNetworkRequests(ctx, client, pod.Annotations)
		if err != nil {
			return webhook.Denied(fmt.Sprintf("unable parse annotation %s, %s", types.PodNetworksRequest, err))
		}

		if len(podNetworks) > 0 {
			networks.PodNetworks = podNetworks
			vSwitchZone = vSwitchZone.Insert(zones...)
		} else {

			// get pn
			podNetworking, err := matchOnePodNetworking(ctx, req.Namespace, client, pod)
			if err != nil {
				l.Error(err, "error match podNetworking")
				return webhook.Errored(1, err)
			}
			if podNetworking == nil {
				if config.IPAMType != types.IPAMTypeCRD {
					if !types.PodUseENI(pod) {
						l.V(5).Info("no selector is matched or CRD is not ready")
						return webhook.Allowed("not match")
					}
					// allow use default config if in CRD mode
				}

				networks.PodNetworks = append(networks.PodNetworks, controlplane.PodNetworks{Interface: eth0})
			} else {
				// use config from pn
				pod.Annotations[types.PodNetworking] = podNetworking.Name
				networks.PodNetworks = append(networks.PodNetworks, podNetworkingToPodNetworks(podNetworking))

				for _, vsw := range podNetworking.Status.VSwitches {
					vSwitchZone.Insert(vsw.Zone)
				}
			}
		}
	}

	// validate and set default
	require := false
	iF := sets.NewString()
	for i, n := range networks.PodNetworks {
		if len(n.VSwitchOptions) == 0 || len(n.SecurityGroupIDs) == 0 {
			require = true
		}
		if len(n.SecurityGroupIDs) > 10 {
			return admission.Denied("security group can not more than 10")
		}
		if len(n.Interface) <= 0 || len(n.Interface) >= 6 {
			return admission.Denied("interface name should >0 and <6 ")
		}
		if iF.Has(n.Interface) {
			return admission.Denied("duplicated interface")
		}
		iF.Insert(n.Interface)

		if n.AllocationType == nil {
			networks.PodNetworks[i].AllocationType = &v1beta1.AllocationType{
				Type: v1beta1.IPAllocTypeElastic,
			}
		}
		// only set prev zone for fixed ip
		if networks.PodNetworks[i].AllocationType.Type == v1beta1.IPAllocTypeFixed {
			if !utils.IsFixedNamePod(pod) {
				return admission.Denied("fixed ip only support for fixed name pod")
			}
			if previousZone != "" {
				prevZone.Insert(previousZone)
			}
		}
	}

	if require {
		cfg, err := daemon.ConfigFromConfigMap(ctx, client, "")
		if err != nil {
			return webhook.Errored(1, err)
		}
		for i := range networks.PodNetworks {
			// for now only fill eth0
			if networks.PodNetworks[i].Interface != eth0 {
				continue
			}
			if len(networks.PodNetworks[i].VSwitchOptions) == 0 {
				networks.PodNetworks[i].VSwitchOptions = cfg.GetVSwitchIDs()
			}
			if len(networks.PodNetworks[i].SecurityGroupIDs) == 0 {
				networks.PodNetworks[i].SecurityGroupIDs = cfg.GetSecurityGroups()
			}
		}

	}
	pnaBytes, err := json.Marshal(networks)
	if err != nil {
		return webhook.Errored(1, err)
	}
	pod.Annotations[types.PodNetworks] = string(pnaBytes)
	pod.Annotations[types.PodENI] = "true"

	if *config.EnableWebhookInjectResource {
		setResourceRequest(pod, networks.PodNetworks, *config.EnableTrunk)
	}

	setNodeAffinityByZones(pod, prevZone.List(), vSwitchZone.List())

	podPatched, err := json.Marshal(pod)
	if err != nil {
		l.Error(err, "error marshal pod")
		return webhook.Errored(1, err)
	}
	patches, err := jsonpatch.CreatePatch(original, podPatched)
	if err != nil {
		l.Error(err, "error create patch")
		return webhook.Errored(1, err)
	}
	l.Info("patch pod for trunking", "patch", patches)
	return webhook.Patched("ok", patches...)
}

func podNetworkingWebhook(ctx context.Context, req webhook.AdmissionRequest, client client.Client) webhook.AdmissionResponse {
	original := req.Object.Raw
	podNetworking := &v1beta1.PodNetworking{}
	err := json.Unmarshal(original, podNetworking)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed decoding res: %s, %w", string(original), err))
	}
	l := log.WithName(podNetworking.Name)
	l.Info("checking podNetworking")

	if len(podNetworking.Spec.SecurityGroupIDs) > 0 && len(podNetworking.Spec.VSwitchOptions) > 0 {
		return webhook.Allowed("podNetworking all set")
	}

	cfg, err := daemon.ConfigFromConfigMap(ctx, client, "")
	if err != nil {
		if k8sErr.IsNotFound(err) {
			// let the validate do the job
			return webhook.Allowed("no terway eni-config found")
		}
		return webhook.Errored(1, err)
	}
	if len(podNetworking.Spec.SecurityGroupIDs) == 0 {
		podNetworking.Spec.SecurityGroupIDs = cfg.GetSecurityGroups()
	}
	if len(podNetworking.Spec.VSwitchOptions) == 0 {
		podNetworking.Spec.VSwitchOptions = cfg.GetVSwitchIDs()
	}
	podNetworkingPatched, err := json.Marshal(podNetworking)
	if err != nil {
		l.Error(err, "error marshal podNetworking")
		return webhook.Errored(1, err)
	}
	patches, err := jsonpatch.CreatePatch(original, podNetworkingPatched)
	if err != nil {
		l.Error(err, "error create patch")
		return webhook.Errored(1, err)
	}
	l.Info("patch podNetworking with terway default config")
	return webhook.Patched("ok", patches...)
}

// matchOnePodNetworking will range all podNetworking and try to found a matched podNetworking for this pod
// for stateless pod Fixed ip config is never matched
func matchOnePodNetworking(ctx context.Context, namespace string, client client.Client, pod *corev1.Pod) (*v1beta1.PodNetworking, error) {
	podNetworkings := &v1beta1.PodNetworkingList{}
	err := client.List(ctx, podNetworkings)
	if err != nil {
		return nil, fmt.Errorf("error list podNetworking, %w", err)
	}
	if len(podNetworkings.Items) == 0 {
		return nil, nil
	}

	ns := &corev1.Namespace{}
	err = client.Get(ctx, k8stypes.NamespacedName{
		Name: namespace,
	}, ns)
	if err != nil {
		return nil, fmt.Errorf("error get namespace, %w", err)
	}

	podLabels := labels.Set(pod.Labels)
	nsLabels := labels.Set(ns.Labels)
	for _, podNetworking := range podNetworkings.Items {
		if podNetworking.Status.Status != v1beta1.NetworkingStatusReady {
			continue
		}
		if !utils.IsFixedNamePod(pod) {
			// for fixed ip , only match sts pod
			if podNetworking.Spec.AllocationType.Type == v1beta1.IPAllocTypeFixed {
				continue
			}
		}

		matchOne := false
		if podNetworking.Spec.Selector.PodSelector != nil {
			ok, err := PodMatchSelector(podNetworking.Spec.Selector.PodSelector, podLabels)
			if err != nil {
				return nil, fmt.Errorf("error match pod selector, %w", err)
			}
			if !ok {
				continue
			}
			matchOne = true
		}
		if podNetworking.Spec.Selector.NamespaceSelector != nil {
			ok, err := PodMatchSelector(podNetworking.Spec.Selector.NamespaceSelector, nsLabels)
			if err != nil {
				return nil, fmt.Errorf("error match namespace selector, %w", err)
			}
			if !ok {
				continue
			}
			matchOne = true
		}
		if matchOne {
			return &podNetworking, nil
		}
	}
	return nil, nil
}

func getPreviousZone(ctx context.Context, client client.Client, pod *corev1.Pod) (string, error) {
	if !utils.IsFixedNamePod(pod) {
		return "", nil
	}

	podENI := &v1beta1.PodENI{}
	err := client.Get(ctx, k8stypes.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, podENI)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if !podENI.DeletionTimestamp.IsZero() {
		return "", nil
	}
	if len(podENI.Spec.Allocations) == 0 {
		return "", nil
	}
	return podENI.Spec.Zone, nil
}

func setResourceRequest(pod *corev1.Pod, podNetworks []controlplane.PodNetworks, enableTrunk bool) {
	count := len(podNetworks)
	if count == 0 {
		return
	}
	// we only patch one container for res request
	index := 0

	resName := deviceplugin.MemberENIResName
	if enableTrunk {
		// when user specific stander eni
		lo.ForEach(podNetworks, func(item controlplane.PodNetworks, index int) {
			if item.ENIOptions.ENIAttachType == v1beta1.ENIOptionTypeENI {
				resName = deviceplugin.ENIResName
			}
		})
	} else {
		// for legacy eniOnly case
		resName = deviceplugin.ENIResName
	}

	if pod.Spec.Containers[index].Resources.Requests == nil {
		pod.Spec.Containers[index].Resources.Requests = make(corev1.ResourceList)
	}
	if pod.Spec.Containers[index].Resources.Limits == nil {
		pod.Spec.Containers[index].Resources.Limits = make(corev1.ResourceList)
	}

	pod.Spec.Containers[index].Resources.Requests[corev1.ResourceName(resName)] = resource.MustParse(strconv.Itoa(count))
	pod.Spec.Containers[index].Resources.Limits[corev1.ResourceName(resName)] = resource.MustParse(strconv.Itoa(count))
}

func setNodeAffinityByZones(pod *corev1.Pod, zones ...[]string) {
	if utils.IsDaemonSetPod(pod) || len(zones) == 0 {
		return
	}
	contain := false
	for _, zone := range zones {
		if len(zone) == 0 {
			continue
		}
		contain = true
	}
	if !contain {
		return
	}

	if pod.Spec.Affinity == nil {
		pod.Spec.Affinity = &corev1.Affinity{}
	}
	if pod.Spec.Affinity.NodeAffinity == nil {
		pod.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	if pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{}
	}

	if len(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
		pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms, corev1.NodeSelectorTerm{})
	}
	for i := range pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, zone := range zones {
			if len(zone) == 0 {
				continue
			}
			pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[i].MatchExpressions = append(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[i].MatchExpressions, corev1.NodeSelectorRequirement{
				Key:      corev1.LabelTopologyZone,
				Operator: corev1.NodeSelectorOpIn,
				Values:   zone,
			})
		}
	}
}

// PodMatchSelector pod is selected by selector
func PodMatchSelector(labelSelector *metav1.LabelSelector, l labels.Set) (bool, error) {
	selector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return false, err
	}
	return selector.Matches(l), nil
}

func podNetworkingToPodNetworks(pn *v1beta1.PodNetworking) controlplane.PodNetworks {
	return controlplane.PodNetworks{
		Interface:            eth0,
		VSwitchOptions:       pn.Spec.VSwitchOptions,
		SecurityGroupIDs:     pn.Spec.SecurityGroupIDs,
		ENIOptions:           pn.Spec.ENIOptions,
		VSwitchSelectOptions: pn.Spec.VSwitchSelectOptions,
		AllocationType:       &pn.Spec.AllocationType,
	}
}

// getPodNetworkRequests parse the PodNetworking to PodNetworksAnnotation, and vswitch zone is checked
func getPodNetworkRequests(ctx context.Context, client client.Client, anno map[string]string) ([]controlplane.PodNetworks, []string, error) {
	reqs, err := controlplane.ParsePodNetworksFromRequest(anno)
	if err != nil {
		return nil, nil, err
	}

	if len(reqs) == 0 {
		return nil, nil, nil
	}

	unioned := sets.NewString()
	// if present convert to the PodNetworksAnnotation
	podNetworks := make([]controlplane.PodNetworks, 0, len(reqs))
	for index, req := range reqs {
		podNetworking := &v1beta1.PodNetworking{}

		err = client.Get(ctx, k8stypes.NamespacedName{
			Name: req.Network,
		}, podNetworking)
		if err != nil {
			return nil, nil, err
		}

		if podNetworking.Status.Status != v1beta1.NetworkingStatusReady {
			return nil, nil, fmt.Errorf("pod networking %s is not ready", req.Network)
		}
		if podNetworking.Spec.Selector.PodSelector != nil || podNetworking.Spec.Selector.NamespaceSelector != nil {
			return nil, nil, fmt.Errorf("pod networking %s should not have selector", req.Network)
		}

		zones := sets.NewString()
		lo.ForEach(podNetworking.Status.VSwitches, func(item v1beta1.VSwitch, index int) {
			zones.Insert(item.Zone)
		})

		if index == 0 {
			unioned = zones
		} else {
			unioned = unioned.Intersection(zones)
		}

		parsed := podNetworkingToPodNetworks(podNetworking)
		if req.InterfaceName != "" {
			parsed.Interface = req.InterfaceName
		}
		if req.DefaultRoute {
			parsed.DefaultRoute = true
		}
		if req.Routes != nil {
			parsed.ExtraRoutes = req.Routes
		}
		podNetworks = append(podNetworks, parsed)
	}

	return podNetworks, unioned.List(), nil
}
