package webhook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AliyunContainerService/terway/types/controlplane"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func TestValidateHookAllowsWhenKindIsNotPodNetworking(t *testing.T) {
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "Foo",
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(true)}).Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
	assert.Equal(t, "not care", resp.Result.Message)
}

func TestValidateHookDeniesWhenVSwitchOptionsIsEmpty(t *testing.T) {
	podNetworking := &v1beta1.PodNetworking{
		Spec: v1beta1.PodNetworkingSpec{
			Selector: v1beta1.Selector{
				PodSelector: &metav1.LabelSelector{},
			},
		},
	}
	raw, _ := json.Marshal(podNetworking)
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(true)}).Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Equal(t, "vSwitchOptions is not set", resp.Result.Message)
}

func TestValidateHookDeniesWhenSecurityGroupIDsIsEmpty(t *testing.T) {
	podNetworking := &v1beta1.PodNetworking{
		Spec: v1beta1.PodNetworkingSpec{
			Selector: v1beta1.Selector{
				PodSelector: &metav1.LabelSelector{},
			},
			VSwitchOptions: []string{"vsw-123"},
		},
	}
	raw, _ := json.Marshal(podNetworking)
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(true)}).Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Equal(t, "security group is not set", resp.Result.Message)
}

func TestValidateHookDeniesWhenSecurityGroupIDsExceedsLimit(t *testing.T) {
	podNetworking := &v1beta1.PodNetworking{
		Spec: v1beta1.PodNetworkingSpec{
			Selector: v1beta1.Selector{
				PodSelector: &metav1.LabelSelector{},
			},
			VSwitchOptions:   []string{"vsw-123"},
			SecurityGroupIDs: []string{"sg-1", "sg-2", "sg-3", "sg-4", "sg-5", "sg-6", "sg-7", "8", "9", "10", "11"},
		},
	}
	raw, _ := json.Marshal(podNetworking)
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(true)}).Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Equal(t, "security group can not more than 10", resp.Result.Message)
}

func TestValidateHookDeniesWhenReleaseAfterIsInvalid(t *testing.T) {
	podNetworking := &v1beta1.PodNetworking{
		Spec: v1beta1.PodNetworkingSpec{
			Selector: v1beta1.Selector{
				PodSelector: &metav1.LabelSelector{},
			},
			VSwitchOptions:   []string{"vsw-123"},
			SecurityGroupIDs: []string{"sg-1"},
			AllocationType: v1beta1.AllocationType{
				ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
				ReleaseAfter:    "invalid-duration",
			},
		},
	}
	raw, _ := json.Marshal(podNetworking)
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(true)}).Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
}

func TestValidateHookAllowsWhenAllConditionsAreMet(t *testing.T) {
	podNetworking := &v1beta1.PodNetworking{
		Spec: v1beta1.PodNetworkingSpec{
			Selector: v1beta1.Selector{
				PodSelector: &metav1.LabelSelector{},
			},
			VSwitchOptions:   []string{"vsw-123"},
			SecurityGroupIDs: []string{"sg-1"},
			AllocationType: v1beta1.AllocationType{
				ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
				ReleaseAfter:    "1h",
			},
		},
	}
	raw, _ := json.Marshal(podNetworking)
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(true)}).Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
	assert.Equal(t, "checked", resp.Result.Message)
}

func TestValidateHookAllowsENIAttachTypeWhenWebhookInjectionEnabled(t *testing.T) {
	// When EnableWebhookInjectResource = true and no selectors are set,
	// ENIAttachType should be allowed to be non-default (ENI)
	podNetworking := &v1beta1.PodNetworking{
		Spec: v1beta1.PodNetworkingSpec{
			ENIOptions: v1beta1.ENIOptions{
				ENIAttachType: v1beta1.ENIOptionTypeENI, // Non-default type
			},
			// No PodSelector or NamespaceSelector set
			VSwitchOptions:   []string{"vsw-123"},
			SecurityGroupIDs: []string{"sg-1"},
		},
	}
	raw, _ := json.Marshal(podNetworking)
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(true)}).Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
	assert.Equal(t, "checked", resp.Result.Message)
}

func TestValidateHookDeniesNonDefaultENIAttachTypeWhenWebhookInjectionDisabled(t *testing.T) {
	// When EnableWebhookInjectResource = false and no selectors are set,
	// ENIAttachType must be default
	podNetworking := &v1beta1.PodNetworking{
		Spec: v1beta1.PodNetworkingSpec{
			ENIOptions: v1beta1.ENIOptions{
				ENIAttachType: v1beta1.ENIOptionTypeENI, // Non-default type should be denied
			},
			// No PodSelector or NamespaceSelector set
			VSwitchOptions:   []string{"vsw-123"},
			SecurityGroupIDs: []string{"sg-1"},
		},
	}
	raw, _ := json.Marshal(podNetworking)
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(false)}).Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	assert.Equal(t, "attachType must be default when podSelector and namespaceSelector are not set", resp.Result.Message)
}

func TestValidateHookAllowsDefaultENIAttachTypeWhenWebhookInjectionDisabled(t *testing.T) {
	// When EnableWebhookInjectResource = false and no selectors are set,
	// ENIAttachType = default should be allowed
	podNetworking := &v1beta1.PodNetworking{
		Spec: v1beta1.PodNetworkingSpec{
			ENIOptions: v1beta1.ENIOptions{
				ENIAttachType: v1beta1.ENIOptionTypeDefault, // Default type should be allowed
			},
			// No PodSelector or NamespaceSelector set
			VSwitchOptions:   []string{"vsw-123"},
			SecurityGroupIDs: []string{"sg-1"},
		},
	}
	raw, _ := json.Marshal(podNetworking)
	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "",
				Kind:    "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
	resp := ValidateHook(&controlplane.Config{EnableWebhookInjectResource: ptr.To(false)}).Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
	assert.Equal(t, "checked", resp.Result.Message)
}
