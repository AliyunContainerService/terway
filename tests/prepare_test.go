package tests

import (
	"bytes"
	"context"
	"net"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sLabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var backoff = wait.Backoff{
	Duration: 2 * time.Second,
	Factor:   1,
	Jitter:   0,
	Steps:    60,
}

func EnsureNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	_, err := cs.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name: name,
			}}, metav1.CreateOptions{})
			return err
		}
	}
	return err
}

func EnsureContainerPods(ctx context.Context, cs kubernetes.Interface, cfg TestResConfig) ([]corev1.Pod, error) {
	dsTpl := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: cfg.Labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: cfg.Labels,
				},
				Spec: corev1.PodSpec{
					HostNetwork:                   cfg.HostNetwork,
					TerminationGracePeriodSeconds: func(a int64) *int64 { return &a }(0),
					Containers: []corev1.Container{
						{
							Name:            "echo",
							Image:           "l1b0k/echo",
							ImagePullPolicy: corev1.PullAlways,
						},
					},
				},
			},
		},
	}
	_, err := cs.AppsV1().DaemonSets(cfg.Namespace).Create(ctx, dsTpl, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}
	uid := ""
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		ds, err := cs.AppsV1().DaemonSets(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		uid = string(ds.UID)
		if ds.Status.DesiredNumberScheduled == 0 {
			return false, nil
		}
		return ds.Status.DesiredNumberScheduled == ds.Status.NumberReady && ds.Status.NumberAvailable == ds.Status.CurrentNumberScheduled, nil
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

func EnsureService(ctx context.Context, cs kubernetes.Interface, cfg TestResConfig) (*corev1.Service, error) {
	svc, err := cs.CoreV1().Services(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err == nil {
		return svc, nil
	}
	svcTpl := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "80",
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
			},
			Selector:              cfg.Labels,
			SessionAffinity:       corev1.ServiceAffinityNone,
			ExternalTrafficPolicy: "",
			IPFamily:              nil,
			Type:                  corev1.ServiceTypeLoadBalancer,
		},
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
		if len(svc.Status.LoadBalancer.Ingress) > 0 {
			return true, nil
		}

		return false, nil
	})

	return svc, err
}

func DeleteDs(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	err := cs.AppsV1().DaemonSets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	return err
}

func DeleteSvc(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
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

func DeleteNs(ctx context.Context, cs kubernetes.Interface, name string) error {
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
		for _, addr := range node.Status.Addresses {
			if addr.Type != corev1.NodeInternalIP {
				continue
			}
			result = append(result, net.ParseIP(addr.Address))
		}
	}
	return result, nil
}

func Exec(ctx context.Context, cs kubernetes.Interface, restConf *rest.Config, podNamespace, podName string, cmd []string) ([]byte, []byte, error) {
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
