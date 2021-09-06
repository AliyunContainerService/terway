//go:build e2e
// +build e2e

package tests

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/AliyunContainerService/terway/pkg/generated/clientset/versioned"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
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

	TestCase []TestCase

	TestNamespace string
}

func (s *ConnectionTestSuite) SetupSuite() {
	s.RestConf = ctrl.GetConfigOrDie()
	utils.RegisterClients(s.RestConf)
	s.ClientSet = utils.K8sClient
	s.PodNetworkingClientSet, _ = versioned.NewForConfig(s.RestConf)

	s.TestNamespace = "network-test"

	ctx := context.Background()
	s.T().Logf("enable trunk: %v", enableTrunk)
	s.T().Logf("enable policy: %v", enablePolicy)
	s.T().Logf("creating namespace %s", s.TestNamespace)
	err := EnsureNamespace(ctx, s.ClientSet, s.TestNamespace)
	assert.NoError(s.T(), err)

	if enableTrunk {
		s.T().Logf("creating %s pod networking", elasticPodNetWorking.Name)
		_, err = EnsurePodNetworking(ctx, s.PodNetworkingClientSet, elasticPodNetWorking)
		assert.NoError(s.T(), err)

		s.T().Logf("creating %s pod networking", fixedPodNetWorking.Name)
		_, err = EnsurePodNetworking(ctx, s.PodNetworkingClientSet, fixedPodNetWorking)
		assert.NoError(s.T(), err)
	}

	s.T().Logf("creating %s", podConnA.Name)
	_, err = EnsureDaemonSet(ctx, s.ClientSet, podConnA)
	assert.NoError(s.T(), err)

	s.T().Logf("creating %s", podConnB.Name)
	_, err = EnsureDaemonSet(ctx, s.ClientSet, podConnB)
	assert.NoError(s.T(), err)

	s.T().Logf("creating %s", podConnC.Name)
	_, err = EnsureDeployment(ctx, s.ClientSet, podConnC)
	assert.NoError(s.T(), err)

	s.T().Logf("creating %s", podConnD.Name)
	_, err = EnsureStatefulSet(ctx, s.ClientSet, podConnD)
	assert.NoError(s.T(), err)

	s.T().Logf("creating %s for %s", clusterIPService.Name, podConnC.Name)
	_, err = EnsureService(ctx, s.ClientSet, clusterIPService)
	assert.NoError(s.T(), err)

	s.T().Logf("creating %s for %s", nodePortService.Name, podConnC.Name)
	_, err = EnsureService(ctx, s.ClientSet, nodePortService)
	assert.NoError(s.T(), err)

	s.T().Logf("creating %s for %s", loadBalancerService.Name, podConnC.Name)
	_, err = EnsureService(ctx, s.ClientSet, loadBalancerService)
	assert.NoError(s.T(), err)

	s.T().Logf("creating %s for %s", headlessService.Name, podConnC.Name)
	_, err = EnsureService(ctx, s.ClientSet, headlessService)
	assert.NoError(s.T(), err)

	if enablePolicy {
		s.T().Logf("creating network policy %s", networkPolicy.Name)
		_, err = EnsureNetworkPolicy(ctx, s.ClientSet, networkPolicy)
		assert.NoError(s.T(), err)
	}

	s.TestCase = []TestCase{
		{
			Type: TestTypePodToPod,
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
			Status: !enablePolicy,
		},
		{
			Type: TestTypePodToServiceIP,
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
			Status: !enablePolicy,
		},
		{
			Type: TestTypePodToServiceName,
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
	}
}

func (s *ConnectionTestSuite) TearDownSuite() {
	ctx := context.Background()
	err := EnsureNamespace(ctx, s.ClientSet, s.TestNamespace)
	assert.NoError(s.T(), err)

	if enablePolicy {
		s.T().Logf("delete %s", networkPolicy.Name)
		err = DeleteNetworkPolicy(ctx, s.ClientSet, s.TestNamespace, networkPolicy.Name)
		assert.NoError(s.T(), err)

	}

	s.T().Logf("delete %s", podConnA.Name)
	err = DeleteDaemonSet(ctx, s.ClientSet, s.TestNamespace, podConnA.Name)
	assert.NoError(s.T(), err)

	s.T().Logf("delete %s", clusterIPService.Name)
	err = DeleteService(ctx, s.ClientSet, s.TestNamespace, clusterIPService.Name)
	assert.NoError(s.T(), err)

	s.T().Logf("delete %s", nodePortService.Name)
	err = DeleteService(ctx, s.ClientSet, s.TestNamespace, nodePortService.Name)
	assert.NoError(s.T(), err)

	s.T().Logf("delete %s", loadBalancerService.Name)
	err = DeleteService(ctx, s.ClientSet, s.TestNamespace, loadBalancerService.Name)
	assert.NoError(s.T(), err)

	s.T().Logf("delete %s", headlessService.Name)
	err = DeleteService(ctx, s.ClientSet, s.TestNamespace, headlessService.Name)
	assert.NoError(s.T(), err)

	s.T().Logf("delete %s", podConnA.Name)
	err = DeleteDaemonSet(ctx, s.ClientSet, s.TestNamespace, podConnA.Name)
	assert.NoError(s.T(), err)

	s.T().Logf("delete %s", podConnB.Name)
	err = DeleteDaemonSet(ctx, s.ClientSet, s.TestNamespace, podConnB.Name)
	assert.NoError(s.T(), err)

	s.T().Logf("delete %s", podConnC.Name)
	err = DeleteDeployment(ctx, s.ClientSet, s.TestNamespace, podConnC.Name)
	assert.NoError(s.T(), err)

	s.T().Logf("delete %s", podConnD.Name)
	err = DeleteStatefulSet(ctx, s.ClientSet, s.TestNamespace, podConnD.Name)
	assert.NoError(s.T(), err)

	if enableTrunk {
		s.T().Logf("delete %s pod networking", elasticPodNetWorking.Name)
		err = DeletePodNetworking(ctx, s.PodNetworkingClientSet, elasticPodNetWorking.Name)
		assert.NoError(s.T(), err)

		s.T().Logf("delete %s pod networking", fixedPodNetWorking.Name)
		err = DeletePodNetworking(ctx, s.PodNetworkingClientSet, fixedPodNetWorking.Name)
		assert.NoError(s.T(), err)
	}

	s.T().Logf("delete ns")
	err = DeleteNamespace(ctx, s.ClientSet, s.TestNamespace)
	assert.NoError(s.T(), err)
}

func (s *ConnectionTestSuite) TestPod2Pod() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToPod {
			continue
		}
		ctx := context.Background()
		srcPods, err := ListPods(ctx, s.ClientSet, s.TestNamespace, c.Src.Label)
		assert.NoError(s.T(), err)

		dstPods, err := ListPods(ctx, s.ClientSet, s.TestNamespace, c.Dst.Label)
		assert.NoError(s.T(), err)
		for _, src := range srcPods {
			for _, dst := range dstPods {
				s.T().Logf("src %s -> dst %s", podInfo(&src), podInfo(&dst))
				for _, ip := range podIPs(&dst) {
					l := fmt.Sprintf("src %s -> dst %s", podInfo(&src), ip)
					_, stdErrOut, err := s.ExecHTTPGet(src.Namespace, src.Name, ip)
					s.Expected(c.Status, stdErrOut, err, l)
				}
			}
		}
	}
}

func (s *ConnectionTestSuite) TestPod2ServiceIP() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToServiceIP {
			continue
		}
		ctx := context.Background()
		srcPods, err := ListPods(ctx, s.ClientSet, s.TestNamespace, c.Src.Label)
		assert.NoError(s.T(), err)

		dstServices, err := ListServices(ctx, s.ClientSet, s.TestNamespace, c.Dst.Label)
		assert.NoError(s.T(), err)

		nodeIPs, err := ListNodeIPs(context.Background(), s.ClientSet)
		assert.NoError(s.T(), err)

		for _, src := range srcPods {
			for _, svc := range dstServices {
				var ips []string
				if svc.Spec.ClusterIP != corev1.ClusterIPNone {
					ips = append(ips, svc.Spec.ClusterIP)
				}

				if !enablePolicy {
					if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
						ips = append(ips, svc.Status.LoadBalancer.Ingress[0].IP)
					}

					if svc.Spec.Type == corev1.ServiceTypeNodePort || svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
						for _, nodeIP := range nodeIPs {
							ips = append(ips, net.JoinHostPort(nodeIP.String(), fmt.Sprintf("%d", svc.Spec.Ports[0].NodePort)))
						}
					}
				}

				for _, ip := range ips {
					l := fmt.Sprintf("src %s -> dst svc name %s, ip %s", podInfo(&src), svc.Name, ip)
					_, stdErrOut, err := s.ExecHTTPGet(src.Namespace, src.Name, curlIP(ip))
					s.Expected(c.Status, stdErrOut, err, l)
				}
			}
		}
	}
}

func (s *ConnectionTestSuite) TestPod2ServiceName() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToServiceName {
			continue
		}
		ctx := context.Background()
		srcPods, err := ListPods(ctx, s.ClientSet, s.TestNamespace, c.Src.Label)
		assert.NoError(s.T(), err)

		dstServices, err := ListServices(ctx, s.ClientSet, s.TestNamespace, c.Dst.Label)
		assert.NoError(s.T(), err)

		for _, src := range srcPods {
			for _, svc := range dstServices {
				l := fmt.Sprintf("src %s -> dst svc name %s", podInfo(&src), svc.Name)
				_, stdErrOut, err := s.ExecHTTPGet(src.Namespace, src.Name, svc.Name)
				s.Expected(c.Status, stdErrOut, err, l)
			}
		}
	}
}

func (s *ConnectionTestSuite) Expected(status bool, stdErrOut []byte, err error, msg string) {
	if status {
		if assert.NoError(s.T(), err, msg) && assert.Equal(s.T(), 0, len(stdErrOut), msg) {
			s.T().Logf(msg + ", test pass")
		} else {
			s.T().Errorf(msg + ", test failed")
			s.T().Errorf("expected connection success, but connection failure")
		}
	} else {
		if assert.Error(s.T(), err, msg) {
			s.T().Logf(msg + ", test pass")
		} else {
			s.T().Errorf(msg + ", test failed")
			s.T().Errorf("expected connection failure, but connection success")
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

func curlIP(ip string) string {
	if terwayIP.IPv6(net.ParseIP(ip)) {
		return fmt.Sprintf("-6 %s", ip)
	}
	return ip
}
