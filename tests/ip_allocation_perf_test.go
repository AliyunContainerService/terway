//go:build e2e

package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/tests/utils"
	"github.com/Jeffail/gabs/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const (
	// Test configuration
	perfTestDeploymentCount = 5
	perfTestPodsPerDeploy   = 6
	perfTestTotalPods       = perfTestDeploymentCount * perfTestPodsPerDeploy
	perfTestIterations      = 1 // Number of test iterations for averaging
)

// PoolConfig represents the IP pool configuration
type PoolConfig struct {
	Name        string
	MinPoolSize int
	MaxPoolSize int
}

var (
	// Pool configurations to test
	defaultPoolConfig = PoolConfig{Name: "default", MinPoolSize: 0, MaxPoolSize: 5}
	warmUpPoolConfig  = PoolConfig{Name: "warmup", MinPoolSize: 30, MaxPoolSize: 30}
)

// IPAllocationPerfTestSuite runs the IP allocation performance test
type IPAllocationPerfTestSuite struct {
	NodeType                  NodeType
	PoolConfig                PoolConfig
	Deployments               []*appsv1.Deployment
	LatencyStats              utils.LatencyStats
	OriginalEnablePatchPodIPs *bool // nil means not set in original config

	// Per-iteration results for averaging
	IterationStats       []utils.LatencyStats
	IterationFailedCount []int
	AllocIPFailedCount   int // Failed IP allocation count for current iteration
}

// NewIPAllocationPerfTestSuite creates a new test suite
func NewIPAllocationPerfTestSuite(nodeType NodeType, poolConfig PoolConfig) *IPAllocationPerfTestSuite {
	return &IPAllocationPerfTestSuite{
		NodeType:   nodeType,
		PoolConfig: poolConfig,
	}
}

// configurePoolSize configures the IP pool size and disable enable_patch_pod_ips in eni-config
func (s *IPAllocationPerfTestSuite) configurePoolSize(ctx context.Context, t *testing.T, config *envconf.Config) error {
	t.Logf("Configuring pool size: min_pool_size=%d, max_pool_size=%d, enable_patch_pod_ips=false",
		s.PoolConfig.MinPoolSize, s.PoolConfig.MaxPoolSize)

	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return err
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		return err
	}

	// Save original enable_patch_pod_ips value
	if eniJson.Exists("enable_patch_pod_ips") {
		originalValue := eniJson.Path("enable_patch_pod_ips").Data().(bool)
		s.OriginalEnablePatchPodIPs = &originalValue
		t.Logf("Original enable_patch_pod_ips: %v", originalValue)
	}

	_, err = eniJson.Set(s.PoolConfig.MaxPoolSize, "max_pool_size")
	if err != nil {
		return err
	}
	_, err = eniJson.Set(s.PoolConfig.MinPoolSize, "min_pool_size")
	if err != nil {
		return err
	}
	// Disable enable_patch_pod_ips for performance test
	_, err = eniJson.Set(false, "enable_patch_pod_ips")
	if err != nil {
		return err
	}

	cm.Data["eni_conf"] = eniJson.String()
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		return err
	}

	// Restart terway to apply new config
	return restartTerway(ctx, config)
}

// Setup performs initial checks and configures the pool size
func (s *IPAllocationPerfTestSuite) Setup(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	// Check if the required node type is available
	nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
	if err != nil {
		t.Fatalf("failed to discover node types: %v", err)
	}

	nodes := nodeInfo.GetNodesByType(s.NodeType)
	if len(nodes) == 0 {
		t.Skipf("No nodes of type %s found, skipping test", s.NodeType)
		return ctx
	}
	t.Logf("Found %d nodes of type %s", len(nodes), s.NodeType)

	// Configure pool size
	err = s.configurePoolSize(ctx, t, config)
	if err != nil {
		t.Fatalf("failed to configure pool size: %v", err)
	}

	// Wait for terway to be ready after config change
	time.Sleep(10 * time.Second)

	return ctx
}

// WaitForPodsReady waits for all pods to be ready
func (s *IPAllocationPerfTestSuite) WaitForPodsReady(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Logf("Waiting for %d pods to be ready...", perfTestTotalPods)

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

// CollectLatencies collects IP allocation latencies from pod events
func (s *IPAllocationPerfTestSuite) CollectLatencies(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("Collecting AllocIPSucceed and AllocIPFailed events...")

	// First, collect all current pod UIDs from our deployments
	// This ensures we only count events from the current test run's pods
	podUIDs := make(map[string]bool)
	for _, deploy := range s.Deployments {
		pods := &corev1.PodList{}
		err := config.Client().Resources(config.Namespace()).List(ctx, pods)
		if err != nil {
			t.Fatalf("failed to list pods: %v", err)
		}
		for _, pod := range pods.Items {
			if strings.HasPrefix(pod.Name, deploy.Name) {
				podUIDs[string(pod.UID)] = true
			}
		}
	}
	t.Logf("Found %d pods from current deployments", len(podUIDs))

	// Get all events in the namespace
	events := &corev1.EventList{}
	err := config.Client().Resources(config.Namespace()).List(ctx, events)
	if err != nil {
		t.Fatalf("failed to list events: %v", err)
	}

	var latencies []time.Duration
	s.AllocIPFailedCount = 0

	for _, event := range events.Items {
		// Check if this event is for one of our current pods by UID
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
	t.Logf("IP Allocation Latency Stats for %s/%s: %s (AllocIPFailed: %d)",
		s.NodeType, s.PoolConfig.Name, s.LatencyStats.String(), s.AllocIPFailedCount)

	return ctx
}

// ScaleDown scales down all deployments to 0
func (s *IPAllocationPerfTestSuite) ScaleDown(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("Scaling down deployments...")

	for _, deploy := range s.Deployments {
		// Refresh the deployment
		err := config.Client().Resources().Get(ctx, deploy.Name, deploy.Namespace, deploy)
		if err != nil {
			t.Logf("Warning: failed to get deployment %s: %v", deploy.Name, err)
			continue
		}

		zero := int32(0)
		deploy.Spec.Replicas = &zero
		err = config.Client().Resources().Update(ctx, deploy)
		if err != nil {
			t.Logf("Warning: failed to scale down deployment %s: %v", deploy.Name, err)
		}
	}

	// Wait for all pods to be deleted
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

// PrintResults prints the final test results
func (s *IPAllocationPerfTestSuite) PrintResults(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("========================================")
	t.Logf("IP Allocation Performance Test Results")
	t.Logf("Node Type: %s", s.NodeType)
	t.Logf("Pool Config: %s (min=%d, max=%d)", s.PoolConfig.Name, s.PoolConfig.MinPoolSize, s.PoolConfig.MaxPoolSize)
	t.Logf("Deployments: %d x %d pods = %d total pods", perfTestDeploymentCount, perfTestPodsPerDeploy, perfTestTotalPods)
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

// RunIteration runs a single iteration of create -> wait -> collect -> scale down
func (s *IPAllocationPerfTestSuite) RunIteration(ctx context.Context, t *testing.T, config *envconf.Config, iteration int) context.Context {
	t.Logf("=== Starting iteration %d/%d ===", iteration+1, perfTestIterations)

	// Get node affinity labels based on node type
	affinityLabels := GetNodeAffinityForType(s.NodeType)
	excludeLabels := GetNodeAffinityExcludeForType(s.NodeType)

	// Define the anchor deployment name and label for pod affinity
	anchorDeployName := fmt.Sprintf("perf-test-%s-%s-0", s.NodeType, s.PoolConfig.Name)
	anchorPodLabel := map[string]string{"app": anchorDeployName}

	// Create deployments
	s.Deployments = make([]*appsv1.Deployment, perfTestDeploymentCount)
	for i := 0; i < perfTestDeploymentCount; i++ {
		name := fmt.Sprintf("perf-test-%s-%s-%d", s.NodeType, s.PoolConfig.Name, i)
		deploy := utils.NewDeployment(name, config.Namespace(), int32(perfTestPodsPerDeploy)).
			WithNodeAffinity(affinityLabels).
			WithNodeAffinityExclude(excludeLabels).
			WithPodAffinity(anchorPodLabel)

		if utils.IsLingjunNodeType(string(s.NodeType)) {
			deploy = deploy.WithLingjunToleration()
		}

		err := config.Client().Resources().Create(ctx, deploy.Deployment)
		if err != nil {
			t.Fatalf("failed to create deployment %s: %v", name, err)
		}
		s.Deployments[i] = deploy.Deployment
	}
	t.Logf("Created %d deployments for iteration %d", perfTestDeploymentCount, iteration+1)

	// Wait for pods ready
	ctx = s.WaitForPodsReady(ctx, t, config)

	// Collect latencies
	ctx = s.CollectLatencies(ctx, t, config)

	// Store iteration results
	s.IterationStats = append(s.IterationStats, s.LatencyStats)
	s.IterationFailedCount = append(s.IterationFailedCount, s.AllocIPFailedCount)

	// Scale down and delete deployments
	ctx = s.ScaleDown(ctx, t, config)

	// Delete deployments to clean up for next iteration
	for _, deploy := range s.Deployments {
		err := config.Client().Resources().Delete(ctx, deploy)
		if err != nil {
			t.Logf("Warning: failed to delete deployment %s: %v", deploy.Name, err)
		}
	}

	// Wait a bit for cleanup
	time.Sleep(5 * time.Second)

	t.Logf("=== Completed iteration %d/%d ===", iteration+1, perfTestIterations)
	return ctx
}

// RunAllIterations runs all test iterations and calculates averaged results
func (s *IPAllocationPerfTestSuite) RunAllIterations(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	s.IterationStats = make([]utils.LatencyStats, 0, perfTestIterations)
	s.IterationFailedCount = make([]int, 0, perfTestIterations)

	for i := 0; i < perfTestIterations; i++ {
		ctx = s.RunIteration(ctx, t, config, i)
	}

	// Calculate averaged results
	s.calculateAveragedResults(t)

	return ctx
}

// calculateAveragedResults calculates averaged statistics from all iterations
func (s *IPAllocationPerfTestSuite) calculateAveragedResults(t *testing.T) {
	if len(s.IterationStats) == 0 {
		return
	}

	var totalP99, totalP90, totalMax, totalMin, totalAvg time.Duration
	var totalN, totalFailed int

	for i, stats := range s.IterationStats {
		totalP99 += stats.P99
		totalP90 += stats.P90
		totalMax += stats.Max
		totalMin += stats.Min
		totalAvg += stats.Avg
		totalN += stats.N
		totalFailed += s.IterationFailedCount[i]
	}

	n := len(s.IterationStats)
	s.LatencyStats = utils.LatencyStats{
		N:   totalN / n,
		P99: totalP99 / time.Duration(n),
		P90: totalP90 / time.Duration(n),
		Max: totalMax / time.Duration(n),
		Min: totalMin / time.Duration(n),
		Avg: totalAvg / time.Duration(n),
	}
	s.AllocIPFailedCount = totalFailed

	t.Log("----------------------------------------")
	t.Logf("Averaged results from %d iterations:", n)
	for i, stats := range s.IterationStats {
		t.Logf("  Iteration %d: P99=%v, P90=%v, Avg=%v, Failed=%d",
			i+1, stats.P99, stats.P90, stats.Avg, s.IterationFailedCount[i])
	}
}

// PrintAveragedResults prints the averaged results from all iterations
func (s *IPAllocationPerfTestSuite) PrintAveragedResults(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("========================================")
	t.Logf("IP Allocation Performance Test Results (Averaged over %d iterations)", perfTestIterations)
	t.Logf("Node Type: %s", s.NodeType)
	t.Logf("Pool Config: %s (min=%d, max=%d)", s.PoolConfig.Name, s.PoolConfig.MinPoolSize, s.PoolConfig.MaxPoolSize)
	t.Logf("Deployments per iteration: %d x %d pods = %d total pods", perfTestDeploymentCount, perfTestPodsPerDeploy, perfTestTotalPods)
	t.Log("----------------------------------------")
	t.Logf("Avg Samples:     %d", s.LatencyStats.N)
	t.Logf("Avg P99:         %v", s.LatencyStats.P99)
	t.Logf("Avg P90:         %v", s.LatencyStats.P90)
	t.Logf("Avg Max:         %v", s.LatencyStats.Max)
	t.Logf("Avg Min:         %v", s.LatencyStats.Min)
	t.Logf("Avg Latency:     %v", s.LatencyStats.Avg)
	t.Logf("Total AllocIPFailed: %d", s.AllocIPFailedCount)
	t.Log("========================================")

	return MarkTestSuccess(ctx)
}

// RestoreConfig restores the default pool configuration and enable_patch_pod_ips
func (s *IPAllocationPerfTestSuite) RestoreConfig(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("Restoring default pool configuration and enable_patch_pod_ips...")

	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		t.Logf("Warning: failed to get eni-config: %v", err)
		return ctx
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		t.Logf("Warning: failed to parse eni_conf: %v", err)
		return ctx
	}

	// Restore default pool values: min_pool_size=0, max_pool_size=5
	_, _ = eniJson.Set(5, "max_pool_size")
	_, _ = eniJson.Set(0, "min_pool_size")

	// Restore enable_patch_pod_ips to original value
	if s.OriginalEnablePatchPodIPs != nil {
		_, _ = eniJson.Set(*s.OriginalEnablePatchPodIPs, "enable_patch_pod_ips")
		t.Logf("Restored enable_patch_pod_ips to: %v", *s.OriginalEnablePatchPodIPs)
	} else {
		// If it wasn't set originally, delete it
		_ = eniJson.Delete("enable_patch_pod_ips")
		t.Log("Removed enable_patch_pod_ips (was not set originally)")
	}

	cm.Data["eni_conf"] = eniJson.String()
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		t.Logf("Warning: failed to update eni-config: %v", err)
	}

	return ctx
}

// TestIPAllocationPerf tests IP allocation performance
// This test creates 5 deployments with 30 pods each, measures IP allocation latency,
// and reports P99, P90, Max, Min statistics.
// Tests are run on both ECS and Lingjun shared nodes with two pool configurations:
// - Default: min_pool_size=0, max_pool_size=5
// - Warm-up: min_pool_size=30, max_pool_size=30
func TestIPAllocationPerf(t *testing.T) {
	// Pre-check: only test centralized IPAM mode
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		ipamType := ""
		if eniConfig != nil {
			ipamType = eniConfig.IPAMType
		}
		t.Skipf("skip: ipam type is not crd, current type: %s", ipamType)
		return
	}

	// Pre-check: terway daemonset name must be terway-eniip
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("TestIPAllocationPerf requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
		return
	}

	// Run ECS tests
	t.Run("ECS", func(t *testing.T) {
		// Test with default pool config
		t.Run("DefaultPool", func(t *testing.T) {
			runIPAllocationPerfTest(t, NodeTypeECSSharedENI, defaultPoolConfig)
		})

		// Test with warm-up pool config
		t.Run("WarmUpPool", func(t *testing.T) {
			runIPAllocationPerfTest(t, NodeTypeECSSharedENI, warmUpPoolConfig)
		})
	})

	// Run Lingjun tests
	t.Run("Lingjun", func(t *testing.T) {
		// Test with default pool config
		t.Run("DefaultPool", func(t *testing.T) {
			runIPAllocationPerfTest(t, NodeTypeLingjunSharedENI, defaultPoolConfig)
		})

		// Test with warm-up pool config
		t.Run("WarmUpPool", func(t *testing.T) {
			runIPAllocationPerfTest(t, NodeTypeLingjunSharedENI, warmUpPoolConfig)
		})
	})
}

// TestIPAllocationPerfECS tests IP allocation performance specifically for ECS shared ENI nodes
func TestIPAllocationPerfECS(t *testing.T) {
	// Pre-check: only test centralized IPAM mode
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		ipamType := ""
		if eniConfig != nil {
			ipamType = eniConfig.IPAMType
		}
		t.Skipf("skip: ipam type is not crd, current type: %s", ipamType)
		return
	}

	// Pre-check: terway daemonset name must be terway-eniip
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("TestIPAllocationPerfECS requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
		return
	}

	// Test with default pool config
	t.Run("DefaultPool", func(t *testing.T) {
		runIPAllocationPerfTest(t, NodeTypeECSSharedENI, defaultPoolConfig)
	})

	// Test with warm-up pool config
	t.Run("WarmUpPool", func(t *testing.T) {
		runIPAllocationPerfTest(t, NodeTypeECSSharedENI, warmUpPoolConfig)
	})
}

// TestIPAllocationPerfLingjun tests IP allocation performance specifically for Lingjun shared ENI nodes
func TestIPAllocationPerfLingjun(t *testing.T) {
	// Pre-check: only test centralized IPAM mode
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		ipamType := ""
		if eniConfig != nil {
			ipamType = eniConfig.IPAMType
		}
		t.Skipf("skip: ipam type is not crd, current type: %s", ipamType)
		return
	}

	// Pre-check: terway daemonset name must be terway-eniip
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("TestIPAllocationPerfLingjun requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
		return
	}

	// Test with default pool config
	t.Run("DefaultPool", func(t *testing.T) {
		runIPAllocationPerfTest(t, NodeTypeLingjunSharedENI, defaultPoolConfig)
	})

	// Test with warm-up pool config
	t.Run("WarmUpPool", func(t *testing.T) {
		runIPAllocationPerfTest(t, NodeTypeLingjunSharedENI, warmUpPoolConfig)
	})
}

// runIPAllocationPerfTest runs IP allocation performance test with multiple iterations
func runIPAllocationPerfTest(t *testing.T, nodeType NodeType, poolConfig PoolConfig) {
	suite := NewIPAllocationPerfTestSuite(nodeType, poolConfig)

	feature := features.New(fmt.Sprintf("IPAllocationPerf/%s-%s", nodeType, poolConfig.Name)).
		WithLabel("env", "performance").
		Setup(suite.Setup).
		Assess("run all iterations", suite.RunAllIterations).
		Assess("print averaged results", suite.PrintAveragedResults).
		Teardown(suite.RestoreConfig).
		Feature()

	testenv.Test(t, feature)
}
