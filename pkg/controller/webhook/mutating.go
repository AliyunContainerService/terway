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

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"

	"gomodules.xyz/jsonpatch/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var log = ctrl.Log.WithName("mutating-webhook")

// MutatingHook MutatingHook
func MutatingHook(client client.Client) *webhook.Admission {
	return &webhook.Admission{
		Handler: admission.HandlerFunc(func(ctx context.Context, req webhook.AdmissionRequest) webhook.AdmissionResponse {
			switch req.Kind.Kind {
			case "Pod":
				if req.Namespace == "kube-system" {
					return webhook.Allowed("namespace is kube-system")
				}
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
	l.Info("checking pod")

	if pod.Spec.HostNetwork {
		return webhook.Allowed("host network")
	}

	if utils.IsDaemonSetPod(pod) {
		return webhook.Allowed("daemonSet pod")
	}

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
		l.V(4).Info("no selector is matched or CRD is not ready")
		return webhook.Allowed("not match")
	}

	if len(pod.Spec.Containers) == 0 {
		return webhook.Allowed("pod do not have containers")
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[types.PodENI] = "true"
	pod.Annotations[types.PodNetworking] = podNetworking.Name

	// we only patch one container for res request
	if pod.Spec.Containers[0].Resources.Requests == nil {
		pod.Spec.Containers[0].Resources.Requests = make(corev1.ResourceList)
	}
	if pod.Spec.Containers[0].Resources.Limits == nil {
		pod.Spec.Containers[0].Resources.Limits = make(corev1.ResourceList)
	}
	pod.Spec.Containers[0].Resources.Requests[deviceplugin.MemberENIResName] = resource.MustParse("1")
	pod.Spec.Containers[0].Resources.Limits[deviceplugin.MemberENIResName] = resource.MustParse("1")

	// 2. get pod previous zone
	previousZone, err := getPreviousZone(client, pod)
	if err != nil {
		msg := fmt.Sprintf("error get previous podENI conf, %s", err)
		l.Error(err, msg)
		return webhook.Errored(1, fmt.Errorf(msg))
	}
	var zones []string
	if previousZone != "" {
		zones = []string{previousZone}
	} else {
		// 3. if no previous conf found, we will add zone limit by vSwitches
		for _, vsw := range podNetworking.Status.VSwitches {
			zones = append(zones, vsw.Zone)
		}
	}
	if len(zones) > 0 {
		if pod.Spec.Affinity == nil {
			pod.Spec.Affinity = &corev1.Affinity{}
		}
		if pod.Spec.Affinity.NodeAffinity == nil {
			pod.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
		}
		if pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
			pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{}
		}

		pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms,
			corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      corev1.LabelZoneFailureDomainStable,
						Operator: corev1.NodeSelectorOpIn,
						Values:   zones,
					},
				},
			})
	}
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

	if len(podNetworking.Spec.SecurityGroupIDs) > 0 && len(podNetworking.Spec.VSwitchIDs) > 0 {
		return webhook.Allowed("podNetworking all set")
	}
	cm := &corev1.ConfigMap{}
	err = client.Get(ctx, k8stypes.NamespacedName{
		Namespace: "kube-system",
		Name:      "eni-config",
	}, cm)
	if err != nil {
		return webhook.Errored(1, fmt.Errorf("error get terway configmap eni-config, %w", err))
	}
	eniConfStr, ok := cm.Data["eni_conf"]
	if !ok {
		return webhook.Errored(1, fmt.Errorf("error parse terway configmap eni-config, %w", err))
	}

	eniConf, err := types.MergeConfigAndUnmarshal(nil, []byte(eniConfStr))
	if err != nil {
		return webhook.Errored(1, fmt.Errorf("error parse terway configmap eni-config, %w", err))
	}
	if len(podNetworking.Spec.SecurityGroupIDs) == 0 {
		podNetworking.Spec.SecurityGroupIDs = []string{eniConf.SecurityGroup}
	}
	if len(podNetworking.Spec.VSwitchIDs) == 0 {
		for _, ids := range eniConf.VSwitches {
			podNetworking.Spec.VSwitchIDs = append(podNetworking.Spec.VSwitchIDs, ids...)
		}
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

func getPreviousZone(client client.Client, pod *corev1.Pod) (string, error) {
	if !utils.IsStsPod(pod) {
		return "", nil
	}

	podENI := &v1beta1.PodENI{}
	err := client.Get(context.TODO(), k8stypes.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, podENI)
	if err != nil {
		if errors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return podENI.Spec.Allocation.ENI.Zone, nil
}
