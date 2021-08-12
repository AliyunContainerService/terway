package tests

import (
	"context"
	"fmt"
	"net"
	"testing"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestConnectionTestSuite(t *testing.T) {
	suite.Run(t, new(ConnectionTestSuite))
}

type ConnectionTestSuite struct {
	suite.Suite

	RestConf  *rest.Config
	ClientSet kubernetes.Interface

	TestCase []TestCase

	TestNamespace string
}

func (s *ConnectionTestSuite) SetupSuite() {
	s.RestConf = ctrl.GetConfigOrDie()
	utils.RegisterClients(s.RestConf)
	s.ClientSet = utils.K8sClient

	s.TestNamespace = "network-test"

	ctx := context.Background()
	s.T().Logf("creating namespace %s", s.TestNamespace)
	err := EnsureNamespace(ctx, utils.K8sClient, s.TestNamespace)
	assert.NoError(s.T(), err)

	s.T().Logf("creating container-network-pod-a")
	_, err = EnsureContainerPods(ctx, s.ClientSet, podConnA)
	assert.NoError(s.T(), err)

	s.T().Logf("creating container-network-pod-b")
	_, err = EnsureContainerPods(ctx, s.ClientSet, podConnB)
	assert.NoError(s.T(), err)

	s.T().Logf("creating container-network-pod-b")
	_, err = EnsureService(ctx, s.ClientSet, podConnB)
	assert.NoError(s.T(), err)

	s.T().Logf("creating host-network-pod")
	_, err = EnsureContainerPods(ctx, s.ClientSet, hostNetworkPod)
	assert.NoError(s.T(), err)

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
			Type: TestTypePodToService,
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
					"app": "container-network-pod-src",
					"e2e": "true",
				},
			},
			Dst: Resource{
				Label: map[string]string{
					"app": "host-network-pod",
					"e2e": "true",
				},
			},
			Status: true,
		},
	}
}

func (s *ConnectionTestSuite) TearDownSuite() {
	ctx := context.Background()
	err := EnsureNamespace(ctx, utils.K8sClient, s.TestNamespace)
	assert.NoError(s.T(), err)

	s.T().Logf("delete container-network-pod-a")
	err = DeleteDs(ctx, s.ClientSet, s.TestNamespace, "container-network-pod-a")
	assert.NoError(s.T(), err)

	s.T().Logf("delete container-network-pod-b")
	err = DeleteDs(ctx, s.ClientSet, s.TestNamespace, "container-network-pod-b")
	assert.NoError(s.T(), err)

	s.T().Logf("delete container-network-pod-b")
	err = DeleteSvc(ctx, s.ClientSet, s.TestNamespace, "container-network-pod-b")
	assert.NoError(s.T(), err)

	s.T().Logf("delete host-network-pod")
	err = DeleteDs(ctx, s.ClientSet, s.TestNamespace, "host-network-pod")
	assert.NoError(s.T(), err)

	s.T().Logf("delete ns")
	err = DeleteNs(ctx, s.ClientSet, s.TestNamespace)
	assert.NoError(s.T(), err)
}

func (s *ConnectionTestSuite) TestPod2Pod() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToPod {
			continue
		}
		ctx := context.Background()
		srcPods, err := ListPods(ctx, s.ClientSet, "", c.Src.Label)
		assert.NoError(s.T(), err)

		dstPods, err := ListPods(ctx, s.ClientSet, "", c.Dst.Label)
		assert.NoError(s.T(), err)
		for _, src := range srcPods {
			for _, dst := range dstPods {
				s.T().Logf("src %s -> dst %s", podInfo(&src), podInfo(&dst))
				for _, ip := range podIPs(&dst) {
					l := fmt.Sprintf("src %s -> dst %s", podInfo(&src), ip)

					_, stdErrOut, err := s.ExecHttpGet(ctx, src.Namespace, src.Name, ip)
					assert.NoError(s.T(), err, l)
					assert.Equal(s.T(), 0, len(stdErrOut), l)
					s.T().Logf(l + " ok ")
				}
			}
		}
	}
}

func (s *ConnectionTestSuite) TestPod2Service() {
	for _, c := range s.TestCase {
		if c.Type != TestTypePodToService {
			continue
		}
		ctx := context.Background()
		srcPods, err := ListPods(ctx, s.ClientSet, s.TestNamespace, c.Src.Label)
		assert.NoError(s.T(), err)

		dstServices, err := ListServices(ctx, s.ClientSet, s.TestNamespace, c.Dst.Label)
		assert.NoError(s.T(), err)

		nodeIPs, err := ListNodeIPs(context.Background(), s.ClientSet)
		assert.NoError(s.T(), err)

		var ips []string
		for _, svc := range dstServices {
			ips = append(ips, svc.Spec.ClusterIP)
			ips = append(ips, svc.Status.LoadBalancer.Ingress[0].IP)

			for _, nodeIP := range nodeIPs {
				ips = append(ips, net.JoinHostPort(nodeIP.String(), fmt.Sprintf("%d", svc.Spec.Ports[0].NodePort)))
			}
		}

		for _, src := range srcPods {
			s.T().Logf("src %s -> dst svc", podInfo(&src))
			for _, ip := range ips {
				ctx := context.Background()
				l := fmt.Sprintf("src %s -> dst %s", podInfo(&src), ip)

				_, stdErrOut, err := s.ExecHttpGet(ctx, src.Namespace, src.Name, ip)
				assert.NoError(s.T(), err, l)
				assert.Equal(s.T(), 0, len(stdErrOut), l)
				s.T().Logf(l + " ok ")
			}
		}
	}
}

func (s *ConnectionTestSuite) ExecHttpGet(ctx context.Context, namespace, name string, ip string) ([]byte, []byte, error) {
	cmd := fmt.Sprintf("curl -o /dev/null -s -w %%{http_code} %%{time_connect} %%{time_total} --connect-timeout 5 --retry 3 %s", curlIP(ip))
	return Exec(ctx, s.ClientSet, s.RestConf, namespace, name, []string{
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
