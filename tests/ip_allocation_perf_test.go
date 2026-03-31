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

// =============================================================================
// Configuration
// =============================================================================

// PerfTestConfig describes both pool-based (shared ENI) and prefix-based test parameters.
type PerfTestConfig struct {
	Name string

	// Pool mode (shared ENI): min_pool_size / max_pool_size in eni-config.
	MinPoolSize int
	MaxPoolSize int

	// Prefix mode (IP Prefix): number of /28 prefixes to pre-allocate on the node.
	PrefixCount int

	DeploymentCount int
	PodsPerDeploy   int
}

func (c PerfTestConfig) TotalPods() int { return c.DeploymentCount * c.PodsPerDeploy }
func (c PerfTestConfig) IsPrefix() bool { return c.PrefixCount > 0 }

var (
	defaultPoolConfig = PerfTestConfig{
		Name: "default-pool", MinPoolSize: 0, MaxPoolSize: 5,
		DeploymentCount: 5, PodsPerDeploy: 6,
	}
	warmUpPoolConfig = PerfTestConfig{
		Name: "warmup-pool", MinPoolSize: 30, MaxPoolSize: 30,
		DeploymentCount: 5, PodsPerDeploy: 6,
	}
	prefixPerfConfig = PerfTestConfig{
		Name: "10-prefixes", PrefixCount: 10,
		DeploymentCount: 10, PodsPerDeploy: 10,
	}
)

// =============================================================================
// Pool config lifecycle (apply once, run multiple node-type tests in parallel)
// =============================================================================

// poolConfigManager applies and restores pool settings in eni-config.
// It is used at the parent test level so that parallel child tests (ECS, Lingjun)
// share a single config change + terway restart.
type poolConfigManager struct {
	config                    PerfTestConfig
	originalEnablePatchPodIPs *bool
}

func (m *poolConfigManager) apply(ctx context.Context, t *testing.T, envCfg *envconf.Config) {
	t.Helper()
	t.Logf("Applying pool config %s: min=%d, max=%d, enable_patch_pod_ips=false",
		m.config.Name, m.config.MinPoolSize, m.config.MaxPoolSize)

	cm := &corev1.ConfigMap{}
	if err := envCfg.Client().Resources().Get(ctx, "eni-config", "kube-system", cm); err != nil {
		t.Fatalf("failed to get eni-config: %v", err)
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		t.Fatalf("failed to parse eni_conf: %v", err)
	}

	if eniJson.Exists("enable_patch_pod_ips") {
		v := eniJson.Path("enable_patch_pod_ips").Data().(bool)
		m.originalEnablePatchPodIPs = &v
	}

	_, _ = eniJson.Set(m.config.MaxPoolSize, "max_pool_size")
	_, _ = eniJson.Set(m.config.MinPoolSize, "min_pool_size")
	_, _ = eniJson.Set(false, "enable_patch_pod_ips")

	cm.Data["eni_conf"] = eniJson.String()
	if err := envCfg.Client().Resources().Update(ctx, cm); err != nil {
		t.Fatalf("failed to update eni-config: %v", err)
	}

	if err := restartTerway(ctx, envCfg); err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	time.Sleep(10 * time.Second)
}

func (m *poolConfigManager) restore(ctx context.Context, t *testing.T, envCfg *envconf.Config) {
	t.Helper()
	t.Log("Restoring pool configuration...")

	cm := &corev1.ConfigMap{}
	if err := envCfg.Client().Resources().Get(ctx, "eni-config", "kube-system", cm); err != nil {
		t.Logf("Warning: failed to get eni-config: %v", err)
		return
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		t.Logf("Warning: failed to parse eni_conf: %v", err)
		return
	}

	_, _ = eniJson.Set(5, "max_pool_size")
	_, _ = eniJson.Set(0, "min_pool_size")

	if m.originalEnablePatchPodIPs != nil {
		_, _ = eniJson.Set(*m.originalEnablePatchPodIPs, "enable_patch_pod_ips")
	} else {
		_ = eniJson.Delete("enable_patch_pod_ips")
	}

	cm.Data["eni_conf"] = eniJson.String()
	if err := envCfg.Client().Resources().Update(ctx, cm); err != nil {
		t.Logf("Warning: failed to update eni-config: %v", err)
	}
}

// =============================================================================
// IPAllocationPerfTestSuite — measurement suite
// =============================================================================

// IPAllocationPerfTestSuite measures IP allocation latency.
// For pool mode the caller (poolConfigManager) has already applied the config;
// this suite only checks node availability.
// For prefix mode this suite manages its own config lifecycle.
type IPAllocationPerfTestSuite struct {
	NodeType NodeType
	Config   PerfTestConfig

	Deployments        []*appsv1.Deployment
	LatencyStats       utils.LatencyStats
	AllocIPFailedCount int

	IterationStats       []utils.LatencyStats
	IterationFailedCount []int

	// prefix mode: the specific node name discovered during setup
	prefixNodeName string
}

func NewIPAllocationPerfTestSuite(nodeType NodeType, config PerfTestConfig) *IPAllocationPerfTestSuite {
	return &IPAllocationPerfTestSuite{
		NodeType: nodeType,
		Config:   config,
	}
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

func (s *IPAllocationPerfTestSuite) Setup(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	if s.Config.IsPrefix() {
		return s.setupPrefix(ctx, t, config)
	}
	return s.checkNodeAvailability(ctx, t, config)
}

// checkNodeAvailability is the lightweight Setup for pool-mode tests.
// Pool config is already applied by the parent poolConfigManager.
func (s *IPAllocationPerfTestSuite) checkNodeAvailability(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
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
	return ctx
}

func (s *IPAllocationPerfTestSuite) setupPrefix(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, config.Client())
	if err != nil {
		t.Fatalf("failed to discover node types with capacity: %v", err)
	}

	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		if cap.IPv4PerAdapter >= s.Config.PrefixCount+1 {
			s.prefixNodeName = nodeName
			break
		}
	}
	if s.prefixNodeName == "" {
		t.Skipf("No qualified IP Prefix node (IPv4PerAdapter >= %d)", s.Config.PrefixCount+1)
		return ctx
	}
	t.Logf("Selected IP Prefix node: %s", s.prefixNodeName)

	t.Logf("Configuring ipv4_prefix_count=%d on node %s", s.Config.PrefixCount, s.prefixNodeName)
	if err := configureIPPrefixCount(ctx, t, config, s.prefixNodeName, s.Config.PrefixCount); err != nil {
		t.Fatalf("failed to configure ipv4_prefix_count: %v", err)
	}

	if err := restartTerway(ctx, config); err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	t.Logf("Waiting for %d prefixes to be allocated on node %s", s.Config.PrefixCount, s.prefixNodeName)
	if err := waitForPrefixAllocation(ctx, config, t, s.prefixNodeName, s.Config.PrefixCount, 5*time.Minute); err != nil {
		t.Fatalf("prefixes not allocated in time: %v", err)
	}

	t.Logf("Setup complete: %d prefixes ready on node %s", s.Config.PrefixCount, s.prefixNodeName)
	return ctx
}

// ---------------------------------------------------------------------------
// Core test flow (shared by both modes)
// ---------------------------------------------------------------------------

func (s *IPAllocationPerfTestSuite) WaitForPodsReady(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Logf("Waiting for %d pods to be ready...", s.Config.TotalPods())

	for _, deploy := range s.Deployments {
		err := wait.For(conditions.New(config.Client().Resources()).DeploymentConditionMatch(
			deploy, appsv1.DeploymentAvailable, corev1.ConditionTrue),
			wait.WithTimeout(10*time.Minute),
			wait.WithInterval(5*time.Second))
		if err != nil {
			t.Fatalf("deployment %s not ready: %v", deploy.Name, err)
		}
		t.Logf("Deployment %s is ready", deploy.Name)
	}
	return ctx
}

func (s *IPAllocationPerfTestSuite) CollectLatencies(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("Collecting AllocIPSucceed and AllocIPFailed events...")

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
				t.Logf("Warning: failed to parse latency: %v", err)
				continue
			}
			latencies = append(latencies, latency)
		case "AllocIPFailed":
			s.AllocIPFailedCount++
			t.Logf("AllocIPFailed: pod=%s, message=%s", event.InvolvedObject.Name, event.Message)
		}
	}

	if len(latencies) == 0 {
		t.Error("No AllocIPSucceed events found")
		return ctx
	}

	s.LatencyStats = utils.CalculateLatencyStats(latencies)
	t.Logf("Latency Stats for %s/%s: %s (AllocIPFailed: %d)",
		s.NodeType, s.Config.Name, s.LatencyStats.String(), s.AllocIPFailedCount)
	return ctx
}

func (s *IPAllocationPerfTestSuite) ScaleDown(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	t.Log("Scaling down deployments...")

	for _, deploy := range s.Deployments {
		if err := config.Client().Resources().Get(ctx, deploy.Name, deploy.Namespace, deploy); err != nil {
			t.Logf("Warning: failed to get deployment %s: %v", deploy.Name, err)
			continue
		}
		zero := int32(0)
		deploy.Spec.Replicas = &zero
		if err := config.Client().Resources().Update(ctx, deploy); err != nil {
			t.Logf("Warning: failed to scale down %s: %v", deploy.Name, err)
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
			t.Logf("Warning: deployment %s did not scale to zero: %v", deploy.Name, err)
		}
	}

	t.Log("All deployments scaled down")
	return ctx
}

// ---------------------------------------------------------------------------
// Iteration management
// ---------------------------------------------------------------------------

func (s *IPAllocationPerfTestSuite) RunIteration(ctx context.Context, t *testing.T, config *envconf.Config, iteration int) context.Context {
	t.Logf("=== Starting iteration %d ===", iteration+1)

	s.Deployments = make([]*appsv1.Deployment, s.Config.DeploymentCount)
	anchorDeployName := fmt.Sprintf("perf-%s-%s-0", s.NodeType, s.Config.Name)
	anchorPodLabel := map[string]string{"app": anchorDeployName}

	for i := 0; i < s.Config.DeploymentCount; i++ {
		name := fmt.Sprintf("perf-%s-%s-%d", s.NodeType, s.Config.Name, i)
		deploy := utils.NewDeployment(name, config.Namespace(), int32(s.Config.PodsPerDeploy))

		if s.Config.IsPrefix() {
			deploy = deploy.WithNodeAffinity(map[string]string{"kubernetes.io/hostname": s.prefixNodeName})
		} else {
			deploy = deploy.
				WithNodeAffinity(GetNodeAffinityForType(s.NodeType)).
				WithNodeAffinityExclude(GetNodeAffinityExcludeForType(s.NodeType))
		}
		deploy = deploy.WithPodAffinity(anchorPodLabel)

		if utils.IsLingjunNodeType(string(s.NodeType)) {
			deploy = deploy.WithLingjunToleration()
		}

		if err := config.Client().Resources().Create(ctx, deploy.Deployment); err != nil {
			t.Fatalf("failed to create deployment %s: %v", name, err)
		}
		s.Deployments[i] = deploy.Deployment
	}
	t.Logf("Created %d deployments (%d pods total)", s.Config.DeploymentCount, s.Config.TotalPods())

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

	time.Sleep(5 * time.Second)
	t.Logf("=== Completed iteration %d ===", iteration+1)
	return ctx
}

func (s *IPAllocationPerfTestSuite) RunAllIterations(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	s.IterationStats = make([]utils.LatencyStats, 0, 1)
	s.IterationFailedCount = make([]int, 0, 1)
	return s.RunIteration(ctx, t, config, 0)
}

// ---------------------------------------------------------------------------
// Results
// ---------------------------------------------------------------------------

func (s *IPAllocationPerfTestSuite) PrintResults(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
	t.Log("========================================")
	t.Logf("IP Allocation Performance Test Results")
	t.Logf("Node Type:       %s", s.NodeType)
	t.Logf("Config:          %s", s.Config.Name)
	if s.Config.IsPrefix() {
		t.Logf("Prefix count:    %d", s.Config.PrefixCount)
		t.Logf("Node:            %s", s.prefixNodeName)
	} else {
		t.Logf("Pool:            min=%d, max=%d", s.Config.MinPoolSize, s.Config.MaxPoolSize)
	}
	t.Logf("Deployments:     %d x %d pods = %d total",
		s.Config.DeploymentCount, s.Config.PodsPerDeploy, s.Config.TotalPods())
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

// ---------------------------------------------------------------------------
// Teardown (only needed for prefix mode; pool mode config is managed by parent)
// ---------------------------------------------------------------------------

func (s *IPAllocationPerfTestSuite) RestoreConfig(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
	if !s.Config.IsPrefix() {
		return ctx
	}
	if s.prefixNodeName == "" {
		return ctx
	}
	t.Logf("Resetting prefix state on node %s", s.prefixNodeName)
	if err := resetNodePrefixState(ctx, config, t, s.prefixNodeName, 5*time.Minute); err != nil {
		t.Logf("Warning: resetNodePrefixState failed: %v", err)
	}
	return ctx
}

// =============================================================================
// Test entry points
// =============================================================================

// TestIPAllocationPerf measures IP allocation performance across different modes:
//   - Pool-based (shared ENI): grouped by pool config; ECS and Lingjun run in
//     parallel within each group since they use different nodes.
//   - IP Prefix: standalone (requires terway >= v1.17.0).
//
// Hierarchy:
//
//	DefaultPool
//	  ├── ecs-shared-eni      ← parallel
//	  └── lingjun-shared-eni  ← parallel
//	WarmUpPool
//	  ├── ecs-shared-eni      ← parallel
//	  └── lingjun-shared-eni  ← parallel
//	IPPrefix (sequential, own config lifecycle)
func TestIPAllocationPerf(t *testing.T) {
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		ipamType := ""
		if eniConfig != nil {
			ipamType = eniConfig.IPAMType
		}
		t.Skipf("skip: ipam type is not crd, current type: %s", ipamType)
		return
	}
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
		return
	}

	t.Run("DefaultPool", func(t *testing.T) {
		runPoolPerfTests(t, defaultPoolConfig)
	})

	t.Run("WarmUpPool", func(t *testing.T) {
		runPoolPerfTests(t, warmUpPoolConfig)
	})

	t.Run("IPPrefix", func(t *testing.T) {
		if !RequireTerwayVersion("v1.17.0") {
			t.Skipf("IP Prefix requires terway >= v1.17.0")
			return
		}
		runPrefixPerfTest(t, NodeTypeECSIPPrefix, prefixPerfConfig)
	})
}

// runPoolPerfTests applies a pool config once, then runs ECS and Lingjun
// measurement tests in parallel. t.Cleanup restores the config after both finish.
func runPoolPerfTests(t *testing.T, poolCfg PerfTestConfig) {
	ctx := context.Background()
	envCfg := testenv.EnvConf()

	mgr := &poolConfigManager{config: poolCfg}
	mgr.apply(ctx, t, envCfg)
	t.Cleanup(func() { mgr.restore(ctx, t, envCfg) })

	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}
	for _, nt := range nodeTypes {
		nt := nt
		t.Run(string(nt), func(t *testing.T) {
			t.Parallel()
			runMeasurementTest(t, nt, poolCfg)
		})
	}
}

// runMeasurementTest runs a measurement-only feature (no config setup/teardown).
func runMeasurementTest(t *testing.T, nodeType NodeType, config PerfTestConfig) {
	suite := NewIPAllocationPerfTestSuite(nodeType, config)

	feature := features.New(fmt.Sprintf("IPAllocationPerf/%s-%s", nodeType, config.Name)).
		WithLabel("env", "performance").
		Setup(suite.Setup).
		Assess("run iterations", suite.RunAllIterations).
		Assess("print results", suite.PrintResults).
		Feature()

	testenv.Test(t, feature)
}

// runPrefixPerfTest runs a full-lifecycle prefix performance test (own config + teardown).
func runPrefixPerfTest(t *testing.T, nodeType NodeType, config PerfTestConfig) {
	suite := NewIPAllocationPerfTestSuite(nodeType, config)

	feature := features.New(fmt.Sprintf("IPAllocationPerf/%s-%s", nodeType, config.Name)).
		WithLabel("env", "performance").
		Setup(suite.Setup).
		Assess("run iterations", suite.RunAllIterations).
		Assess("print results", suite.PrintResults).
		Teardown(suite.RestoreConfig).
		Feature()

	testenv.Test(t, feature)
}
