//go:build e2e

package tests

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const echoServerImage = "registry.cn-hangzhou.aliyuncs.com/l1b0k/echo"

// TestConnectivityWithReport runs connectivity tests and generates a comprehensive report
func TestConnectivityWithReport(t *testing.T) {
	if !HasExternalECSConfig() {
		t.Skip("External ECS configuration not available (set EXTERNAL_ECS_INSTANCE_ID, ACCESS_KEY_ID, ACCESS_KEY_SECRET, REGION)")
	}

	report := NewConnectivityTestReport()

	// Create test scenarios
	scenarios := createAllTestScenarios(t, report)

	testenv.Test(t, scenarios...)

	// Finalize and generate report
	report.Finalize()

	// Output report to console
	t.Log("\n" + report.GenerateMarkdownReport())
}

// TestScenarioConfig holds configuration for a single test scenario
type TestScenarioConfig struct {
	Name              string
	Source            string // "external-ecs", "internal-ecs", "pod"
	Target            string // "pod-ip", "nodeport", "loadbalancer"
	TrafficPolicy     corev1.ServiceExternalTrafficPolicyType
	BackendOnSameNode bool
	LoadBalancerType  string // "internet", "intranet"
}

// createAllTestScenarios creates all test scenarios for the connectivity report
func createAllTestScenarios(t *testing.T, report *ConnectivityTestReport) []features.Feature {
	var feats []features.Feature

	// Define all test scenarios
	scenarios := []TestScenarioConfig{
		// External ECS -> Pod IP
		{Name: "ExtECS-PodIP", Source: "external-ecs", Target: "pod-ip"},

		// External ECS -> NodePort
		{Name: "ExtECS-NP-Cluster-Local", Source: "external-ecs", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: true},
		{Name: "ExtECS-NP-Cluster-Remote", Source: "external-ecs", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: false},
		{Name: "ExtECS-NP-Local-Local", Source: "external-ecs", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: true},
		{Name: "ExtECS-NP-Local-Remote", Source: "external-ecs", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: false},

		// External ECS -> LoadBalancer (Internet)
		{Name: "ExtECS-LB-Internet-Cluster-Local", Source: "external-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: true, LoadBalancerType: "internet"},
		{Name: "ExtECS-LB-Internet-Cluster-Remote", Source: "external-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: false, LoadBalancerType: "internet"},
		{Name: "ExtECS-LB-Internet-Local-Local", Source: "external-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: true, LoadBalancerType: "internet"},
		{Name: "ExtECS-LB-Internet-Local-Remote", Source: "external-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: false, LoadBalancerType: "internet"},

		// External ECS -> LoadBalancer (Intranet)
		{Name: "ExtECS-LB-Intranet-Cluster-Local", Source: "external-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: true, LoadBalancerType: "intranet"},
		{Name: "ExtECS-LB-Intranet-Cluster-Remote", Source: "external-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: false, LoadBalancerType: "intranet"},
		{Name: "ExtECS-LB-Intranet-Local-Local", Source: "external-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: true, LoadBalancerType: "intranet"},
		{Name: "ExtECS-LB-Intranet-Local-Remote", Source: "external-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: false, LoadBalancerType: "intranet"},

		// Internal ECS -> Pod IP
		{Name: "IntECS-PodIP", Source: "internal-ecs", Target: "pod-ip"},

		// Internal ECS -> NodePort
		{Name: "IntECS-NP-Cluster-Local", Source: "internal-ecs", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: true},
		{Name: "IntECS-NP-Cluster-Remote", Source: "internal-ecs", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: false},
		{Name: "IntECS-NP-Local-Local", Source: "internal-ecs", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: true},
		{Name: "IntECS-NP-Local-Remote", Source: "internal-ecs", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: false},

		// Internal ECS -> LoadBalancer
		{Name: "IntECS-LB-Cluster-Local", Source: "internal-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: true, LoadBalancerType: "intranet"},
		{Name: "IntECS-LB-Cluster-Remote", Source: "internal-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: false, LoadBalancerType: "intranet"},
		{Name: "IntECS-LB-Local-Local", Source: "internal-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: true, LoadBalancerType: "intranet"},
		{Name: "IntECS-LB-Local-Remote", Source: "internal-ecs", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: false, LoadBalancerType: "intranet"},

		// Pod -> NodePort
		{Name: "Pod-NP-Cluster-Local", Source: "pod", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: true},
		{Name: "Pod-NP-Cluster-Remote", Source: "pod", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: false},
		{Name: "Pod-NP-Local-Local", Source: "pod", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: true},
		{Name: "Pod-NP-Local-Remote", Source: "pod", Target: "nodeport", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: false},

		// Pod -> LoadBalancer
		{Name: "Pod-LB-Cluster-Local", Source: "pod", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: true, LoadBalancerType: "intranet"},
		{Name: "Pod-LB-Cluster-Remote", Source: "pod", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeCluster, BackendOnSameNode: false, LoadBalancerType: "intranet"},
		{Name: "Pod-LB-Local-Local", Source: "pod", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: true, LoadBalancerType: "intranet"},
		{Name: "Pod-LB-Local-Remote", Source: "pod", Target: "loadbalancer", TrafficPolicy: corev1.ServiceExternalTrafficPolicyTypeLocal, BackendOnSameNode: false, LoadBalancerType: "intranet"},
	}

	for _, scenario := range scenarios {
		feat := createReportableTestScenario(scenario, report)
		feats = append(feats, feat)
	}

	return feats
}

// createReportableTestScenario creates a single test feature that reports its results
func createReportableTestScenario(cfg TestScenarioConfig, report *ConnectivityTestReport) features.Feature {
	serverName := fmt.Sprintf("echo-rpt-%s", strings.ToLower(cfg.Name))
	clientName := fmt.Sprintf("client-rpt-%s", strings.ToLower(cfg.Name))
	serviceName := fmt.Sprintf("svc-rpt-%s", strings.ToLower(cfg.Name))

	return features.New(fmt.Sprintf("Report/%s", cfg.Name)).
		WithLabel("source", cfg.Source).
		WithLabel("target", cfg.Target).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover nodes: %v", err)
			}

			if len(nodeInfo.AllNodes) < 2 && !cfg.BackendOnSameNode {
				t.Skip("Need at least 2 nodes for remote backend test")
			}

			serverNode := nodeInfo.AllNodes[0].Name
			var clientNode string
			if len(nodeInfo.AllNodes) > 1 {
				clientNode = nodeInfo.AllNodes[1].Name
			} else {
				clientNode = serverNode
			}

			// Create echo server pod
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": serverName}).
				WithContainer("echo", echoServerImage, nil).
				WithNodeAffinity(map[string]string{"kubernetes.io/hostname": serverNode})

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("Failed to create server pod: %v", err)
			}

			// Create client based on source type
			var objs []k8s.Object
			objs = append(objs, server.Pod)

			var clientNodeName string
			if cfg.BackendOnSameNode {
				clientNodeName = serverNode
			} else {
				clientNodeName = clientNode
			}

			switch cfg.Source {
			case "external-ecs":
				// No client pod needed, will use ECS executor
				executor, err := NewECSCommandExecutor()
				if err != nil {
					t.Skipf("Cannot create ECS executor: %v", err)
				}
				externalECSID := GetExternalECSInstanceID()
				externalECSIP, err := executor.GetECSPrivateIP(ctx, externalECSID)
				if err != nil {
					t.Skipf("Cannot get external ECS IP: %v", err)
				}
				ctx = context.WithValue(ctx, "executor", executor)
				ctx = context.WithValue(ctx, "externalECSID", externalECSID)
				ctx = context.WithValue(ctx, "externalECSIP", externalECSIP)

			case "internal-ecs":
				// Create host network pod
				client := NewPod(clientName, config.Namespace()).
					WithLabels(map[string]string{"app": clientName}).
					WithContainer("curl", nginxImage, nil).
					WithHostNetwork().
					WithDNSPolicy(corev1.DNSClusterFirstWithHostNet).
					WithNodeAffinity(map[string]string{"kubernetes.io/hostname": clientNodeName})

				err = config.Client().Resources().Create(ctx, client.Pod)
				if err != nil {
					t.Fatalf("Failed to create client pod: %v", err)
				}
				objs = append(objs, client.Pod)

			case "pod":
				// Create regular pod
				client := NewPod(clientName, config.Namespace()).
					WithLabels(map[string]string{"app": clientName}).
					WithContainer("curl", nginxImage, nil).
					WithNodeAffinity(map[string]string{"kubernetes.io/hostname": clientNodeName})

				err = config.Client().Resources().Create(ctx, client.Pod)
				if err != nil {
					t.Fatalf("Failed to create client pod: %v", err)
				}
				objs = append(objs, client.Pod)
			}

			// Create service based on target type
			if cfg.Target != "pod-ip" {
				svc := NewService(serviceName, config.Namespace(), map[string]string{"app": serverName}).
					ExposePort(80, "http")

				switch cfg.Target {
				case "nodeport":
					svc = svc.ExposeNodePortWithPolicy(cfg.TrafficPolicy)
				case "loadbalancer":
					svc = svc.ExposeLoadBalancerWithOptions(cfg.LoadBalancerType, cfg.TrafficPolicy)
				}

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("Failed to create service: %v", err)
				}
				objs = append(objs, svc.Service)
			}

			ctx = SaveResources(ctx, objs...)
			ctx = context.WithValue(ctx, "serverNode", serverNode)
			ctx = context.WithValue(ctx, "clientNode", clientNodeName)
			ctx = context.WithValue(ctx, "cfg", cfg)
			return ctx
		}).
		Assess("Test connectivity and record result", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			cfg := ctx.Value("cfg").(TestScenarioConfig)
			clientNodeName := ctx.Value("clientNode").(string)

			// Wait for server pod
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			err := waitPodsReady(config.Client(), server)
			if err != nil {
				t.Fatalf("Server pod not ready: %v", err)
			}

			err = config.Client().Resources().Get(ctx, serverName, config.Namespace(), server)
			if err != nil {
				t.Fatalf("Failed to get server pod: %v", err)
			}

			// Determine target address
			var targetURL string
			var expectedSourceIP string

			switch cfg.Target {
			case "pod-ip":
				targetURL = fmt.Sprintf("http://%s:80/echo", server.Status.PodIP)

			case "nodeport":
				svc := &corev1.Service{}
				err = config.Client().Resources().Get(ctx, serviceName, config.Namespace(), svc)
				if err != nil {
					t.Fatalf("Failed to get service: %v", err)
				}
				nodePort := svc.Spec.Ports[0].NodePort

				node := &corev1.Node{}
				err = config.Client().Resources().Get(ctx, clientNodeName, "", node)
				if err != nil {
					t.Fatalf("Failed to get node: %v", err)
				}
				nodeIP, _ := getNodeIPs(node, false)
				targetURL = fmt.Sprintf("http://%s:%d/echo", nodeIP, nodePort)

			case "loadbalancer":
				svc := &corev1.Service{}
				err = wait.For(conditions.New(config.Client().Resources()).ResourceMatch(&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: config.Namespace()},
				}, func(object k8s.Object) bool {
					s := object.(*corev1.Service)
					return len(s.Status.LoadBalancer.Ingress) > 0
				}), wait.WithTimeout(5*time.Minute), wait.WithInterval(5*time.Second))

				if err != nil {
					result := &ConnectivityTestResult{
						ScenarioName:    cfg.Name,
						Source:          cfg.Source,
						Target:          cfg.Target,
						ServiceType:     cfg.Target,
						TrafficPolicy:   string(cfg.TrafficPolicy),
						BackendLocation: backendLocationStr(cfg.BackendOnSameNode),
						Connected:       false,
						ErrorMessage:    "LoadBalancer IP not assigned",
					}
					report.AddResult(result)
					t.Skipf("LoadBalancer IP not assigned: %v", err)
					return ctx
				}

				err = config.Client().Resources().Get(ctx, serviceName, config.Namespace(), svc)
				if err != nil {
					t.Fatalf("Failed to get service: %v", err)
				}

				lbIP := svc.Status.LoadBalancer.Ingress[0].IP
				if lbIP == "" {
					lbIP = svc.Status.LoadBalancer.Ingress[0].Hostname
				}
				targetURL = fmt.Sprintf("http://%s:80/echo", lbIP)
			}

			// Execute connectivity test based on source type
			var output string
			var execErr error

			// Retry configuration: retry once on failure (for slow SLB backend updates)
			const maxRetries = 2
			const retryDelay = 15 * time.Second

			var result *ConnectivityTestResult

			for attempt := 1; attempt <= maxRetries; attempt++ {
				execErr = nil
				output = ""

				switch cfg.Source {
				case "external-ecs":
					executor := ctx.Value("executor").(*ECSCommandExecutor)
					externalECSID := ctx.Value("externalECSID").(string)
					expectedSourceIP = ctx.Value("externalECSIP").(string)

					cmdResult, err := executor.RunCurlCommand(ctx, externalECSID, targetURL, 60*time.Second)
					if err != nil {
						execErr = fmt.Errorf("RunCommand API error: %v", err)
					} else if !cmdResult.Success {
						if cmdResult.Output != "" {
							execErr = fmt.Errorf("exit_code=%d, error_code=%s, output: %s", cmdResult.ExitCode, cmdResult.ErrorCode, cmdResult.Output)
						} else {
							execErr = fmt.Errorf("exit_code=%d, error_code=%s (no output)", cmdResult.ExitCode, cmdResult.ErrorCode)
						}
					} else {
						output = cmdResult.Output
					}

				case "internal-ecs", "pod":
					client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}
					err = waitPodsReady(config.Client(), client)
					if err != nil {
						t.Fatalf("Client pod not ready: %v", err)
					}

					err = config.Client().Resources().Get(ctx, clientName, config.Namespace(), client)
					if err != nil {
						t.Fatalf("Failed to get client pod: %v", err)
					}

					if cfg.Source == "internal-ecs" {
						node := &corev1.Node{}
						err = config.Client().Resources().Get(ctx, client.Spec.NodeName, "", node)
						if err != nil {
							t.Fatalf("Failed to get node: %v", err)
						}
						expectedSourceIP, _ = getNodeIPs(node, false)
					} else {
						expectedSourceIP = client.Status.PodIP
					}

					var stdout, stderr bytes.Buffer
					cmd := []string{"curl", "-s", "-m", "10", targetURL}
					execErr = config.Client().Resources().ExecInPod(ctx, config.Namespace(), clientName, "curl", cmd, &stdout, &stderr)
					output = stdout.String()
					if execErr != nil {
						execErr = fmt.Errorf("curl failed: %v, stderr: %s", execErr, stderr.String())
					}
				}

				// Build result for this attempt
				result = &ConnectivityTestResult{
					ScenarioName:     cfg.Name,
					Source:           cfg.Source,
					Target:           cfg.Target,
					ServiceType:      cfg.Target,
					TrafficPolicy:    string(cfg.TrafficPolicy),
					BackendLocation:  backendLocationStr(cfg.BackendOnSameNode),
					LoadBalancerType: cfg.LoadBalancerType,
					ExpectedSourceIP: expectedSourceIP,
				}

				if execErr != nil {
					result.Connected = false
					result.ErrorMessage = execErr.Error()
				} else {
					echoResp, parseErr := ParseEchoResponse(output)
					if parseErr != nil {
						result.Connected = false
						result.ErrorMessage = fmt.Sprintf("Failed to parse response: %v", parseErr)
					} else {
						result.Connected = true
						result.ObservedSourceIP = echoResp.GetSourceIP()
						result.SourceIPPreserved = result.ObservedSourceIP == expectedSourceIP
						result.RawResponse = output
					}
				}

				// If succeeded or last attempt, break
				if result.Connected || attempt == maxRetries {
					if !result.Connected && attempt > 1 {
						result.ErrorMessage = fmt.Sprintf("[after %d attempts] %s", attempt, result.ErrorMessage)
					}
					break
				}

				// Retry: wait before next attempt (for SLB backend updates)
				t.Logf("[%s] Attempt %d failed, retrying in %v... (error: %s)", cfg.Name, attempt, retryDelay, result.ErrorMessage)
				time.Sleep(retryDelay)
			}

			report.AddResult(result)
			t.Log(result.String())

			return MarkTestSuccess(ctx)
		}).
		Feature()
}

func backendLocationStr(onSameNode bool) string {
	if onSameNode {
		return "Local"
	}
	return "Remote"
}
