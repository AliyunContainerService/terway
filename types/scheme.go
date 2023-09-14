package types

import (
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	aliv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var Scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(corev1.AddToScheme(Scheme))
	utilruntime.Must(aliv1beta1.AddToScheme(Scheme))
	utilruntime.Must(v1beta1.AddToScheme(Scheme))
}

func NewRESTMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper(Scheme.PreferredVersionAllGroups())
	// enumerate all supported versions, get the kinds, and register with the mapper how to address
	// our resources.
	for _, gv := range Scheme.PreferredVersionAllGroups() {
		for kind := range Scheme.KnownTypes(gv) {
			scope := meta.RESTScopeNamespace
			if rootScopedKinds[gv.WithKind(kind).GroupKind()] {
				scope = meta.RESTScopeRoot
			}
			mapper.Add(gv.WithKind(kind), scope)
		}
	}
	return mapper
}

// hardcoded is good enough for the test we're running
var rootScopedKinds = map[schema.GroupKind]bool{
	{Group: "admission.k8s.io", Kind: "AdmissionReview"}: true,

	{Group: "admissionregistration.k8s.io", Kind: "ValidatingWebhookConfiguration"}: true,
	{Group: "admissionregistration.k8s.io", Kind: "MutatingWebhookConfiguration"}:   true,

	{Group: "authentication.k8s.io", Kind: "TokenReview"}: true,

	{Group: "authorization.k8s.io", Kind: "SubjectAccessReview"}:     true,
	{Group: "authorization.k8s.io", Kind: "SelfSubjectAccessReview"}: true,
	{Group: "authorization.k8s.io", Kind: "SelfSubjectRulesReview"}:  true,

	{Group: "certificates.k8s.io", Kind: "CertificateSigningRequest"}: true,

	{Group: "", Kind: "Node"}:             true,
	{Group: "", Kind: "Namespace"}:        true,
	{Group: "", Kind: "PersistentVolume"}: true,
	{Group: "", Kind: "ComponentStatus"}:  true,

	{Group: "extensions", Kind: "PodSecurityPolicy"}: true,

	{Group: "policy", Kind: "PodSecurityPolicy"}: true,

	{Group: "extensions", Kind: "PodSecurityPolicy"}: true,

	{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole"}:        true,
	{Group: "rbac.authorization.k8s.io", Kind: "ClusterRoleBinding"}: true,

	{Group: "scheduling.k8s.io", Kind: "PriorityClass"}: true,

	{Group: "storage.k8s.io", Kind: "StorageClass"}:     true,
	{Group: "storage.k8s.io", Kind: "VolumeAttachment"}: true,

	{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}: true,

	{Group: "apiserver.k8s.io", Kind: "AdmissionConfiguration"}: true,

	{Group: "audit.k8s.io", Kind: "Event"}:  true,
	{Group: "audit.k8s.io", Kind: "Policy"}: true,

	{Group: "apiregistration.k8s.io", Kind: "APIService"}: true,

	{Group: "metrics.k8s.io", Kind: "NodeMetrics"}: true,

	{Group: "wardle.example.com", Kind: "Fischer"}: true,

	{Group: "network.alibabacloud.com", Kind: "PodNetworking"}: true,
}
