//go:build e2e

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jeffail/gabs/v2"
	"golang.org/x/mod/semver"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

type Pod struct {
	*corev1.Pod
}

func NewPod(name, namespace string) *Pod {
	return &Pod{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: func() *int64 {
					i := int64(0)
					return &i
				}(),
			},
		},
	}
}

func (p *Pod) WithLabels(labels map[string]string) *Pod {
	if p.Labels == nil {
		p.Labels = make(map[string]string)
	}
	for k, v := range labels {
		p.Labels[k] = v
	}
	return p
}

func (p *Pod) WithContainer(name, image string, command []string) *Pod {
	p.Spec.Containers = []corev1.Container{
		{
			Name:            name,
			Image:           image,
			ImagePullPolicy: "IfNotPresent",
			Command:         command,
		},
	}
	return p
}

func (p *Pod) WithNodeAffinity(labels map[string]string) *Pod {
	var nodeSelectorTerms []corev1.NodeSelectorRequirement
	for k, v := range labels {
		nodeSelectorTerms = append(nodeSelectorTerms, corev1.NodeSelectorRequirement{
			Key:      k,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{v},
		})
	}

	if len(nodeSelectorTerms) == 0 {
		return p
	}
	if p.Spec.Affinity == nil {
		p.Spec.Affinity = &corev1.Affinity{}
	}
	if p.Spec.Affinity.NodeAffinity == nil {
		p.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	if p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{}
	}
	if len(p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
		p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = []corev1.NodeSelectorTerm{
			{MatchExpressions: nodeSelectorTerms},
		}
	} else {
		p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions = append(
			p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions,
			nodeSelectorTerms...,
		)
	}
	return p
}

func (p *Pod) WithNodeAffinityExclude(excludeLabels map[string]string) *Pod {
	var nodeSelectorTerms []corev1.NodeSelectorRequirement
	for k, v := range excludeLabels {
		nodeSelectorTerms = append(nodeSelectorTerms, corev1.NodeSelectorRequirement{
			Key:      k,
			Operator: corev1.NodeSelectorOpNotIn,
			Values:   []string{v},
		})
	}

	if len(nodeSelectorTerms) == 0 {
		return p
	}
	if p.Spec.Affinity == nil {
		p.Spec.Affinity = &corev1.Affinity{}
	}
	if p.Spec.Affinity.NodeAffinity == nil {
		p.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	if p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{}
	}
	if len(p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
		p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = []corev1.NodeSelectorTerm{
			{MatchExpressions: nodeSelectorTerms},
		}
	} else {
		p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions = append(
			p.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions,
			nodeSelectorTerms...,
		)
	}
	return p
}

func (p *Pod) WithPodAffinity(labels map[string]string) *Pod {
	if p.Spec.Affinity == nil {
		p.Spec.Affinity = &corev1.Affinity{}
	}
	p.Spec.Affinity.PodAffinity = &corev1.PodAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
			{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
				TopologyKey: "kubernetes.io/hostname",
			},
		},
	}
	return p
}

func (p *Pod) WithPodAntiAffinity(labels map[string]string) *Pod {
	p.Spec.Affinity = &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					TopologyKey: "kubernetes.io/hostname",
				},
			},
		},
	}
	return p
}

func (p *Pod) WithHostNetwork() *Pod {
	p.Spec.HostNetwork = true
	return p
}

func (p *Pod) WithHealthCheck(port int32) *Pod {
	p.Spec.Containers[0].LivenessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Port: intstr.FromInt(int(port)),
			},
		},
		InitialDelaySeconds: 1,
		TimeoutSeconds:      2,
		PeriodSeconds:       2,
		SuccessThreshold:    1,
		FailureThreshold:    3,
	}
	p.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Port: intstr.FromInt(int(port)),
			},
		},
		InitialDelaySeconds: 1,
		TimeoutSeconds:      2,
		PeriodSeconds:       2,
		SuccessThreshold:    1,
		FailureThreshold:    3,
	}
	return p
}

func (p *Pod) WithDNSPolicy(policy corev1.DNSPolicy) *Pod {
	p.Spec.DNSPolicy = policy
	return p
}

func (p *Pod) WithHostPort(containerPort, hostPort int32) *Pod {
	if len(p.Spec.Containers) == 0 {
		return p
	}

	p.Spec.Containers[0].Ports = append(p.Spec.Containers[0].Ports, corev1.ContainerPort{
		ContainerPort: containerPort,
		HostPort:      hostPort,
		Protocol:      corev1.ProtocolTCP,
	})

	return p
}

func (p *Pod) WithAnnotations(annotations map[string]string) *Pod {
	if p.Annotations == nil {
		p.Annotations = make(map[string]string)
	}
	for k, v := range annotations {
		p.Annotations[k] = v
	}
	return p
}

func (p *Pod) WithPodNetworking(pnName string) *Pod {
	if p.Labels == nil {
		p.Labels = make(map[string]string)
	}
	p.Labels["netplan"] = pnName
	return p
}

func (p *Pod) WithOwnerReference(ref metav1.OwnerReference) *Pod {
	p.OwnerReferences = append(p.OwnerReferences, ref)
	return p
}

func (p *Pod) WithResourceRequests(resources corev1.ResourceList) *Pod {
	if len(p.Spec.Containers) == 0 {
		return p
	}
	if p.Spec.Containers[0].Resources.Requests == nil {
		p.Spec.Containers[0].Resources.Requests = make(corev1.ResourceList)
	}
	for k, v := range resources {
		p.Spec.Containers[0].Resources.Requests[k] = v
	}
	return p
}

func (p *Pod) WithResourceLimits(resources corev1.ResourceList) *Pod {
	if len(p.Spec.Containers) == 0 {
		return p
	}
	if p.Spec.Containers[0].Resources.Limits == nil {
		p.Spec.Containers[0].Resources.Limits = make(corev1.ResourceList)
	}
	for k, v := range resources {
		p.Spec.Containers[0].Resources.Limits[k] = v
	}
	return p
}

func (p *Pod) WithTolerations(tolerations []corev1.Toleration) *Pod {
	p.Spec.Tolerations = append(p.Spec.Tolerations, tolerations...)
	return p
}

type Service struct {
	*corev1.Service
}

func NewService(name, namespace string, selector map[string]string) *Service {
	single := corev1.IPFamilyPolicySingleStack
	return &Service{
		Service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: corev1.ServiceSpec{
				IPFamilyPolicy: &single,
				Selector:       selector,
			},
		},
	}
}

func (s *Service) WithIPFamily(stack string) *Service {
	if stack == "ipv4" {
		s.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol}
	}
	if stack == "ipv6" {
		s.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol}
	}
	return s
}

func (s *Service) ExposePort(port int32, name string) *Service {
	s.Spec.Ports = append(s.Spec.Ports, corev1.ServicePort{Port: port, Name: name})
	return s
}

func (s *Service) ExposeLoadBalancer(public, local bool) *Service {
	if s.Annotations == nil {
		s.Annotations = make(map[string]string)
	}
	s.Annotations["service.beta.kubernetes.io/alibaba-cloud-loadbalancer-spec"] = "slb.s1.small"
	if public {
		s.Annotations["service.beta.kubernetes.io/alibaba-cloud-loadbalancer-address-type"] = "internet"
	} else {
		s.Annotations["service.beta.kubernetes.io/alibaba-cloud-loadbalancer-address-type"] = "intranet"
	}

	s.Spec.Type = corev1.ServiceTypeLoadBalancer
	if local {
		s.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyTypeLocal
	}
	return s
}

func (s *Service) ExposeNodePort() *Service {
	s.Spec.Type = corev1.ServiceTypeNodePort
	return s
}

type NetworkPolicy struct {
	*networkingv1.NetworkPolicy
}

func NewNetworkPolicy(name, namespace string) *NetworkPolicy {
	return &NetworkPolicy{
		NetworkPolicy: &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		},
	}
}

func (s *NetworkPolicy) WithPodSelector(matchPodLabels map[string]string) *NetworkPolicy {
	s.Spec.PodSelector = metav1.LabelSelector{MatchLabels: matchPodLabels}
	return s
}

func (s *NetworkPolicy) WithPolicyType(policyTypes ...networkingv1.PolicyType) *NetworkPolicy {
	for _, policyType := range policyTypes {
		s.Spec.PolicyTypes = append(s.Spec.PolicyTypes, policyType)
	}
	return s
}

func getIP(pod *corev1.Pod) (v4 netip.Addr, v6 netip.Addr) {
	ips := sets.New[string](pod.Status.PodIP)
	for _, ip := range pod.Status.PodIPs {
		ips.Insert(ip.IP)
	}
	for ip := range ips {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}

		if addr.Is4() {
			v4 = addr
		} else {
			v6 = addr
		}
	}
	return
}

type resourceKey struct{}

func ResourcesFromCtx(ctx context.Context) []k8s.Object {
	v, ok := ctx.Value(resourceKey{}).([]k8s.Object)
	if !ok {
		return nil
	}
	return v
}

func SaveResources(ctx context.Context, resources ...k8s.Object) context.Context {
	prev := ResourcesFromCtx(ctx)
	prev = append(prev, resources...)
	return context.WithValue(ctx, resourceKey{}, prev)
}

type testSuccessKey struct{}

func MarkTestSuccess(ctx context.Context) context.Context {
	return context.WithValue(ctx, testSuccessKey{}, true)
}

func IsTestSuccess(ctx context.Context) bool {
	v, ok := ctx.Value(testSuccessKey{}).(bool)
	return ok && v
}

func getNodeIPs(node *corev1.Node, ipv6 bool) (internalIP, externalIP string) {
	for _, addr := range node.Status.Addresses {
		ip := net.ParseIP(addr.Address)
		if ip == nil {
			continue
		}

		isIPv6 := ip.To4() == nil
		if isIPv6 != ipv6 {
			continue
		}

		switch addr.Type {
		case corev1.NodeInternalIP:
			internalIP = addr.Address
		case corev1.NodeExternalIP:
			externalIP = addr.Address
		}
	}
	return internalIP, externalIP
}

func restartTerway(ctx context.Context, config *envconf.Config) error {
	ds := &appsv1.DaemonSet{}
	err := config.Client().Resources().Get(ctx, "terway-eniip", "kube-system", ds)
	if err != nil {
		return err
	}

	annotationPatchStr := fmt.Sprintf(`{ "spec": { "template": { "metadata": { "annotations": { "%v": "%v" } } } } }`, "e2e/restartedAt", time.Now().Format(time.RFC3339))

	err = config.Client().Resources().Patch(ctx, ds, k8s.Patch{PatchType: types.StrategicMergePatchType, Data: []byte(annotationPatchStr)})
	if err != nil {
		return err
	}
	err = wait.For(conditions.New(config.Client().Resources()).DaemonSetReady(ds),
		wait.WithTimeout(5*time.Minute),
		wait.WithInterval(5*time.Second))
	return err
}

func waitPodsReady(client klient.Client, pods ...*corev1.Pod) error {
	for _, pod := range pods {
		err := wait.For(conditions.New(client.Resources()).PodReady(pod),
			wait.WithTimeout(2*time.Minute),
			wait.WithInterval(1*time.Second))
		if err != nil {
			return fmt.Errorf("wait pod %s/%s ready failed: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

type PodNetworking struct {
	*networkv1beta1.PodNetworking
}

func NewPodNetworking(name string) *PodNetworking {
	return &PodNetworking{
		PodNetworking: &networkv1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: networkv1beta1.PodNetworkingSpec{
				ENIOptions: networkv1beta1.ENIOptions{
					ENIAttachType: networkv1beta1.ENIOptionTypeDefault,
				},
			},
		},
	}
}

func (pn *PodNetworking) WithVSwitches(vswitches []string) *PodNetworking {
	pn.Spec.VSwitchOptions = vswitches
	return pn
}

func (pn *PodNetworking) WithSecurityGroups(sgs []string) *PodNetworking {
	pn.Spec.SecurityGroupIDs = sgs
	return pn
}

func (pn *PodNetworking) WithPodSelector(selector *metav1.LabelSelector) *PodNetworking {
	pn.Spec.Selector.PodSelector = selector
	return pn
}

func (pn *PodNetworking) WithNamespaceSelector(selector *metav1.LabelSelector) *PodNetworking {
	pn.Spec.Selector.NamespaceSelector = selector
	return pn
}

func (pn *PodNetworking) WithAllocationType(allocType networkv1beta1.AllocationType) *PodNetworking {
	pn.Spec.AllocationType = allocType
	return pn
}

func (pn *PodNetworking) WithENIAttachType(attachType networkv1beta1.ENIAttachType) *PodNetworking {
	pn.Spec.ENIOptions.ENIAttachType = attachType
	return pn
}

func WaitPodNetworkingDeleted(name string, client klient.Client) error {
	pn := networkv1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	_ = client.Resources().Delete(context.Background(), &pn)

	err := wait.For(conditions.New(client.Resources()).ResourceDeleted(&pn),
		wait.WithImmediate(),
		wait.WithInterval(1*time.Second),
		wait.WithTimeout(30*time.Second),
	)
	return err
}

func WaitPodNetworkingReady(name string, client klient.Client) error {
	pn := networkv1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	err := wait.For(conditions.New(client.Resources()).ResourceMatch(&pn, func(object k8s.Object) bool {
		p := object.(*networkv1beta1.PodNetworking)
		if len(p.Status.VSwitches) != len(p.Spec.VSwitchOptions) {
			return false
		}
		for _, s := range p.Status.VSwitches {
			if s.Zone == "" {
				return false
			}
		}
		return p.Status.Status == networkv1beta1.NetworkingStatusReady
	}),
		wait.WithImmediate(),
		wait.WithInterval(1*time.Second),
		wait.WithTimeout(30*time.Second),
	)
	return err
}

func ValidatePodHasPodNetworking(pod *corev1.Pod, pnName string) error {
	if !terwayTypes.PodUseENI(pod) {
		return fmt.Errorf("pod does not use ENI")
	}
	if pod.Annotations[terwayTypes.PodNetworking] != pnName {
		return fmt.Errorf("expected PodNetworking %s, got %s", pnName, pod.Annotations[terwayTypes.PodNetworking])
	}
	return nil
}

// ValidatePodResourceRequests validates that pod has the correct resource requests based on ENI attach type
func ValidatePodResourceRequests(pod *corev1.Pod, eniAttachType networkv1beta1.ENIAttachType) error {
	var expectedResourceName string
	switch eniAttachType {
	case networkv1beta1.ENIOptionTypeENI:
		expectedResourceName = "aliyun/eni"
	case networkv1beta1.ENIOptionTypeTrunk, networkv1beta1.ENIOptionTypeDefault:
		expectedResourceName = "aliyun/member-eni"
	default:
		return fmt.Errorf("unknown ENI attach type: %s", eniAttachType)
	}

	if pod.Spec.Containers[0].Resources.Requests == nil {
		return fmt.Errorf("pod does not have resource requests")
	}

	if _, exists := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceName(expectedResourceName)]; !exists {
		return fmt.Errorf("expected resource request for %s", expectedResourceName)
	}

	return nil
}

// WaitPodENIReady waits for a PodENI CR to reach Bind phase
func WaitPodENIReady(ctx context.Context, namespace, name string, client klient.Client) (*networkv1beta1.PodENI, error) {
	podENI := &networkv1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}

	err := wait.For(conditions.New(client.Resources()).ResourceMatch(podENI, func(object k8s.Object) bool {
		p := object.(*networkv1beta1.PodENI)
		return p.Status.Phase == networkv1beta1.ENIPhaseBind
	}),
		wait.WithImmediate(),
		wait.WithInterval(1*time.Second),
		wait.WithTimeout(30*time.Second),
	)

	if err != nil {
		return podENI, fmt.Errorf("wait PodENI %s/%s ready failed: %w (current phase: %s, msg: %s)",
			namespace, name, err, podENI.Status.Phase, podENI.Status.Msg)
	}

	return podENI, nil
}

func CreatePodNetworking(ctx context.Context, client klient.Client, pn *networkv1beta1.PodNetworking) error {
	err := WaitPodNetworkingDeleted(pn.Name, client)
	if err != nil {
		return fmt.Errorf("failed to delete existing PodNetworking: %w", err)
	}

	if err := client.Resources().Create(ctx, pn); err != nil {
		return fmt.Errorf("failed to create PodNetworking: %w", err)
	}

	return nil
}

func CreatePodNetworkingAndWaitReady(ctx context.Context, client klient.Client, pn *networkv1beta1.PodNetworking) error {
	err := CreatePodNetworking(ctx, client, pn)
	if err != nil {
		return fmt.Errorf("failed to delete existing PodNetworking: %w", err)
	}

	if err := WaitPodNetworkingReady(pn.Name, client); err != nil {
		return fmt.Errorf("failed waiting for PodNetworking to be ready: %w", err)
	}

	return nil
}

// PodNetworkingTestConfig holds configuration for PodNetworking tests
type PodNetworkingTestConfig struct {
	PodNetworkingName string
	ENIAttachType     networkv1beta1.ENIAttachType
	PodLabels         map[string]string
	VSwitches         []string
	SecurityGroups    []string
	AllocationType    *networkv1beta1.AllocationType
	CreateServices    bool
	TestConnectivity  bool
	ExpectedENIType   *networkv1beta1.ENIType
}

// DefaultPodNetworkingTestConfig returns a default configuration for PodNetworking tests
func DefaultPodNetworkingTestConfig() PodNetworkingTestConfig {
	return PodNetworkingTestConfig{
		PodNetworkingName: "test-pn",
		ENIAttachType:     networkv1beta1.ENIOptionTypeTrunk,
		PodLabels:         map[string]string{"netplan": "test-pn"},
		CreateServices:    true,
		TestConnectivity:  true,
	}
}

// WithENIAttachType sets the ENI attach type
func (c PodNetworkingTestConfig) WithENIAttachType(attachType networkv1beta1.ENIAttachType) PodNetworkingTestConfig {
	c.ENIAttachType = attachType
	return c
}

// WithPodLabels sets the pod labels for selector
func (c PodNetworkingTestConfig) WithPodLabels(labels map[string]string) PodNetworkingTestConfig {
	c.PodLabels = labels
	return c
}

// WithVSwitches sets the custom vSwitches
func (c PodNetworkingTestConfig) WithVSwitches(vswitches []string) PodNetworkingTestConfig {
	c.VSwitches = vswitches
	return c
}

// WithSecurityGroups sets the custom security groups
func (c PodNetworkingTestConfig) WithSecurityGroups(sgs []string) PodNetworkingTestConfig {
	c.SecurityGroups = sgs
	return c
}

// WithAllocationType sets the allocation type
func (c PodNetworkingTestConfig) WithAllocationType(allocType networkv1beta1.AllocationType) PodNetworkingTestConfig {
	c.AllocationType = &allocType
	return c
}

// WithExpectedENIType sets the expected ENI type for validation
func (c PodNetworkingTestConfig) WithExpectedENIType(eniType networkv1beta1.ENIType) PodNetworkingTestConfig {
	c.ExpectedENIType = &eniType
	return c
}

// WithoutServices disables service creation
func (c PodNetworkingTestConfig) WithoutServices() PodNetworkingTestConfig {
	c.CreateServices = false
	return c
}

// WithoutConnectivity disables connectivity testing
func (c PodNetworkingTestConfig) WithoutConnectivity() PodNetworkingTestConfig {
	c.TestConnectivity = false
	return c
}

// PodNetworkingTestSuite represents a complete PodNetworking test suite
type PodNetworkingTestSuite struct {
	Config        PodNetworkingTestConfig
	PodNetworking *networkv1beta1.PodNetworking
	ServerPod     *corev1.Pod
	ClientPod     *corev1.Pod
	Services      []*corev1.Service
}

// NewPodNetworkingTestSuite creates a new test suite with the given configuration
func NewPodNetworkingTestSuite(config PodNetworkingTestConfig) *PodNetworkingTestSuite {
	// Update pod labels to match PodNetworking name if not specified
	if config.PodLabels == nil {
		config.PodLabels = map[string]string{"netplan": config.PodNetworkingName}
	}

	return &PodNetworkingTestSuite{
		Config: config,
	}
}

// Setup creates the PodNetworking and pods
func (ts *PodNetworkingTestSuite) Setup(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	// Create PodNetworking
	pn := NewPodNetworking(ts.Config.PodNetworkingName).
		WithPodSelector(&metav1.LabelSelector{
			MatchLabels: ts.Config.PodLabels,
		}).
		WithENIAttachType(ts.Config.ENIAttachType)

	if len(ts.Config.VSwitches) > 0 {
		pn = pn.WithVSwitches(ts.Config.VSwitches)
	}
	if len(ts.Config.SecurityGroups) > 0 {
		pn = pn.WithSecurityGroups(ts.Config.SecurityGroups)
	}
	if ts.Config.AllocationType != nil {
		pn = pn.WithAllocationType(*ts.Config.AllocationType)
	}

	err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn.PodNetworking)
	if err != nil {
		t.Fatalf("create PodNetworking failed: %v", err)
	}
	ts.PodNetworking = pn.PodNetworking

	// Create server and client pods
	serverLabels := make(map[string]string)
	clientLabels := make(map[string]string)
	for k, v := range ts.Config.PodLabels {
		serverLabels[k] = v
		clientLabels[k] = v
	}
	serverLabels["app"] = "server"
	clientLabels["app"] = "client"

	ts.ServerPod = NewPod("server", config.Namespace()).
		WithLabels(serverLabels).
		WithContainer("server", nginxImage, nil).Pod

	ts.ClientPod = NewPod("client", config.Namespace()).
		WithLabels(clientLabels).
		WithContainer("client", nginxImage, nil).Pod

	err = config.Client().Resources().Create(ctx, ts.ServerPod)
	if err != nil {
		t.Fatalf("create server pod failed: %v", err)
	}
	err = config.Client().Resources().Create(ctx, ts.ClientPod)
	if err != nil {
		t.Fatalf("create client pod failed: %v", err)
	}

	// Create services if enabled
	if ts.Config.CreateServices {
		ts.Services = []*corev1.Service{}
		for _, stack := range getStack() {
			svc := NewService(fmt.Sprintf("server-%s", stack), config.Namespace(),
				map[string]string{"app": "server"}).
				ExposePort(80, "http").
				WithIPFamily(stack)

			err = config.Client().Resources().Create(ctx, svc.Service)
			if err != nil {
				t.Fatalf("create service failed: %v", err)
			}
			ts.Services = append(ts.Services, svc.Service)
		}
	}

	// Save resources for cleanup
	resources := []k8s.Object{ts.PodNetworking, ts.ServerPod, ts.ClientPod}
	for _, svc := range ts.Services {
		resources = append(resources, svc)
	}

	return SaveResources(ctx, resources...)
}

// ValidatePodNetworking validates that pods use the correct PodNetworking
func (ts *PodNetworkingTestSuite) ValidatePodNetworking(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
	client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: config.Namespace()}}

	err := waitPodsReady(config.Client(), server, client)
	if err != nil {
		t.Fatalf("wait pods ready failed: %v", err)
	}

	// Validate PodNetworking annotation
	if err := ValidatePodHasPodNetworking(server, ts.Config.PodNetworkingName); err != nil {
		t.Fatalf("server pod PodNetworking validation failed: %v", err)
	}
	if err := ValidatePodHasPodNetworking(client, ts.Config.PodNetworkingName); err != nil {
		t.Fatalf("client pod PodNetworking validation failed: %v", err)
	}

	// Validate resource requests
	if err := ValidatePodResourceRequests(server, ts.Config.ENIAttachType); err != nil {
		t.Fatalf("server pod resource requests validation failed: %v", err)
	}
	if err := ValidatePodResourceRequests(client, ts.Config.ENIAttachType); err != nil {
		t.Fatalf("client pod resource requests validation failed: %v", err)
	}

	t.Logf("✓ Pods are using PodNetworking %s with correct resource requests", ts.Config.PodNetworkingName)
	return ctx
}

// ValidatePodENI validates PodENI configuration
func (ts *PodNetworkingTestSuite) ValidatePodENI(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
	client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: config.Namespace()}}

	// Validate server PodENI
	serverPodENI, err := WaitPodENIReady(ctx, server.Namespace, server.Name, config.Client())
	if err != nil {
		t.Fatalf("server PodENI not ready: %v", err)
	}
	if len(serverPodENI.Spec.Allocations) == 0 {
		t.Fatalf("server PodENI has no allocations")
	}
	ts.validatePodENIType(t, serverPodENI, "server")

	// Validate client PodENI
	clientPodENI, err := WaitPodENIReady(ctx, client.Namespace, client.Name, config.Client())
	if err != nil {
		t.Fatalf("client PodENI not ready: %v", err)
	}
	if len(clientPodENI.Spec.Allocations) == 0 {
		t.Fatalf("client PodENI has no allocations")
	}
	ts.validatePodENIType(t, clientPodENI, "client")

	return ctx
}

func (ts *PodNetworkingTestSuite) validatePodENIType(t *testing.T, podENI *networkv1beta1.PodENI, podType string) {
	eniID := podENI.Spec.Allocations[0].ENI.ID
	if ts.Config.ExpectedENIType != nil {
		if eniInfo, ok := podENI.Status.ENIInfos[eniID]; ok {
			if eniInfo.Type != *ts.Config.ExpectedENIType {
				t.Errorf("expected ENI type %s, got %s", *ts.Config.ExpectedENIType, eniInfo.Type)
			} else {
				t.Logf("✓ %s pod using expected ENI type: %s (allocations: %d)", podType, eniInfo.Type, len(podENI.Spec.Allocations))
			}
		} else {
			t.Logf("✓ %s pod created (allocations: %d, ENI ID: %s)", podType, len(podENI.Spec.Allocations), eniID)
		}
	} else {
		if eniInfo, ok := podENI.Status.ENIInfos[eniID]; ok {
			t.Logf("✓ %s pod using ENI type: %s (allocations: %d)", podType, eniInfo.Type, len(podENI.Spec.Allocations))
		} else {
			t.Logf("✓ %s pod created (allocations: %d, ENI ID: %s)", podType, len(podENI.Spec.Allocations), eniID)
		}
	}
}

// TestConnectivity tests connectivity between pods
func (ts *PodNetworkingTestSuite) TestConnectivity(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	if !ts.Config.TestConnectivity {
		return ctx
	}

	for _, stack := range getStack() {
		serviceName := fmt.Sprintf("server-%s", stack)
		_, err := Pull(config.Client(), config.Namespace(), "client", "client", serviceName, t)
		if err != nil {
			t.Errorf("connectivity failed for %s: %v", stack, err)
		}
	}
	return MarkTestSuccess(ctx)
}

// RunTestSuite runs a complete PodNetworking test suite
func RunPodNetworkingTestSuite(t *testing.T, config PodNetworkingTestConfig) {
	suite := NewPodNetworkingTestSuite(config)

	feature := features.New(fmt.Sprintf("PodNetworking/%s", config.PodNetworkingName)).
		WithLabel("env", "pod-networking").
		Setup(suite.Setup).
		Assess("pods should use PodNetworking", suite.ValidatePodNetworking).
		Assess("pods should have correct PodENI configuration", suite.ValidatePodENI).
		Assess("connectivity should work", suite.TestConnectivity).
		Feature()

	testenv.Test(t, feature)
}

// Version check functions for test requirements

var (
	terwayDSNameOnce sync.Once
	terwayDSName     string
)

// GetTerwayDaemonSetName returns the terway daemonset name in the cluster.
// It caches the result since the component name is cluster-wide and won't change.
// Returns one of: "terway-eniip", "terway-eni", "terway"
func GetTerwayDaemonSetName(ctx context.Context, config *envconf.Config) (string, error) {
	var err error
	terwayDSNameOnce.Do(func() {
		ds := &appsv1.DaemonSetList{}
		listErr := config.Client().Resources().WithNamespace("kube-system").List(ctx, ds)
		if listErr != nil {
			err = fmt.Errorf("failed to list daemonsets: %w", listErr)
			return
		}
		for _, d := range ds.Items {
			switch d.Name {
			case "terway-eniip", "terway-eni", "terway":
				terwayDSName = d.Name
				return
			}
		}
		err = fmt.Errorf("terway daemonset not found")
	})
	if err != nil {
		return "", err
	}
	return terwayDSName, nil
}

// GetTerwayVersion retrieves the terway version by reading the first init container's image tag from the daemonset.
// Returns the version in semantic version format (e.g., "v1.16.1").
// This function does not use cache and retrieves the version in real-time.
func GetTerwayVersion(ctx context.Context, config *envconf.Config) (string, error) {
	dsName, err := GetTerwayDaemonSetName(ctx, config)
	if err != nil {
		return "", err
	}

	ds := &appsv1.DaemonSet{}
	err = config.Client().Resources().Get(ctx, dsName, "kube-system", ds)
	if err != nil {
		return "", fmt.Errorf("failed to get daemonset %s: %w", dsName, err)
	}

	// Get version from the first init container's image tag
	if len(ds.Spec.Template.Spec.InitContainers) == 0 {
		return "", fmt.Errorf("no init containers found in daemonset %s", dsName)
	}

	image := ds.Spec.Template.Spec.InitContainers[0].Image
	parts := strings.Split(image, ":")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid image format: %s", image)
	}

	version := parts[len(parts)-1]

	// Validate semantic version format
	if !semver.IsValid(version) {
		return "", fmt.Errorf("invalid semantic version: %s", version)
	}

	return version, nil
}

// GetK8sVersion retrieves the Kubernetes cluster version from a node.
// Returns the version in semantic version format (e.g., "v1.24.0").
// This function does not use cache and retrieves the version in real-time.
func GetK8sVersion(ctx context.Context, config *envconf.Config) (string, error) {
	nodes := &corev1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return "", fmt.Errorf("failed to list nodes: %w", err)
	}

	if len(nodes.Items) == 0 {
		return "", fmt.Errorf("no nodes found in cluster")
	}

	// Get version from the first node
	version := nodes.Items[0].Status.NodeInfo.KubeletVersion

	// Ensure version starts with 'v' for semantic versioning
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	// Validate semantic version format
	if !semver.IsValid(version) {
		return "", fmt.Errorf("invalid semantic version: %s", version)
	}

	return version, nil
}

// CheckTerwayDaemonSetName checks if the terway daemonset name matches the expected name.
// dsName should be one of: "terway-eniip", "terway-eni", "terway"
func CheckTerwayDaemonSetName(ctx context.Context, config *envconf.Config, expectedName string) (bool, error) {
	actualName, err := GetTerwayDaemonSetName(ctx, config)
	if err != nil {
		return false, err
	}
	return actualName == expectedName, nil
}

// CheckTerwayVersion checks if the current terway version is greater than or equal to the required version.
// requiredVersion can be with or without 'v' prefix, e.g., "v1.16.1" or "1.16.1"
func CheckTerwayVersion(ctx context.Context, config *envconf.Config, requiredVersion string) (bool, error) {
	currentVersion, err := GetTerwayVersion(ctx, config)
	if err != nil {
		return false, err
	}

	if !semver.IsValid(requiredVersion) {
		return false, fmt.Errorf("invalid version format: %s", requiredVersion)
	}

	// semver.Compare returns: -1 if v < w, 0 if v == w, +1 if v > w
	// We check if current >= required (i.e., compare >= 0)
	return semver.Compare(currentVersion, requiredVersion) >= 0, nil
}

// ENIConfigBackup stores the backup of eni-config for restoration
type ENIConfigBackup struct {
	TerwayConf     string
	TerwayConfList string
	HasConfList    bool
}

// BackupENIConfig backs up the current eni-config ConfigMap
func BackupENIConfig(ctx context.Context, config *envconf.Config) (*ENIConfigBackup, error) {
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return nil, fmt.Errorf("failed to get eni-config: %w", err)
	}

	backup := &ENIConfigBackup{
		TerwayConf: cm.Data["10-terway.conf"],
	}

	if confList, ok := cm.Data["10-terway.conflist"]; ok {
		backup.TerwayConfList = confList
		backup.HasConfList = true
	}

	return backup, nil
}

// RestoreENIConfig restores the eni-config ConfigMap from backup
func RestoreENIConfig(ctx context.Context, config *envconf.Config, backup *ENIConfigBackup) error {
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return fmt.Errorf("failed to get eni-config: %w", err)
	}

	cm.Data["10-terway.conf"] = backup.TerwayConf

	if backup.HasConfList {
		cm.Data["10-terway.conflist"] = backup.TerwayConfList
	} else {
		delete(cm.Data, "10-terway.conflist")
	}

	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		return fmt.Errorf("failed to update eni-config: %w", err)
	}

	return nil
}

// ConfigureHostPortCNIChain configures the CNI chain with portmap plugin for HostPort support
// isDatapathV2 indicates whether the cluster is using Datapath V2 (kube-proxy replacement)
func ConfigureHostPortCNIChain(ctx context.Context, config *envconf.Config, isDatapathV2 bool) error {
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return fmt.Errorf("failed to get eni-config: %w", err)
	}

	// Parse existing 10-terway.conf to build the conflist
	existingConf := cm.Data["10-terway.conf"]
	terwayJSON, err := gabs.ParseJSON([]byte(existingConf))
	if err != nil {
		return fmt.Errorf("failed to parse 10-terway.conf: %w", err)
	}

	// Enable symmetric_routing in terway config
	_, err = terwayJSON.Set(true, "symmetric_routing")
	if err != nil {
		return fmt.Errorf("failed to set symmetric_routing: %w", err)
	}

	// Build portmap plugin config
	portmapConfig := map[string]interface{}{
		"type":         "portmap",
		"capabilities": map[string]bool{"portMappings": true},
	}

	// For Datapath V2, add masqAll option
	if isDatapathV2 {
		portmapConfig["externalSetMarkChain"] = "KUBE-MARK-MASQ"
		portmapConfig["masqAll"] = true
	} else {
		portmapConfig["externalSetMarkChain"] = "KUBE-MARK-MASQ"
	}

	// Build the conflist structure
	confList := map[string]interface{}{
		"cniVersion": "0.4.0",
		"name":       "terway",
		"plugins": []interface{}{
			terwayJSON.Data(),
			portmapConfig,
		},
	}

	confListBytes, err := json.MarshalIndent(confList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conflist: %w", err)
	}

	// Update ConfigMap
	cm.Data["10-terway.conflist"] = string(confListBytes)

	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		return fmt.Errorf("failed to update eni-config: %w", err)
	}

	return nil
}

// IsDatapathV2Enabled checks if the cluster is using Datapath V2 by checking the CNI config
func IsDatapathV2Enabled(ctx context.Context, config *envconf.Config) (bool, error) {
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return false, fmt.Errorf("failed to get eni-config: %w", err)
	}

	// Check in 10-terway.conf
	if strings.Contains(cm.Data["10-terway.conf"], "datapathv2") {
		return true, nil
	}

	// Check in 10-terway.conflist if exists
	if confList, ok := cm.Data["10-terway.conflist"]; ok {
		if strings.Contains(confList, "datapathv2") {
			return true, nil
		}
	}

	return false, nil
}
