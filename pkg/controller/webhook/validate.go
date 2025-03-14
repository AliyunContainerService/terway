package webhook

import (
	"context"
	"fmt"
	"net/http"
	"time"

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
				switch podNetworking.Spec.ENIOptions.ENIAttachType {
				case "", v1beta1.ENIOptionTypeDefault:
				default:
					return admission.Denied("attachType must be default when podSelector and namespaceSelector are not set")
				}
			}

			if len(podNetworking.Spec.VSwitchOptions) == 0 {
				return admission.Denied("vSwitchOptions is not set")
			}
			if len(podNetworking.Spec.SecurityGroupIDs) == 0 {
				return admission.Denied("security group is not set")
			}
			if len(podNetworking.Spec.SecurityGroupIDs) > 10 {
				return admission.Denied("security group can not more than 10")
			}

			if podNetworking.Spec.AllocationType.ReleaseStrategy == v1beta1.ReleaseStrategyTTL {
				_, err = time.ParseDuration(podNetworking.Spec.AllocationType.ReleaseAfter)
				if err != nil {
					return webhook.Denied(fmt.Sprintf("invalid releaseAfter %s", podNetworking.Spec.AllocationType.ReleaseAfter))
				}
			}

			if podNetworking.Spec.AllocationType.ReleaseStrategy == v1beta1.ReleaseStrategyNever {
				if podNetworking.Spec.AllocationType.ReleaseAfter != "" {
					return webhook.Denied("releaseAfter must be empty when releaseStrategy is never")
				}
			}

			return webhook.Allowed("checked")
		}),
	}
}
