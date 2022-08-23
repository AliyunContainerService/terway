//go:build e2e
// +build e2e

package tests

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/AliyunContainerService/terway/pkg/generated/clientset/versioned"
	"github.com/AliyunContainerService/terway/pkg/utils"
)

func TestConnectionTestSuite(t *testing.T) {
	suite.Run(t, new(ConnectionTestSuite))
}

type ConnectionTestSuite struct {
	suite.Suite

	RestConf               *rest.Config
	ClientSet              kubernetes.Interface
	PodNetworkingClientSet *versioned.Clientset
	err                    error

	TestCase []TestCase
}

func (s *ConnectionTestSuite) SetupSuite() {
	s.RestConf = ctrl.GetConfigOrDie()
	utils.RegisterClients(s.RestConf)
	s.ClientSet = utils.K8sClient
	s.PodNetworkingClientSet, _ = versioned.NewForConfig(s.RestConf)

	ctx := context.Background()
	s.T().Logf("test image: %v", image)
	s.T().Logf("test namespace: %s", testNamespace)
	s.T().Logf("enable trunk: %v", enableTrunk)
	s.T().Logf("enable policy: %v", enablePolicy)
	s.T().Logf("creating namespace %s", testNamespace)
	if err := EnsureNamespace(ctx, s.ClientSet, testNamespace); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create namespace %s", testNamespace))
	}

	if enableTrunk {
		s.T().Logf("creating %s pod networking", elasticPodNetWorking.Name)
		if _, err := EnsurePodNetworking(ctx, s.PodNetworkingClientSet, elasticPodNetWorking); err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to create pod networking %s", elasticPodNetWorking.Name))
		}

		s.T().Logf("creating %s pod networking", fixedPodNetWorking.Name)
		if _, err := EnsurePodNetworking(ctx, s.PodNetworkingClientSet, fixedPodNetWorking); err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to create pod networking %s", fixedPodNetWorking.Name))
		}
	}

	s.T().Logf("creating %s", podConnA.Name)
	if _, err := EnsureDeployment(ctx, s.ClientSet, podConnA); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create pod %s", podConnA.Name))
	}

	s.T().Logf("creating %s", podConnB.Name)
	if _, err := EnsureDeployment(ctx, s.ClientSet, podConnB); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create pod %s", podConnB.Name))
	}

	s.T().Logf("creating %s", podConnC.Name)
	if _, err := EnsureDeployment(ctx, s.ClientSet, podConnC); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create pod %s", podConnC.Name))
	}

	s.T().Logf("creating %s", podConnD.Name)
	if _, err := EnsureStatefulSet(ctx, s.ClientSet, podConnD); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create pod %s", podConnD.Name))
	}

	s.T().Logf("creating %s", podConnPolicy.Name)
	if _, err := EnsureDeployment(ctx, s.ClientSet, podConnPolicy); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create network policy %s", podConnPolicy.Name))
	}

	s.T().Logf("creating %s for %s", clusterIPService.Name, podConnC.Name)
	if _, err := EnsureService(ctx, s.ClientSet, clusterIPService); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create service %s", clusterIPService.Name))
	}

	s.T().Logf("creating %s for %s", nodePortService.Name, podConnC.Name)
	if _, err := EnsureService(ctx, s.ClientSet, nodePortService); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create service %s", nodePortService.Name))
	}

	s.T().Logf("creating %s for %s", loadBalancerService.Name, podConnC.Name)
	if _, err := EnsureService(ctx, s.ClientSet, loadBalancerService); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create service %s", loadBalancerService.Name))
	}

	s.T().Logf("creating %s for %s", headlessService.Name, podConnC.Name)
	if _, err := EnsureService(ctx, s.ClientSet, headlessService); err != nil {
		s.err = err
		s.T().Error(errors.Wrapf(err, "fail to create service %s", headlessService.Name))
	}

	if enablePolicy {
		s.T().Logf("creating network policy %s", networkPolicy.Name)
		if _, err := EnsureNetworkPolicy(ctx, s.ClientSet, networkPolicy); err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to create network policy %s", networkPolicy.Name))
		}
	}

	s.TestCase = []TestCase{
		{
			Type: TestTypePodToPod,
			Skip: false,
			Src: Resource{
				Label: map[string]string{
					"app": "container-network-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"app": "container-network-pod-dst",
					"e2e": "true",
				},
			},
			Status: true,
		},
		{
			Type: TestTypePodToPod,
			Skip: enablePolicy,
			Src: Resource{
				Label: map[string]string{
					"app": "host-network-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"app": "container-network-pod-dst",
					"e2e": "true",
				},
			},
			Status: true,
		},
		{
			Type: TestTypePodToPod,
			Skip: !enablePolicy,
			Src: Resource{
				Label: map[string]string{
					"app": "container-network-policy-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"app": "container-network-pod-dst",
					"e2e": "true",
				},
			},
			Status: false,
		},
		{
			Type: TestTypePodToServiceIP,
			Skip: false,
			Src: Resource{
				Label: map[string]string{
					"app": "container-network-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"svc": "container-network-svc-dst",
					"e2e": "true",
				},
			},
			Status: true,
		},
		{
			Type: TestTypePodToServiceIP,
			Skip: enablePolicy,
			Src: Resource{
				Label: map[string]string{
					"app": "host-network-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"svc": "container-network-svc-dst",
					"e2e": "true",
				},
			},
			Status: true,
		},
		{
			Type: TestTypePodToServiceIP,
			Skip: !enablePolicy,
			Src: Resource{
				Label: map[string]string{
					"app": "container-network-policy-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"svc": "container-network-svc-dst",
					"e2e": "true",
				},
			},
			Status: false,
		},
		{
			Type: TestTypePodToServiceName,
			Skip: false,
			Src: Resource{
				Label: map[string]string{
					"app": "container-network-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"svc": "container-network-svc-dst",
					"e2e": "true",
				},
			},
			Status: true,
		},
		{
			Type: TestTypePodToServiceName,
			Skip: enablePolicy,
			Src: Resource{
				Label: map[string]string{
					"app": "host-network-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"svc": "container-network-svc-dst",
					"e2e": "true",
				},
			},
			Status: false,
		},
		{
			Type: TestTypePodToServiceName,
			Skip: !enablePolicy,
			Src: Resource{
				Label: map[string]string{
					"app": "container-network-policy-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"svc": "container-network-svc-dst",
					"e2e": "true",
				},
			},
			Status: false,
		},
	}
}

func (s *ConnectionTestSuite) TearDownSuite() {

	if s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "skip tear down resource in namespace %s, because of an error occurred.", testNamespace))
	}

	s.PrintEvents(testNamespace)
	s.PrintPods(testNamespace)

	ctx := context.Background()
	if enablePolicy {
		s.T().Logf("delete %s", networkPolicy.Name)
		if err := DeleteNetworkPolicy(ctx, s.ClientSet, testNamespace, networkPolicy.Name); err != nil {
			s.T().Logf("fail to delete network policy %s: %v", networkPolicy.Name, err)
		}
	}

	s.T().Logf("delete %s", clusterIPService.Name)
	if err := DeleteService(ctx, s.ClientSet, testNamespace, clusterIPService.Name); err != nil {
		s.T().Logf("fail to delete service %s: %v", clusterIPService.Name, err)
	}

	s.T().Logf("delete %s", nodePortService.Name)
	if err := DeleteService(ctx, s.ClientSet, testNamespace, nodePortService.Name); err != nil {
		s.T().Logf("fail to delete service %s: %v", nodePortService.Name, err)
	}

	s.T().Logf("delete %s", loadBalancerService.Name)
	if err := DeleteService(ctx, s.ClientSet, testNamespace, loadBalancerService.Name); err != nil {
		s.T().Logf("fail to delete service %s: %v", loadBalancerService.Name, err)
	}

	s.T().Logf("delete %s", headlessService.Name)
	if err := DeleteService(ctx, s.ClientSet, testNamespace, headlessService.Name); err != nil {
		s.T().Logf("fail to delete service %s: %v", headlessService.Name, err)
	}

	s.T().Logf("delete %s", podConnA.Name)
	if err := DeleteDeployment(ctx, s.ClientSet, testNamespace, podConnA.Name); err != nil {
		s.T().Logf("fail to delete pod %s: %v", podConnA.Name, err)
	}

	s.T().Logf("delete %s", podConnB.Name)
	if err := DeleteDeployment(ctx, s.ClientSet, testNamespace, podConnB.Name); err != nil {
		s.T().Logf("fail to delete pod %s: %v", podConnB.Name, err)
	}

	s.T().Logf("delete %s", podConnC.Name)
	if err := DeleteDeployment(ctx, s.ClientSet, testNamespace, podConnC.Name); err != nil {
		s.T().Logf("fail to delete pod %s: %v", podConnC.Name, err)
	}

	s.T().Logf("delete %s", podConnD.Name)
	if err := DeleteStatefulSet(ctx, s.ClientSet, testNamespace, podConnD.Name); err != nil {
		s.T().Logf("fail to delete pod %s: %v", podConnD.Name, err)
	}

	s.T().Logf("delete %s", podConnPolicy.Name)
	if err := DeleteDeployment(ctx, s.ClientSet, testNamespace, podConnPolicy.Name); err != nil {
		s.T().Logf("fail to delete pod %s: %v", podConnPolicy.Name, err)
	}

	if enableTrunk {
		s.T().Logf("delete %s pod networking", elasticPodNetWorking.Name)
		if err := DeletePodNetworking(ctx, s.PodNetworkingClientSet, elasticPodNetWorking.Name); err != nil {
			s.T().Logf("fail to delete pod networking %s: %v", elasticPodNetWorking.Name, err)
		}

		s.T().Logf("delete %s pod networking", fixedPodNetWorking.Name)
		if err := DeletePodNetworking(ctx, s.PodNetworkingClientSet, fixedPodNetWorking.Name); err != nil {
			s.T().Logf("fail to delete pod networking %s: %v", fixedPodNetWorking.Name, err)
		}
	}

	s.T().Logf("delete ns")
	if err := DeleteNamespace(ctx, s.ClientSet, testNamespace); err != nil {
		s.T().Logf("fail to delete namespace %s: %v", testNamespace, err)
	}
}

func (s *ConnectionTestSuite) TestPod2Pod() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToPod || c.Skip {
			continue
		}
		ctx := context.Background()
		var srcPods []corev1.Pod
		var err error
		srcPods, err = ListPods(ctx, s.ClientSet, testNamespace, c.Src.Label)
		if err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to list src pod %v", c.Src.Label))
		}

		var dstPods []corev1.Pod
		dstPods, err = ListPods(ctx, s.ClientSet, testNamespace, c.Dst.Label)
		if err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to list dst pod %v", c.Dst.Label))
		}
		for _, src := range srcPods {
			for _, dst := range dstPods {
				s.T().Logf("src %s -> dst %s", podInfo(&src), podInfo(&dst))
				for _, ip := range podIPs(&dst) {
					addr := net.JoinHostPort(ip, fmt.Sprintf("%d", httpTestPort))
					l := fmt.Sprintf("src %s -> dst %s", podInfo(&src), addr)
					stdout, stdErrOut, err := s.ExecHTTPGet(src.Namespace, src.Name, curlAddr(addr))
					s.Expected(c.Status, stdout, stdErrOut, err, l)
				}
			}
		}
	}
}

func (s *ConnectionTestSuite) TestPod2ServiceIP() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToServiceIP || c.Skip {
			continue
		}
		ctx := context.Background()
		var srcPods []corev1.Pod
		var err error
		srcPods, err = ListPods(ctx, s.ClientSet, testNamespace, c.Src.Label)
		if err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to list src pod %v", c.Src.Label))
		}

		var dstServices []corev1.Service
		dstServices, err = ListServices(ctx, s.ClientSet, testNamespace, c.Dst.Label)
		if err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to list dst pod %v", c.Dst.Label))
		}

		var nodeIPs []net.IP
		nodeIPs, err = ListNodeIPs(context.Background(), s.ClientSet)
		if err != nil {
			s.err = err
			s.T().Error(errors.Wrap(err, "fail to list node ip"))
		}

		for _, src := range srcPods {
			for _, svc := range dstServices {
				var addrs []string
				if svc.Spec.ClusterIP != corev1.ClusterIPNone {
					if len(svc.Spec.Ports) < 1 {
						s.err = errors.New("fail to allocate service port")
						s.T().Error(s.err.Error())
						continue
					}
					addrs = append(addrs, net.JoinHostPort(svc.Spec.ClusterIP, fmt.Sprintf("%d", svc.Spec.Ports[0].Port)))
				}

				if !enablePolicy {
					if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
						if len(svc.Status.LoadBalancer.Ingress) < 1 || len(svc.Spec.Ports) < 1 {
							s.err = errors.New("fail to allocate load balancer ip or port")
							s.T().Error(s.err.Error())
							continue
						}
						addrs = append(addrs, net.JoinHostPort(svc.Status.LoadBalancer.Ingress[0].IP, fmt.Sprintf("%d", svc.Spec.Ports[0].Port)))
					}

					if svc.Spec.Type == corev1.ServiceTypeNodePort || svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
						for _, nodeIP := range nodeIPs {
							if len(svc.Spec.Ports) < 1 {
								s.err = errors.New("fail to allocate service node port")
								s.T().Error(s.err.Error())
								continue
							}
							addrs = append(addrs, net.JoinHostPort(nodeIP.String(), fmt.Sprintf("%d", svc.Spec.Ports[0].NodePort)))
						}
					}
				}

				for _, addr := range addrs {
					l := fmt.Sprintf("src %s -> dst svc name %s, addr %s", podInfo(&src), svc.Name, addr)
					stdout, stdErrOut, err := s.ExecHTTPGet(src.Namespace, src.Name, curlAddr(addr))
					s.Expected(c.Status, stdout, stdErrOut, err, l)
				}
			}
		}
	}
}

func (s *ConnectionTestSuite) TestPod2ServiceName() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToServiceName || c.Skip {
			continue
		}
		ctx := context.Background()
		var srcPods []corev1.Pod
		var err error
		srcPods, err = ListPods(ctx, s.ClientSet, testNamespace, c.Src.Label)
		if err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to list src pod %v", c.Src.Label))
		}

		var dstServices []corev1.Service
		dstServices, err = ListServices(ctx, s.ClientSet, testNamespace, c.Dst.Label)
		if err != nil {
			s.err = err
			s.T().Error(errors.Wrapf(err, "fail to list dst service %v", c.Dst.Label))
		}

		for _, src := range srcPods {
			for _, svc := range dstServices {
				l := fmt.Sprintf("src %s -> dst svc name %s", podInfo(&src), svc.Name)
				stdout, stdErrOut, err := s.ExecHTTPGet(src.Namespace, src.Name, net.JoinHostPort(svc.Name, fmt.Sprintf("%d", httpTestPort)))
				s.Expected(c.Status, stdout, stdErrOut, err, l)
			}
		}
	}
}

func (s *ConnectionTestSuite) Expected(status bool, stdout []byte, stdErrOut []byte, err error, msg string) {
	s.T().Logf("stdOut: %s, errOut: %s", string(stdout), string(stdErrOut))
	if status {
		if assert.NoError(s.T(), err, msg) && assert.Equal(s.T(), 0, len(stdErrOut), msg) {
			s.T().Logf(msg + ", test pass")
		} else {
			s.err = err
			s.T().Error(errors.Wrapf(err, "%s, test failed, expected connection success, but connection failure", msg))
		}
	} else {
		if assert.Error(s.T(), err, msg) {
			s.T().Logf(msg + ", test pass")
		} else {
			s.T().Errorf(msg + ", test failed, expected connection failure, but connection success")
		}
	}
}

func (s *ConnectionTestSuite) ExecHTTPGet(namespace, name string, dst string) ([]byte, []byte, error) {
	cmd := fmt.Sprintf("curl -o /dev/null -s -w %%{http_code} %%{time_connect} %%{time_total} --connect-timeout 6 --retry 2 %s", dst)
	s.T().Log(cmd)
	return Exec(s.ClientSet, s.RestConf, namespace, name, []string{
		"/usr/bin/bash",
		"-c",
		cmd,
	})
}

func (s *ConnectionTestSuite) PrintEvents(namespace string) {
	events, _ := s.ClientSet.CoreV1().Events(namespace).List(context.TODO(), metav1.ListOptions{})
	for i, e := range events.Items {
		s.T().Logf("event #%d: %v", i, e)
	}
}

func (s *ConnectionTestSuite) PrintPods(namespace string) {
	pods, _ := s.ClientSet.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	for i, e := range pods.Items {
		s.T().Logf("pod #%d: %v", i, e)
	}
}

func podInfo(pod *corev1.Pod) string {
	return fmt.Sprintf("%s(%s)", pod.Name, pod.Spec.NodeName)
}

func podIPs(pod *corev1.Pod) []string {
	result := []string{pod.Status.PodIP}
	if len(pod.Status.PodIPs) == 2 {
		result = append(result, pod.Status.PodIPs[1].IP)
	}
	return result
}

func curlAddr(addr string) string {
	if strings.Contains(addr, "[") {
		return fmt.Sprintf("-g -6 http://%s", addr)
	}
	return addr
}
