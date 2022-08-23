//go:build e2e
// +build e2e

package tests

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"time"

	"github.com/sirupsen/logrus"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sLabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/generated/clientset/versioned"
)

var backoff = wait.Backoff{
	Duration: 2 * time.Second,
	Factor:   1,
	Jitter:   0,
	Steps:    150,
}

func EnsureNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	_, err := cs.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					"project": "network-test",
				},
			}}, metav1.CreateOptions{})
			return err
		}
	}
	return err
}

func EnsurePodNetworking(ctx context.Context, cs *versioned.Clientset, cfg PodNetworkingConfig) (*v1beta1.PodNetworking, error) {
	pnTpl := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: cfg.Name,
		},
		Spec: v1beta1.PodNetworkingSpec{
			AllocationType: cfg.IPType,
			Selector: v1beta1.Selector{
				PodSelector: &metav1.LabelSelector{
					MatchLabels: cfg.PodSelectLabels,
				},
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: cfg.NamespaceLabels,
				},
			},
		},
	}
	_, err := cs.NetworkV1beta1().PodNetworkings().Create(ctx, pnTpl, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}
	var pn *v1beta1.PodNetworking
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		pn, err = cs.NetworkV1beta1().PodNetworkings().Get(ctx, cfg.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if pn.Status.Status != v1beta1.NetworkingStatusReady {
			return false, nil
		}
		return true, nil
	})
	return pn, err
}

func EnsureNetworkPolicy(ctx context.Context, cs kubernetes.Interface, cfg NetworkPolicyConfig) (*v1.NetworkPolicy, error) {
	networkPolicyTpl := &v1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
		},
		Spec: v1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: cfg.PodSelectLabels,
			},
			Ingress: []v1.NetworkPolicyIngressRule{
				{
					From: []v1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: cfg.IngressPodLabels,
							},
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: cfg.IngressNamespaceLabels,
							},
						},
					},
				},
			},
		},
	}
	_, err := cs.NetworkingV1().NetworkPolicies(cfg.Namespace).Create(ctx, networkPolicyTpl, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}
	var np *v1.NetworkPolicy
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		np, err = cs.NetworkingV1().NetworkPolicies(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return true, nil
	})
	return np, err
}

func EnsureDeployment(ctx context.Context, cs kubernetes.Interface, cfg PodResConfig) ([]corev1.Pod, error) {
	deplTml := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: cfg.Labels,
			},
			Replicas: func(a int32) *int32 { return &a }(cfg.Replicas),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: cfg.Labels,
				},
				Spec: corev1.PodSpec{
					HostNetwork:                   cfg.HostNetwork,
					TerminationGracePeriodSeconds: func(a int64) *int64 { return &a }(0),
					Containers: []corev1.Container{
						{
							Name:  "echo",
							Image: image,
							Args: []string{"--http-bind-address", fmt.Sprintf(":%d", httpTestPort),
								"--https-bind-address", fmt.Sprintf(":%d", httpsTestPort)},
							ImagePullPolicy: corev1.PullAlways,
						},
					},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "type",
												Operator: corev1.NodeSelectorOpNotIn,
												Values: []string{
													"virtual-kubelet",
												},
											},
										},
									},
								},
							},
						},
					},
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
						{
							MaxSkew:           1,
							TopologyKey:       corev1.LabelHostname,
							WhenUnsatisfiable: corev1.ScheduleAnyway,
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: cfg.Labels,
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Key:   "kubernetes.io/arch",
							Value: "arm64",
						},
					},
				},
			},
		},
	}
	_, err := cs.AppsV1().Deployments(cfg.Namespace).Create(ctx, deplTml, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}
	uid := ""
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		delp, err := cs.AppsV1().Deployments(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		uid = string(delp.UID)
		return delp.Status.Replicas == *delp.Spec.Replicas && delp.Status.Replicas == delp.Status.AvailableReplicas, nil
	})
	if err != nil {
		return nil, err
	}
	pods, err := GetPodsByRef(ctx, cs, uid)
	if err != nil {
		return nil, err
	}
	return pods, nil
}

func EnsureStatefulSet(ctx context.Context, cs kubernetes.Interface, cfg PodResConfig) ([]corev1.Pod, error) {
	stsTml := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: cfg.Labels,
			},
			Replicas: func(a int32) *int32 { return &a }(cfg.Replicas),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: cfg.Labels,
				},
				Spec: corev1.PodSpec{
					HostNetwork:                   cfg.HostNetwork,
					TerminationGracePeriodSeconds: func(a int64) *int64 { return &a }(0),
					Containers: []corev1.Container{
						{
							Name:  "echo",
							Image: image,
							Args: []string{"--http-bind-address", fmt.Sprintf(":%d", httpTestPort),
								"--https-bind-address", fmt.Sprintf(":%d", httpsTestPort)},
							ImagePullPolicy: corev1.PullAlways,
						},
					},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "type",
												Operator: corev1.NodeSelectorOpNotIn,
												Values: []string{
													"virtual-kubelet",
												},
											},
										},
									},
								},
							},
						},
					},
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
						{
							MaxSkew:           1,
							TopologyKey:       corev1.LabelHostname,
							WhenUnsatisfiable: corev1.ScheduleAnyway,
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: cfg.Labels,
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Key:   "kubernetes.io/arch",
							Value: "arm64",
						},
					},
				},
			},
		},
	}
	_, err := cs.AppsV1().StatefulSets(cfg.Namespace).Create(ctx, stsTml, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}
	uid := ""
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		sts, err := cs.AppsV1().StatefulSets(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		uid = string(sts.UID)
		return sts.Status.Replicas == *sts.Spec.Replicas && sts.Status.Replicas == sts.Status.ReadyReplicas, nil
	})
	if err != nil {
		return nil, err
	}
	pods, err := GetPodsByRef(ctx, cs, uid)
	if err != nil {
		return nil, err
	}
	return pods, nil
}

func EnsureService(ctx context.Context, cs kubernetes.Interface, cfg ServiceResConfig) (*corev1.Service, error) {
	svc, err := cs.CoreV1().Services(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err == nil {
		return svc, nil
	}
	svcTpl := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    cfg.Labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "http-server",
					Port:       int32(httpTestPort),
					TargetPort: intstr.FromInt(httpTestPort),
				},
			},
			Selector:              cfg.PodSelectLabels,
			SessionAffinity:       corev1.ServiceAffinityNone,
			ExternalTrafficPolicy: "",
			IPFamilies:            nil,
			Type:                  cfg.Type,
		},
	}

	if cfg.Headless {
		svcTpl.Spec.ClusterIP = "None"
	}
	svc, err = cs.CoreV1().Services(cfg.Namespace).Create(ctx, svcTpl, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		svc, err = cs.CoreV1().Services(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if cfg.Type == corev1.ServiceTypeLoadBalancer {
			if len(svc.Status.LoadBalancer.Ingress) > 0 {
				return true, nil
			}
			return false, nil
		}
		return true, nil
	})

	return svc, err
}

func DeletePodNetworking(ctx context.Context, cs *versioned.Clientset, name string) error {
	err := cs.NetworkV1beta1().PodNetworkings().Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := cs.NetworkV1beta1().PodNetworkings().Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	return err
}

func DeleteNetworkPolicy(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	err := cs.NetworkingV1().NetworkPolicies(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := cs.NetworkingV1().NetworkPolicies(namespace).Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	return err
}

func DeleteDeployment(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	err := cs.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	return err
}

func DeleteStatefulSet(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	err := cs.AppsV1().StatefulSets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	return err
}

func DeleteService(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	err := cs.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := cs.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	return err
}

func DeleteNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	err := cs.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := cs.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	return err
}

func GetPodsByRef(ctx context.Context, cs kubernetes.Interface, uid string) ([]corev1.Pod, error) {
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []corev1.Pod
	for _, pod := range pods.Items {
		if MatchOwnerReference(pod, uid) {
			result = append(result, pod)
		}
	}
	return result, nil
}

func MatchOwnerReference(pod corev1.Pod, uid string) bool {
	for _, ref := range pod.OwnerReferences {
		if string(ref.UID) == uid {
			return true
		}
	}
	return false
}

func ListNodeIPs(ctx context.Context, cs kubernetes.Interface) ([]net.IP, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []net.IP
	for _, node := range nodes.Items {
		if _, ok := node.Labels["type"]; ok {
			if node.Labels["type"] == "virtual-kubelet" {
				logrus.Infof("skip virtual node %s", node.Name)
				continue
			}
		}
		for _, v := range node.Status.Conditions {
			if v.Type == corev1.NodeReady {
				if v.Status == corev1.ConditionFalse {
					logrus.Infof("skip notready node %s", node.Name)
					continue
				}
			}
		}
		for _, addr := range node.Status.Addresses {
			if addr.Type != corev1.NodeInternalIP {
				continue
			}
			result = append(result, net.ParseIP(addr.Address))
		}
	}
	return result, nil
}

func Exec(cs kubernetes.Interface, restConf *rest.Config, podNamespace, podName string, cmd []string) ([]byte, []byte, error) {
	req := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(podNamespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(restConf, "POST", req.URL())
	if err != nil {
		return nil, nil, err
	}

	buf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	if err = exec.Stream(remotecommand.StreamOptions{
		Stdout: buf,
		Stderr: errBuf,
		Tty:    false,
	}); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), errBuf.Bytes(), nil
}

func ListPods(ctx context.Context, cs kubernetes.Interface, namespace string, labels map[string]string) ([]corev1.Pod, error) {
	labelSelector := metav1.LabelSelector{MatchLabels: labels}

	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: k8sLabels.Set(labelSelector.MatchLabels).String()})
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

func ListServices(ctx context.Context, cs kubernetes.Interface, namespace string, labels map[string]string) ([]corev1.Service, error) {
	labelSelector := metav1.LabelSelector{MatchLabels: labels}

	svcs, err := cs.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{LabelSelector: k8sLabels.Set(labelSelector.MatchLabels).String()})
	if err != nil {
		return nil, err
	}
	return svcs.Items, nil
}
