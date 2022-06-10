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
	"fmt"
	"net/http"
	"strconv"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"

	"gomodules.xyz/jsonpatch/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
func MutatingHook(client client.Client) *webhook.Admission {
	return &webhook.Admission{
		Handler: admission.HandlerFunc(func(ctx context.Context, req webhook.AdmissionRequest) webhook.AdmissionResponse {
			switch req.Kind.Kind {
			case "Pod":
				return podWebhook(ctx, &req, client)
			case "PodNetworking":
				return podNetworkingWebhook(ctx, req, client)
			}
			return webhook.Allowed("not care")
		}),
	}
}

func podWebhook(ctx context.Context, req *webhook.AdmissionRequest, client client.Client) webhook.AdmissionResponse {
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

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	// 2. get pod previous zone
	previousZone, err := getPreviousZone(ctx, client, pod)
	if err != nil {
		msg := fmt.Sprintf("error get previous podENI conf, %s", err)
		l.Error(err, msg)
		return webhook.Errored(1, fmt.Errorf(msg))
	}

	zones := sets.NewString()

	memberCount := 0

	// 1. check pod annotation config first
	if pod.Annotations[types.PodNetworking] != "" && pod.Annotations[types.PodNetworks] != "" {
		return webhook.Denied("can not use pod annotation and podNetworking at same time")
	}

	if types.PodUseENI(pod) || controlplane.GetConfig().IPAMType == types.IPAMTypeCRD {
		networks, err := controlplane.ParsePodNetworksFromAnnotation(pod)
		if err != nil {
			return webhook.Denied(fmt.Sprintf("unable parse annotation field %s, %s", types.PodNetworks, err))
		}

		if len(networks.PodNetworks) == 0 {
			networks.PodNetworks = append(networks.PodNetworks, controlplane.PodNetworks{Interface: eth0})
		}
		// validate and set default
		require := false
		iF := sets.NewString()
		for _, n := range networks.PodNetworks {
			if len(n.VSwitchOptions) == 0 || len(n.SecurityGroupIDs) == 0 || len(n.ExtraRoutes) == 0 {
				require = true
			}
			if len(n.SecurityGroupIDs) > 5 {
				return admission.Denied("security group can not more than 5")
			}
			if len(n.Interface) <= 0 || len(n.Interface) >= 6 {
				return admission.Denied("interface name should >0 and <6 ")
			}
			if iF.Has(n.Interface) {
				return admission.Denied("duplicated interface")
			}
			iF.Insert(n.Interface)

			if needPreviousZoneForAnnotation(previousZone, n) {
				zones.Insert(previousZone)
			}
		}

		if require {
			cfg, err := common.ConfigFromConfigMConfigFromConfigMap(ctx, client, "")
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
				if len(networks.PodNetworks[i].ExtraRoutes) == 0 {
					networks.PodNetworks[i].ExtraRoutes = cfg.ExtraRoutes
				}
			}
			pnaBytes, err := json.Marshal(networks)
			if err != nil {
				return webhook.Errored(1, err)
			}
			pod.Annotations[types.PodNetworks] = string(pnaBytes)
			pod.Annotations[types.PodENI] = "true"
		}
		memberCount = len(networks.PodNetworks)
	} else {
		if pod.Annotations[types.PodNetworks] != "" {
			return webhook.Denied("can not use pod annotation and podNetworking at same time, pod-eni is missing")
		}

		if previousZone != "" {
			zones.Insert(previousZone)
		}

		memberCount = 1

		// 1. check pod with podNetworking config and get one
		podNetworkings := &v1beta1.PodNetworkingList{}
		err = client.List(ctx, podNetworkings)
		if err != nil {
			return webhook.Errored(1, fmt.Errorf("error list podNetworking, %w", err))
		}

		ns := &corev1.Namespace{}
		err = client.Get(ctx, k8stypes.NamespacedName{
			Name: req.Namespace,
		}, ns)
		if err != nil {
			return webhook.Errored(1, fmt.Errorf("error get namespace, %w", err))
		}

		podNetworking, err := common.MatchOnePodNetworking(pod, ns, podNetworkings.Items)
		if err != nil {
			l.Error(err, "error match podNetworking")
			return webhook.Errored(1, err)
		}
		if podNetworking == nil {
			l.V(5).Info("no selector is matched or CRD is not ready")
			return webhook.Allowed("not match")
		}
		pod.Annotations[types.PodENI] = "true"
		pod.Annotations[types.PodNetworking] = podNetworking.Name

		if previousZone == "" {
			// 3. if no previous conf found, we will add zone limit by vSwitches
			for _, vsw := range podNetworking.Status.VSwitches {
				zones.Insert(vsw.Zone)
			}
		}
	}
	resName := deviceplugin.MemberENIResName
	if !*controlplane.GetConfig().EnableTrunk {
		resName = deviceplugin.ENIResName
	}
	setResourceRequest(pod, resName, memberCount)

	setNodeAffinityByZones(pod, zones.List())

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
	l.Info("patch pod for trunking")
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

	cfg, err := common.ConfigFromConfigMConfigFromConfigMap(ctx, client, "")
	if err != nil {
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

func getPreviousZone(ctx context.Context, client client.Client, pod *corev1.Pod) (string, error) {
	if !utils.IsStsPod(pod) {
		return "", nil
	}

	podENI := &v1beta1.PodENI{}
	err := client.Get(ctx, k8stypes.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, podENI)
	if err != nil {
		if errors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if len(podENI.Spec.Allocations) == 0 {
		return "", nil
	}
	return podENI.Spec.Zone, nil
}

func setResourceRequest(pod *corev1.Pod, resName string, count int) {
	if count == 0 {
		return
	}
	// we only patch one container for res request
	if pod.Spec.Containers[0].Resources.Requests == nil {
		pod.Spec.Containers[0].Resources.Requests = make(corev1.ResourceList)
	}
	if pod.Spec.Containers[0].Resources.Limits == nil {
		pod.Spec.Containers[0].Resources.Limits = make(corev1.ResourceList)
	}

	pod.Spec.Containers[0].Resources.Requests[corev1.ResourceName(resName)] = resource.MustParse(strconv.Itoa(count))
	pod.Spec.Containers[0].Resources.Limits[corev1.ResourceName(resName)] = resource.MustParse(strconv.Itoa(count))
}

func setNodeAffinityByZones(pod *corev1.Pod, zones []string) {
	if utils.IsDaemonSetPod(pod) || len(zones) == 0 {
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
		pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[i].MatchExpressions = append(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[i].MatchExpressions, corev1.NodeSelectorRequirement{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   zones,
		})
	}
}
