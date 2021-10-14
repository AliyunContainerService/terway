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
	s.T().Logf("test namespace: %s", testNamespace)
	s.T().Logf("enable trunk: %v", enableTrunk)
	s.T().Logf("enable policy: %v", enablePolicy)
	s.T().Logf("creating namespace %s", testNamespace)
	if s.err = EnsureNamespace(ctx, s.ClientSet, testNamespace); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create namespace %s", testNamespace))
	}

	if enableTrunk {
		s.T().Logf("creating %s pod networking", elasticPodNetWorking.Name)
		if _, s.err = EnsurePodNetworking(ctx, s.PodNetworkingClientSet, elasticPodNetWorking); s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to create pod networking %s", elasticPodNetWorking.Name))
		}

		s.T().Logf("creating %s pod networking", fixedPodNetWorking.Name)
		if _, s.err = EnsurePodNetworking(ctx, s.PodNetworkingClientSet, fixedPodNetWorking); s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to create pod networking %s", fixedPodNetWorking.Name))
		}
	}

	s.T().Logf("creating %s", podConnA.Name)
	if _, s.err = EnsureDaemonSet(ctx, s.ClientSet, podConnA); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create pod %s", podConnA.Name))
	}

	s.T().Logf("creating %s", podConnB.Name)
	if _, s.err = EnsureDaemonSet(ctx, s.ClientSet, podConnB); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create pod %s", podConnB.Name))
	}

	s.T().Logf("creating %s", podConnC.Name)
	if _, s.err = EnsureDeployment(ctx, s.ClientSet, podConnC); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create pod %s", podConnC.Name))
	}

	s.T().Logf("creating %s", podConnD.Name)
	if _, s.err = EnsureStatefulSet(ctx, s.ClientSet, podConnD); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create pod %s", podConnD.Name))
	}

	s.T().Logf("creating %s", podConnPolicy.Name)
	if _, s.err = EnsureDaemonSet(ctx, s.ClientSet, podConnPolicy); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create network policy %s", podConnPolicy.Name))
	}

	s.T().Logf("creating %s for %s", clusterIPService.Name, podConnC.Name)
	if _, s.err = EnsureService(ctx, s.ClientSet, clusterIPService); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create service %s", clusterIPService.Name))
	}

	s.T().Logf("creating %s for %s", nodePortService.Name, podConnC.Name)
	if _, s.err = EnsureService(ctx, s.ClientSet, nodePortService); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create service %s", nodePortService.Name))
	}

	s.T().Logf("creating %s for %s", loadBalancerService.Name, podConnC.Name)
	if _, s.err = EnsureService(ctx, s.ClientSet, loadBalancerService); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create service %s", loadBalancerService.Name))
	}

	s.T().Logf("creating %s for %s", headlessService.Name, podConnC.Name)
	if _, s.err = EnsureService(ctx, s.ClientSet, headlessService); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to create service %s", headlessService.Name))
	}

	if enablePolicy {
		s.T().Logf("creating network policy %s", networkPolicy.Name)
		if _, s.err = EnsureNetworkPolicy(ctx, s.ClientSet, networkPolicy); s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to create network policy %s", networkPolicy.Name))
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
		s.T().Errorf("skip tear down resource in namespace %s, because of an error occurred.", testNamespace)
		return
	}
	ctx := context.Background()
	if enablePolicy {
		s.T().Logf("delete %s", networkPolicy.Name)
		if s.err = DeleteNetworkPolicy(ctx, s.ClientSet, testNamespace, networkPolicy.Name); s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to delete network policy %s", networkPolicy.Name))
		}
	}

	s.T().Logf("delete %s", clusterIPService.Name)
	if s.err = DeleteService(ctx, s.ClientSet, testNamespace, clusterIPService.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete service %s", clusterIPService.Name))
	}

	s.T().Logf("delete %s", nodePortService.Name)
	if s.err = DeleteService(ctx, s.ClientSet, testNamespace, nodePortService.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete service %s", nodePortService.Name))
	}

	s.T().Logf("delete %s", loadBalancerService.Name)
	if s.err = DeleteService(ctx, s.ClientSet, testNamespace, loadBalancerService.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete service %s", loadBalancerService.Name))
	}

	s.T().Logf("delete %s", headlessService.Name)
	if s.err = DeleteService(ctx, s.ClientSet, testNamespace, headlessService.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete service %s", headlessService.Name))
	}

	s.T().Logf("delete %s", podConnA.Name)
	if s.err = DeleteDaemonSet(ctx, s.ClientSet, testNamespace, podConnA.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete pod %s", podConnA.Name))
	}

	s.T().Logf("delete %s", podConnB.Name)
	if s.err = DeleteDaemonSet(ctx, s.ClientSet, testNamespace, podConnB.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete pod %s", podConnB.Name))
	}

	s.T().Logf("delete %s", podConnC.Name)
	if s.err = DeleteDeployment(ctx, s.ClientSet, testNamespace, podConnC.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete pod %s", podConnC.Name))
	}

	s.T().Logf("delete %s", podConnD.Name)
	if s.err = DeleteStatefulSet(ctx, s.ClientSet, testNamespace, podConnD.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete pod %s", podConnD.Name))
	}

	s.T().Logf("delete %s", podConnPolicy.Name)
	if s.err = DeleteDaemonSet(ctx, s.ClientSet, testNamespace, podConnPolicy.Name); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete pod %s", podConnPolicy.Name))
	}

	if enableTrunk {
		s.T().Logf("delete %s pod networking", elasticPodNetWorking.Name)
		if s.err = DeletePodNetworking(ctx, s.PodNetworkingClientSet, elasticPodNetWorking.Name); s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to delete pod networking %s", elasticPodNetWorking.Name))
		}

		s.T().Logf("delete %s pod networking", fixedPodNetWorking.Name)
		if s.err = DeletePodNetworking(ctx, s.PodNetworkingClientSet, fixedPodNetWorking.Name); s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to delete pod networking %s", fixedPodNetWorking.Name))
		}
	}

	s.T().Logf("delete ns")
	if s.err = DeleteNamespace(ctx, s.ClientSet, testNamespace); s.err != nil {
		s.T().Error(errors.Wrapf(s.err, "fail to delete namespace %s", testNamespace))
	}
}

func (s *ConnectionTestSuite) TestPod2Pod() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToPod || c.Skip {
			continue
		}
		ctx := context.Background()
		var srcPods []corev1.Pod
		srcPods, s.err = ListPods(ctx, s.ClientSet, testNamespace, c.Src.Label)
		if s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to list src pod %v", c.Src.Label))
		}

		var dstPods []corev1.Pod
		dstPods, s.err = ListPods(ctx, s.ClientSet, testNamespace, c.Dst.Label)
		if s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to list dst pod %v", c.Dst.Label))
		}
		for _, src := range srcPods {
			for _, dst := range dstPods {
				s.T().Logf("src %s -> dst %s", podInfo(&src), podInfo(&dst))
				for _, ip := range podIPs(&dst) {
					addr := net.JoinHostPort(ip, "80")
					l := fmt.Sprintf("src %s -> dst %s", podInfo(&src), addr)
					var stdErrOut []byte
					_, stdErrOut, s.err = s.ExecHTTPGet(src.Namespace, src.Name, curlAddr(addr))
					s.Expected(c.Status, stdErrOut, s.err, l)
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
		srcPods, s.err = ListPods(ctx, s.ClientSet, testNamespace, c.Src.Label)
		if s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to list src pod %v", c.Src.Label))
		}

		var dstServices []corev1.Service
		dstServices, s.err = ListServices(ctx, s.ClientSet, testNamespace, c.Dst.Label)
		if s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to list dst pod %v", c.Dst.Label))
		}

		var nodeIPs []net.IP
		nodeIPs, s.err = ListNodeIPs(context.Background(), s.ClientSet)
		if s.err != nil {
			s.T().Error(errors.Wrap(s.err, "fail to list node ip"))
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
					var stdErrOut []byte
					_, stdErrOut, s.err = s.ExecHTTPGet(src.Namespace, src.Name, curlAddr(addr))
					s.Expected(c.Status, stdErrOut, s.err, l)
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
		srcPods, s.err = ListPods(ctx, s.ClientSet, testNamespace, c.Src.Label)
		if s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to list src pod %v", c.Src.Label))
		}

		var dstServices []corev1.Service
		dstServices, s.err = ListServices(ctx, s.ClientSet, testNamespace, c.Dst.Label)
		if s.err != nil {
			s.T().Error(errors.Wrapf(s.err, "fail to list dst service %v", c.Dst.Label))
		}

		for _, src := range srcPods {
			for _, svc := range dstServices {
				l := fmt.Sprintf("src %s -> dst svc name %s", podInfo(&src), svc.Name)
				var stdErrOut []byte
				_, stdErrOut, s.err = s.ExecHTTPGet(src.Namespace, src.Name, svc.Name)
				s.Expected(c.Status, stdErrOut, s.err, l)
			}
		}
	}
}

func (s *ConnectionTestSuite) Expected(status bool, stdErrOut []byte, err error, msg string) {
	if status {
		if assert.NoError(s.T(), err, msg) && assert.Equal(s.T(), 0, len(stdErrOut), msg) {
			s.T().Logf(msg + ", test pass")
		} else {
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
	cmd := fmt.Sprintf("curl -o /dev/null -s -w %%{http_code} %%{time_connect} %%{time_total} --connect-timeout 3 --retry 2 %s", dst)
	return Exec(s.ClientSet, s.RestConf, namespace, name, []string{
		"/usr/bin/bash",
		"-c",
		cmd,
	})
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
