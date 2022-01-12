package webhook

import (
	"context"
	"fmt"
	"net/http"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"

	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var validateLog = ctrl.Log.WithName("validate-webhook")

// ValidateHook ValidateHook
func ValidateHook() *webhook.Admission {
	return &webhook.Admission{
		Handler: admission.HandlerFunc(func(ctx context.Context, req webhook.AdmissionRequest) webhook.AdmissionResponse {
			validateLog.Info("obj in", "kind", req.Kind.Kind, "name", req.Name, "res", req.Resource.String())
			if req.Kind.Kind != "PodNetworking" {
				return webhook.Allowed("not care")
			}

			original := req.Object.Raw

			podNetworking := &v1beta1.PodNetworking{}
			err := json.Unmarshal(original, podNetworking)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed decoding podNetworking: %s, %w", string(original), err))
			}
			l := log.WithName(podNetworking.Name)
			l.Info("checking podNetworking")
			if podNetworking.Spec.Selector.PodSelector == nil && podNetworking.Spec.Selector.NamespaceSelector == nil {
				return admission.Denied("neither the PodSelector nor the NamespaceSelector is set")
			}
			if len(podNetworking.Spec.VSwitchOptions) == 0 {
				return admission.Denied("vSwitchOptions is not set")
			}
			if len(podNetworking.Spec.SecurityGroupIDs) == 0 {
				return admission.Denied("security group is not set")
			}
			if len(podNetworking.Spec.SecurityGroupIDs) > 5 {
				return admission.Denied("security group can not more than 5")
			}
			return webhook.Allowed("checked")
		}),
	}
}
