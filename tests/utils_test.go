//go:build e2e

package tests

import (
	"context"
	"net/netip"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	p.Spec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: nodeSelectorTerms,
						MatchFields:      nil,
					},
				},
			},
		},
	}

	return p
}
func (p *Pod) WithPodAffinity(labels map[string]string) *Pod {
	p.Spec.Affinity = &corev1.Affinity{
		PodAffinity: &corev1.PodAffinity{
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

	return p
}

func (p *Pod) WithDNSPolicy(policy corev1.DNSPolicy) *Pod {
	p.Spec.DNSPolicy = policy
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

func ResourcesFromCtx(ctx context.Context) []client.Object {
	v, ok := ctx.Value(resourceKey{}).([]client.Object)
	if !ok {
		return nil
	}
	return v
}

func SaveResources(ctx context.Context, resources ...client.Object) context.Context {
	prev := ResourcesFromCtx(ctx)
	prev = append(prev, resources...)
	return context.WithValue(ctx, resourceKey{}, prev)
}
