//go:build e2e

package tests

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/e2e-framework/pkg/types"
)

const (
	serverImage = "registry-cn-hangzhou.ack.aliyuncs.com/test/graceful-termination-test-apps"
	clientImage = "registry-cn-hangzhou.ack.aliyuncs.com/test/graceful-termination-test-apps"
)

type NetworkTestCase struct {
	Name                string
	ServerHostNetwork   bool
	ClientHostNetwork   bool
	SameNode            bool
	IPv6                bool
	ServiceType         string // ClusterIP, NodePort
	TrafficPolicy       string // Cluster, Local (for NodePort)
	ServerPodNetworking string // Optional, specific pod networking to use
	ClientPodNetworking string // Optional, specific pod networking to use

	ListenPort int // server lister port
	TargetPort int // client conn port
}

func generateTestCases() []NetworkTestCase {
	var testCases []NetworkTestCase

	// Connection types
	connTypes := []struct {
		sameNode          bool
		serverHostNetwork bool
		clientHostNetwork bool
		listenPort        int
		targetPort        int
	}{
		{true, false, false, 80, 80},       // Same node, both pod network
		{false, false, false, 80, 80},      // Cross node, both pod network
		{false, true, false, 20000, 31000}, // Cross node, server host network
		{false, false, true, 20001, 31001}, // Cross node, client host network
		{false, true, true, 20002, 31002},  // Cross node, both host network
		{true, true, false, 20003, 31003},  // Same node, server host network
		{true, false, true, 20004, 31004},  // Same node, client host network
		{true, true, true, 20005, 31005},   // Same node, both host network
	}

	// Service types
	serviceTypes := []string{"ClusterIP"} // , "NodePort"

	// IP versions
	ipVersions := []bool{false, true} // false=IPv4, true=IPv6

	// Traffic policies for NodePort
	trafficPolicies := []string{"Cluster", "Local"}

	// Generate cases
	for _, conn := range connTypes {
		for _, serviceType := range serviceTypes {
			for _, ipv6 := range ipVersions {
				if serviceType == "NodePort" {
					for _, policy := range trafficPolicies {
						testCases = append(testCases, NetworkTestCase{
							Name:              formatName(conn.sameNode, conn.serverHostNetwork, conn.clientHostNetwork, serviceType, policy, ipv6),
							ServerHostNetwork: conn.serverHostNetwork,
							ClientHostNetwork: conn.clientHostNetwork,
							SameNode:          conn.sameNode,
							IPv6:              ipv6,
							ServiceType:       serviceType,
							TrafficPolicy:     policy,
							ListenPort:        conn.listenPort,
							TargetPort:        conn.targetPort,
						})
					}
				} else {
					testCases = append(testCases, NetworkTestCase{
						Name:              formatName(conn.sameNode, conn.serverHostNetwork, conn.clientHostNetwork, serviceType, "", ipv6),
						ServerHostNetwork: conn.serverHostNetwork,
						ClientHostNetwork: conn.clientHostNetwork,
						SameNode:          conn.sameNode,
						IPv6:              ipv6,
						ServiceType:       serviceType,
						ListenPort:        conn.listenPort,
						TargetPort:        conn.targetPort,
					})
				}
			}
		}
	}

	return testCases
}

func ternary(condition bool, trueVal, falseVal string) string {
	if condition {
		return trueVal
	}
	return falseVal
}

func TestUpgrade_PodToPodConnectivity(t *testing.T) {
	testCases := generateTestCases()

	var f []types.Feature
	for _, tc := range testCases {
		if !testIPv6 && tc.IPv6 {
			continue
		}
		tc := tc // capture for closure
		testCase := features.New(fmt.Sprintf("PodToPodConnectivity/%s", tc.Name)).
			WithSetup(tc.Name, setupTestCase(tc)).
			Assess("verify podStatus", verifyPodStatus(tc)).
			Feature()

		f = append(f, testCase)
	}
	testenv.TestInParallel(t, f...)
}

func setupTestCase(tc NetworkTestCase) func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		// Create server pod
		serverPod := createServerPod(tc, cfg.Namespace())
		err := cfg.Client().Resources().Create(ctx, serverPod)
		if err != nil {
			t.Fatalf("Failed to create server pod: %v", err)
		}
		ctx = SaveResources(ctx, serverPod)

		// Create service for the server pod
		svc := createService(tc, serverPod, cfg.Namespace())
		err = cfg.Client().Resources().Create(ctx, svc)
		if err != nil {
			t.Fatalf("Failed to create service: %v", err)
		}
		ctx = SaveResources(ctx, svc)

		// Wait for server pod to be ready

		err = wait.For(conditions.New(cfg.Client().Resources()).PodReady(serverPod), wait.WithTimeout(time.Minute*2), wait.WithInterval(time.Second*5))
		if err != nil {
			t.Fatalf("Server pod failed to become ready: %v", err)
		}

		// Get node info for server pod to schedule client on same/different node if needed
		err = cfg.Client().Resources().Get(ctx, serverPod.Name, serverPod.Namespace, serverPod)
		if err != nil {
			t.Fatalf("Failed to get server pod: %v", err)
		}
		serverNodeName := serverPod.Spec.NodeName

		serverNode := &corev1.Node{}
		err = cfg.Client().Resources().Get(context.Background(), serverNodeName, "", serverNode)
		if err != nil {
			t.Fatalf("Failed to get server node: %v", err)
		}

		nV4, nV6 := netip.Addr{}, netip.Addr{}
		lo.ForEach(serverNode.Status.Addresses, func(item corev1.NodeAddress, index int) {
			if item.Type == corev1.NodeInternalIP {
				a, err := netip.ParseAddr(item.Address)
				if err != nil {
					t.Fatalf("Failed to parse addr %s", err)
					return
				}
				if a.Is6() {
					nV6 = a
				} else {
					nV4 = a
				}
			}
		})

		// Create client pod
		clientPod := createClientPod(tc, cfg.Namespace(), serverNodeName, svc, nV4, nV6)

		err = cfg.Client().Resources().Create(ctx, clientPod)
		if err != nil {
			t.Fatalf("Failed to create client pod: %v", err)
		}
		ctx = SaveResources(ctx, clientPod)

		err = wait.For(conditions.New(cfg.Client().Resources()).PodReady(clientPod), wait.WithTimeout(time.Minute*2), wait.WithInterval(time.Second*5))
		if err != nil {
			t.Fatalf("Client pod failed to become ready: %v", err)
		}
		return ctx
	}
}

func createServerPod(tc NetworkTestCase, namespace string) *corev1.Pod {
	podName := fmt.Sprintf("server-%s", tc.Name)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":       podName,
				"test-case": tc.Name,
				"role":      "server",
			},
		},
		Spec: corev1.PodSpec{
			HostNetwork: tc.ServerHostNetwork,
			Containers: []corev1.Container{
				{
					Name:  "server",
					Image: serverImage,
					Command: []string{
						"/server",
						fmt.Sprintf("%d", tc.ListenPort),
					},
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: int32(tc.ListenPort),
							Protocol:      corev1.ProtocolTCP,
						},
					},
				},
			},
			TerminationGracePeriodSeconds: ptr.To(int64(0)),
		},
	}

	// Apply pod networking if specified
	if tc.ServerPodNetworking != "" {
		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		pod.Labels["netplan"] = tc.ServerPodNetworking
	}

	return pod
}

func createClientPod(tc NetworkTestCase, namespace, serverNodeName string, svc *corev1.Service, serverNodeV4, serverNodeV6 netip.Addr) *corev1.Pod {
	podName := fmt.Sprintf("client-%s", tc.Name)

	// Determine the server address to connect to
	var serverAddr string

	if tc.ServiceType == "ClusterIP" {
		// For ClusterIP, just use the service's cluster IP
		serverAddr = net.JoinHostPort(svc.Spec.ClusterIP, strconv.Itoa(int(tc.TargetPort)))
	} else if tc.ServiceType == "NodePort" {
		// For NodePort, we need to use a node's IP and the node port
		if tc.IPv6 {
			serverAddr = net.JoinHostPort(serverNodeV6.String(), string(svc.Spec.Ports[0].NodePort))
		} else {
			serverAddr = net.JoinHostPort(serverNodeV4.String(), string(svc.Spec.Ports[0].NodePort))
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":       podName,
				"test-case": tc.Name,
				"role":      "client",
			},
		},
		Spec: corev1.PodSpec{
			HostNetwork: tc.ClientHostNetwork,
			Containers: []corev1.Container{
				{
					Name:  "client",
					Image: clientImage,
					Command: []string{
						"/client",
						serverAddr,
					},
				},
			},
			TerminationGracePeriodSeconds: ptr.To(int64(0)),
		},
	}

	// Apply pod networking if specified
	if tc.ClientPodNetworking != "" {
		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		pod.Labels["netplan"] = tc.ClientPodNetworking
	}

	// Schedule on same node or different node
	if tc.SameNode {
		pod.Spec.NodeName = serverNodeName
	} else {
		pod.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpNotIn,
									Values:   []string{serverNodeName},
								},
							},
						},
					},
				},
			},
		}
	}

	return pod
}

func createService(tc NetworkTestCase, serverPod *corev1.Pod, namespace string) *corev1.Service {
	svcName := fmt.Sprintf("svc-%s", tc.Name)
	serviceType := corev1.ServiceTypeClusterIP
	if tc.ServiceType == "NodePort" {
		serviceType = corev1.ServiceTypeNodePort
	}

	ipFamilyPolicy := corev1.IPFamilyPolicySingleStack
	ipFamilies := []corev1.IPFamily{corev1.IPv4Protocol}

	if tc.IPv6 {
		ipFamilyPolicy = corev1.IPFamilyPolicySingleStack
		ipFamilies = []corev1.IPFamily{corev1.IPv6Protocol}
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": serverPod.Name,
			},
			Type:           serviceType,
			IPFamilyPolicy: &ipFamilyPolicy,
			IPFamilies:     ipFamilies,
			Ports: []corev1.ServicePort{
				{
					Port:       int32(tc.TargetPort),
					TargetPort: intstr.FromInt32(int32(tc.ListenPort)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	// Set traffic policy for NodePort
	if tc.ServiceType == "NodePort" && tc.TrafficPolicy != "" {
		if tc.TrafficPolicy == "Local" {
			svc.Spec.InternalTrafficPolicy = ptr.To(corev1.ServiceInternalTrafficPolicyLocal)
		} else {
			svc.Spec.InternalTrafficPolicy = ptr.To(corev1.ServiceInternalTrafficPolicyCluster)
		}
	}

	return svc
}

func verifyPodStatus(tc NetworkTestCase) func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		innerCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()

		clientPod := &corev1.Pod{}
		err := wait.For(func(ctx context.Context) (bool, error) {
			err := cfg.Client().Resources().Get(ctx, fmt.Sprintf("client-%s", tc.Name), cfg.Namespace(), clientPod)
			if err != nil {
				return false, err
			}

			// Check if pod completed
			if clientPod.Status.Phase != corev1.PodRunning {
				return true, fmt.Errorf("")
			}
			if clientPod.Status.Phase == corev1.PodFailed {
				return false, fmt.Errorf("client pod failed")
			}

			for _, status := range clientPod.Status.ContainerStatuses {
				if status.RestartCount > 0 {
					return false, fmt.Errorf("client pod has failed")
				}
			}

			return false, nil
		}, wait.WithTimeout(time.Minute*30), wait.WithInterval(time.Second*10), wait.WithContext(innerCtx))

		if err != nil {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Client pod has failed: %v", err)
			}
		}

		return ctx
	}
}

func formatName(sameNode bool, srvHostNetwork bool, cliHostNetwork bool, svcType string, inClusterPolicy string, stack bool) string {
	out := ""
	if sameNode {
		out += "same"
	} else {
		out += "cross"
	}

	if cliHostNetwork {
		out += "-host-to"
	} else {
		out += "-pod-to"
	}

	if srvHostNetwork {
		out += "-host"
	} else {
		out += "-pod"
	}

	if svcType == "NodePort" {
		out += "-nodeport-" + strings.ToLower(inClusterPolicy)
	}

	return out + "-" + ternary(stack, "6", "4")
}
