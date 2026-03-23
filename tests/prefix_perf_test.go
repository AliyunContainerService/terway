//go:build e2e

package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/tests/utils"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const (
	// prefixPerfTestPrefixCount is the number of IP Prefixes to configure.
	// Each /28 prefix provides 16 IPs, so 10 prefixes = 160 available IPs.
	prefixPerfTestPrefixCount = 10

	// prefixPerfTestDeploymentCount is the number of deployments to create.
	prefixPerfTestDeploymentCount = 10

	// prefixPerfTestPodsPerDeploy is the number of pods per deployment.
	prefixPerfTestPodsPerDeploy = 10

	// prefixPerfTestTotalPods is the total number of pods (10 x 10 = 100).
	prefixPerfTestTotalPods = prefixPerfTestDeploymentCount * prefixPerfTestPodsPerDeploy
)

// PrefixIPAllocationPerfTestSuite runs the IP Prefix allocation performance test.
// It configures 10 IP Prefixes on a qualified node, then starts 100 pods and
// measures IP allocation latency (P99, P90, Max, Min, Avg).
type PrefixIPAllocationPerfTestSuite struct {
	NodeName           string
	Deployments        []*appsv1.Deployment
	LatencyStats       utils.LatencyStats
	AllocIPFailedCount int

	IterationStats       []utils.LatencyStats
	IterationFailedCount []int
}

// NewPrefixIPAllocationPerfTestSuite creates a new test suite.
func NewPrefixIPAllocationPerfTestSuite() *PrefixIPAllocationPerfTestSuite {
	return &PrefixIPAllocationPerfTestSuite{}
}

// Setup discovers a qualified IP Prefix node, configures 10 prefixes, restarts
// Terway, and waits for the prefixes to be allocated before the test runs.
func (s *PrefixIPAllocationPerfTestSuite) Setup(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, config.Client())
	if err != nil {
		t.Fatalf("failed to discover node types with capacity: %v", err)
	}

	// Find a qualified IP Prefix node. Each /28 prefix holds 16 IPs, so
	// 10 prefixes require IPv4PerAdapter >= 11 (1 primary + 10 prefix slots).
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		if cap.IPv4PerAdapter >= 11 {
			s.NodeName = nodeName
			break
		}
	}

	if s.NodeName == "" {
		t.Skip("No qualified IP Prefix node found (need NodeTypeECSIPPrefix with IPv4PerAdapter >= 11)")
		return ctx
	}
	t.Logf("Selected IP Prefix node: %s", s.NodeName)

	// Configure 10 IP Prefixes on the node via the shared dynamic ConfigMap.
	t.Logf("Configuring ipv4_prefix_count=%d on node %s", prefixPerfTestPrefixCount, s.NodeName)
	if err := configureIPPrefixCount(ctx, t, config, s.NodeName, prefixPerfTestPrefixCount); err != nil {
		t.Fatalf("failed to configure ipv4_prefix_count: %v", err)
	}

	// Restart Terway so it picks up the new prefix count immediately.
	t.Log("Restarting Terway to apply prefix configuration")
	if err := restartTerway(ctx, config); err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for all 10 prefixes to be allocated and in Valid state.
	t.Logf("Waiting for %d prefixes to be allocated on node %s", prefixPerfTestPrefixCount, s.NodeName)
	if err := waitForPrefixAllocation(ctx, config, t, s.NodeName, prefixPerfTestPrefixCount, 5*time.Minute); err != nil {
		t.Fatalf("prefixes not allocated in time: %v", err)
	}

	t.Logf("Setup complete: %d prefixes ready on node %s", prefixPerfTestPrefixCount, s.NodeName)
	return ctx
}

// WaitForPodsReady waits for all test deployments to become available.
func (s *PrefixIPAllocationPerfTestSuite) WaitForPodsReady(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Logf("Waiting for %d pods to be ready...", prefixPerfTestTotalPods)

	for _, deploy := range s.Deployments {
		err := wait.For(conditions.New(config.Client().Resources()).DeploymentConditionMatch(
			deploy, appsv1.DeploymentAvailable, corev1.ConditionTrue),
			wait.WithTimeout(10*time.Minute),
			wait.WithInterval(5*time.Second))
		if err != nil {
			t.Fatalf("failed waiting for deployment %s to be ready: %v", deploy.Name, err)
		}
		t.Logf("Deployment %s is ready", deploy.Name)
	}

	return ctx
}

// CollectLatencies collects AllocIPSucceed / AllocIPFailed events from the current
// test run's pods and computes latency statistics.
func (s *PrefixIPAllocationPerfTestSuite) CollectLatencies(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("Collecting AllocIPSucceed and AllocIPFailed events...")

	// Build a set of pod UIDs belonging to our deployments.
	podUIDs := make(map[string]bool)
	for _, deploy := range s.Deployments {
		pods := &corev1.PodList{}
		if err := config.Client().Resources(config.Namespace()).List(ctx, pods); err != nil {
			t.Fatalf("failed to list pods: %v", err)
		}
		for _, pod := range pods.Items {
			if strings.HasPrefix(pod.Name, deploy.Name) {
				podUIDs[string(pod.UID)] = true
			}
		}
	}
	t.Logf("Found %d pods from current deployments", len(podUIDs))

	events := &corev1.EventList{}
	if err := config.Client().Resources(config.Namespace()).List(ctx, events); err != nil {
		t.Fatalf("failed to list events: %v", err)
	}

	var latencies []time.Duration
	s.AllocIPFailedCount = 0

	for _, event := range events.Items {
		if !podUIDs[string(event.InvolvedObject.UID)] {
			continue
		}
		switch event.Reason {
		case "AllocIPSucceed":
			latency, err := utils.ParseAllocIPSucceedLatency(event.Message)
			if err != nil {
				t.Logf("Warning: failed to parse latency from event: %v", err)
				continue
			}
			latencies = append(latencies, latency)
		case "AllocIPFailed":
			s.AllocIPFailedCount++
			t.Logf("AllocIPFailed event: pod=%s, message=%s", event.InvolvedObject.Name, event.Message)
		}
	}

	if len(latencies) == 0 {
		t.Error("No AllocIPSucceed events found")
		return ctx
	}

	s.LatencyStats = utils.CalculateLatencyStats(latencies)
	t.Logf("IP Allocation Latency Stats (prefix, %d pods): %s (AllocIPFailed: %d)",
		prefixPerfTestTotalPods, s.LatencyStats.String(), s.AllocIPFailedCount)

	return ctx
}

// ScaleDown scales all test deployments to zero replicas and waits for pods to terminate.
func (s *PrefixIPAllocationPerfTestSuite) ScaleDown(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("Scaling down deployments...")

	for _, deploy := range s.Deployments {
		if err := config.Client().Resources().Get(ctx, deploy.Name, deploy.Namespace, deploy); err != nil {
			t.Logf("Warning: failed to get deployment %s: %v", deploy.Name, err)
			continue
		}
		zero := int32(0)
		deploy.Spec.Replicas = &zero
		if err := config.Client().Resources().Update(ctx, deploy); err != nil {
			t.Logf("Warning: failed to scale down deployment %s: %v", deploy.Name, err)
		}
	}

	for _, deploy := range s.Deployments {
		err := wait.For(conditions.New(config.Client().Resources()).ResourceScaled(
			deploy, func(object k8s.Object) int32 {
				return object.(*appsv1.Deployment).Status.Replicas
			}, 0),
			wait.WithTimeout(5*time.Minute),
			wait.WithInterval(5*time.Second))
		if err != nil {
			t.Logf("Warning: failed waiting for deployment %s to scale down: %v", deploy.Name, err)
		}
	}

	t.Log("All deployments scaled down")
	return ctx
}

// PrintResults prints the final performance statistics.
func (s *PrefixIPAllocationPerfTestSuite) PrintResults(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("========================================")
	t.Log("IP Prefix Allocation Performance Test Results")
	t.Logf("Node:            %s", s.NodeName)
	t.Logf("Prefix count:    %d", prefixPerfTestPrefixCount)
	t.Logf("Total pods:      %d (%d deployments x %d pods)",
		prefixPerfTestTotalPods, prefixPerfTestDeploymentCount, prefixPerfTestPodsPerDeploy)
	t.Log("----------------------------------------")
	t.Logf("Samples:         %d", s.LatencyStats.N)
	t.Logf("P99:             %v", s.LatencyStats.P99)
	t.Logf("P90:             %v", s.LatencyStats.P90)
	t.Logf("Max:             %v", s.LatencyStats.Max)
	t.Logf("Min:             %v", s.LatencyStats.Min)
	t.Logf("Avg:             %v", s.LatencyStats.Avg)
	t.Logf("AllocIPFailed:   %d", s.AllocIPFailedCount)
	t.Log("========================================")

	return MarkTestSuccess(ctx)
}

// RunIteration runs one full cycle: create 100 pods → wait ready → collect
// latencies → scale down → delete deployments.
func (s *PrefixIPAllocationPerfTestSuite) RunIteration(ctx context.Context, t *testing.T, config *envconf.Config, iteration int) context.Context {
	t.Logf("=== Starting iteration %d ===", iteration+1)

	// Pin all pods to the specific node that has 10 prefixes configured.
	nodeAffinityLabels := map[string]string{"kubernetes.io/hostname": s.NodeName}

	// The first deployment acts as the affinity anchor so all deployments land
	// on the same node (matching the pattern used in ip_allocation_perf_test.go).
	anchorDeployName := fmt.Sprintf("prefix-perf-%d-0", iteration)
	anchorPodLabel := map[string]string{"app": anchorDeployName}

	s.Deployments = make([]*appsv1.Deployment, prefixPerfTestDeploymentCount)
	for i := 0; i < prefixPerfTestDeploymentCount; i++ {
		name := fmt.Sprintf("prefix-perf-%d-%d", iteration, i)
		deploy := utils.NewDeployment(name, config.Namespace(), int32(prefixPerfTestPodsPerDeploy)).
			WithNodeAffinity(nodeAffinityLabels).
			WithPodAffinity(anchorPodLabel)

		if err := config.Client().Resources().Create(ctx, deploy.Deployment); err != nil {
			t.Fatalf("failed to create deployment %s: %v", name, err)
		}
		s.Deployments[i] = deploy.Deployment
	}
	t.Logf("Created %d deployments (%d pods total)", prefixPerfTestDeploymentCount, prefixPerfTestTotalPods)

	ctx = s.WaitForPodsReady(ctx, t, config)
	ctx = s.CollectLatencies(ctx, t, config)

	s.IterationStats = append(s.IterationStats, s.LatencyStats)
	s.IterationFailedCount = append(s.IterationFailedCount, s.AllocIPFailedCount)

	ctx = s.ScaleDown(ctx, t, config)

	for _, deploy := range s.Deployments {
		if err := config.Client().Resources().Delete(ctx, deploy); err != nil {
			t.Logf("Warning: failed to delete deployment %s: %v", deploy.Name, err)
		}
	}

	// Brief pause to let pod IPs be fully released back to the prefix pool
	// before the next iteration (or teardown).
	time.Sleep(5 * time.Second)

	t.Logf("=== Completed iteration %d ===", iteration+1)
	return ctx
}

// RunAllIterations runs the single test iteration and stores results.
func (s *PrefixIPAllocationPerfTestSuite) RunAllIterations(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	s.IterationStats = make([]utils.LatencyStats, 0, 1)
	s.IterationFailedCount = make([]int, 0, 1)
	return s.RunIteration(ctx, t, config, 0)
}

// RestoreConfig resets the prefix count to 0 and cleans up prefix state on the node.
func (s *PrefixIPAllocationPerfTestSuite) RestoreConfig(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	if s.NodeName == "" {
		return ctx
	}
	t.Logf("Resetting prefix state on node %s", s.NodeName)
	if err := resetNodePrefixState(ctx, config, t, s.NodeName, 5*time.Minute); err != nil {
		t.Logf("Warning: resetNodePrefixState failed: %v", err)
	}
	return ctx
}

// TestIPPrefixAllocationPerf is the entry point for the IP Prefix allocation
// performance test. It configures 10 prefixes on a qualified IP Prefix node,
// then starts 100 pods (10 deployments × 10 pods) and reports allocation
// latency statistics (P99 / P90 / Max / Min / Avg).
func TestIPPrefixAllocationPerf(t *testing.T) {
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		ipamType := ""
		if eniConfig != nil {
			ipamType = eniConfig.IPAMType
		}
		t.Skipf("skip: ipam type is not crd, current type: %s", ipamType)
		return
	}

	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("TestIPPrefixAllocationPerf requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
		return
	}

	if !RequireTerwayVersion("v1.17.0") {
		t.Skipf("Requires terway version >= v1.17.0")
		return
	}

	suite := NewPrefixIPAllocationPerfTestSuite()

	feature := features.New("IPPrefixAllocationPerf").
		WithLabel("env", "performance").
		WithLabel("feature", "ip-prefix").
		Setup(suite.Setup).
		Assess("run performance iteration", suite.RunAllIterations).
		Assess("print results", suite.PrintResults).
		Teardown(suite.RestoreConfig).
		Feature()

	testenv.Test(t, feature)
}
