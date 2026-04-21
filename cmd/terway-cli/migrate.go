package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	k8stypes "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/version"
	"github.com/AliyunContainerService/terway/types"
)

// ---------------------------------------------------------------------------
// Flags
// ---------------------------------------------------------------------------

var (
	migrateNodes        string
	migrateNodeSelector string
	migrateDryRun       bool
	migrateYes          bool
	migrateRegion       string
	migrateBatch        int
	migrateLimit        int
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	resultSuccess   = "SUCCESS"
	resultFailed    = "FAILED"
	resultSkipped   = "SKIPPED"
	resultDryRun    = "DRY-RUN"
	resultPreFailed = "PRE-CHECK FAILED"

	postCheckInterval = 10 * time.Second
	postCheckTimeout  = 5 * time.Minute

	stabilizeInterval = 5 * time.Second
	stabilizeTimeout  = 1 * time.Minute

	defaultBatchSize = 10
	confirmListMax   = 20
)

// ---------------------------------------------------------------------------
// Per-node lifecycle state
// ---------------------------------------------------------------------------

type nodeState struct {
	nodeName      string
	instanceID    string
	exclusiveMode string
	oldENOApi     string
	newENOApi     string

	wasUnschedulable bool
	cordonedByUs     bool
	uncordoned       bool
	stabilized       bool

	preCheckPassed   bool
	preCheckFailures []string

	migrated          bool
	postCheckPassed   bool
	postCheckFailures []string

	result string
	detail string
}

// ---------------------------------------------------------------------------
// Command
// ---------------------------------------------------------------------------

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate EFLO (LENI/HDENI) nodes from ENO API to ECS API",
	Long: `Migrate LingJun (EFLO) nodes from ENO backend to ECS backend.

Nodes are processed in parallel batches. Each run goes through sequential phases:
  1. Resolve   — validate each node and determine migration target
  2. Cordon    — mark nodes unschedulable (skips already-cordoned nodes)
  3. Pre-check — verify ENI state, OpenAPI tags, API consistency
  4. Migrate   — update ENOApi annotation (only nodes that passed pre-checks)
  5. Post-check— poll until controller reconciles (timeout 5m)
  6. Uncordon  — restore schedulability (only nodes cordoned by this tool)

Use --dry-run to run only resolve + pre-checks without any mutation.

Prerequisites:
  - kubectl access to the cluster
  - Alibaba Cloud credentials (AK/SK or ECS metadata)
  - Nodes must have migration tags on their ENIs

Examples:
  # Dry-run: pre-check specific nodes
  terway-cli migrate --nodes node-1,node-2 --region cn-hangzhou --dry-run

  # Migrate by label selector with batch=20
  terway-cli migrate --node-selector nodepool=eflo --region cn-hangzhou --batch 20

  # Migrate first 100 nodes from a large pool
  terway-cli migrate --node-selector nodepool=eflo --region cn-hangzhou --limit 100

  # Skip confirmation
  terway-cli migrate --nodes node-1,node-2 --region cn-hangzhou --yes`,
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().StringVar(&migrateNodes, "nodes", "", "Comma-separated list of node names")
	migrateCmd.Flags().StringVar(&migrateNodeSelector, "node-selector", "", "Label selector to filter/discover nodes")
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Only run pre-checks without making changes")
	migrateCmd.Flags().BoolVar(&migrateYes, "yes", false, "Skip confirmation prompt")
	migrateCmd.Flags().StringVar(&migrateRegion, "region", os.Getenv("REGION_ID"), "Alibaba Cloud region ID (or REGION_ID env)")
	migrateCmd.Flags().IntVar(&migrateBatch, "batch", defaultBatchSize, "Max parallel nodes per phase")
	migrateCmd.Flags().IntVar(&migrateLimit, "limit", 0, "Max total nodes to process (0 = unlimited)")
}

// ---------------------------------------------------------------------------
// Orchestrator
// ---------------------------------------------------------------------------

func runMigrate(cmd *cobra.Command, args []string) error {
	log.SetOutput(io.Discard)
	ctrl.SetLogger(logr.Discard())

	if migrateNodes == "" && migrateNodeSelector == "" {
		return fmt.Errorf("either --nodes or --node-selector is required")
	}
	if migrateBatch <= 0 {
		return fmt.Errorf("--batch must be positive")
	}

	if migrateRegion == "" {
		if detected, err := metadata.GetLocalRegion(); err == nil && detected != "" {
			migrateRegion = detected
			pterm.Info.Printf("Auto-detected region from ECS metadata: %s\n", migrateRegion)
		} else {
			return fmt.Errorf("--region is required (auto-detection via ECS metadata failed: %v)", err)
		}
	}

	k8s, err := newK8sClient()
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	nodeNames, err := resolveTargetNodes(context.Background(), k8s, migrateNodes, migrateNodeSelector)
	if err != nil {
		return err
	}
	if len(nodeNames) == 0 {
		return fmt.Errorf("no nodes matched the provided filters")
	}

	if migrateLimit > 0 && len(nodeNames) > migrateLimit {
		pterm.Info.Printf("Limiting to %d of %d matched nodes\n", migrateLimit, len(nodeNames))
		nodeNames = nodeNames[:migrateLimit]
	}

	ecsClient, err := getECSClient(migrateRegion)
	if err != nil {
		return fmt.Errorf("failed to create ECS client: %w", err)
	}
	efloControlClient, err := getEFLOControlClient(migrateRegion)
	if err != nil {
		return fmt.Errorf("failed to create EFLO Control client: %w", err)
	}
	checker := &migrationChecker{k8s: k8s, ecs: ecsClient, efloControl: efloControlClient}

	states := make([]*nodeState, len(nodeNames))
	for i, name := range nodeNames {
		states[i] = &nodeState{nodeName: name}
	}

	batchRounds := len(nodeNames)/migrateBatch + 1
	overallTimeout := postCheckTimeout*time.Duration(batchRounds) + 10*time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	// showSummary gates whether the deferred cleanup prints the final table.
	// Set to true once resolve completes; stays false on early exits (e.g. cancel).
	showSummary := false
	defer func() {
		// Always uncordon nodes we cordoned, regardless of exit path.
		toUncordon := filterCordonedByUs(states)
		if len(toUncordon) > 0 {
			printSectionHeader("Phase 6: Uncordon")
			runPhase(ctx, toUncordon, migrateBatch, "Uncordoning nodes", func(ctx context.Context, ns *nodeState) {
				phaseUncordon(ctx, k8s, ns)
			})
			printUncordonResults(states)
		}
		if showSummary {
			printFinalSummary(states)
		}
	}()

	// ── Phase 1: Resolve ──
	printSectionHeader("Phase 1: Resolve")
	runPhase(ctx, states, migrateBatch, "Resolving nodes", func(ctx context.Context, ns *nodeState) {
		resolveNodeState(ctx, k8s, ns)
	})
	printResolveResults(states)
	showSummary = true

	active := filterActive(states)
	if len(active) == 0 {
		pterm.Info.Println("No nodes eligible for migration")
		return nil
	}

	// ── dry-run: resolve + pre-check only ──
	if migrateDryRun {
		printSectionHeader("Phase 2: Pre-checks (dry-run)")
		runPhase(ctx, active, migrateBatch, "Running pre-checks", func(ctx context.Context, ns *nodeState) {
			phasePreCheck(ctx, checker, ns)
		})
		for _, ns := range active {
			if ns.preCheckPassed {
				ns.result = resultDryRun
				ns.detail = fmt.Sprintf("would migrate %s → %s", displayENOApi(ns.oldENOApi), displayENOApi(ns.newENOApi))
			}
		}
		printPreCheckResults(states)
		return nil
	}

	// ── Confirmation ──
	if !migrateYes {
		pterm.Warning.Printf("About to migrate %d node(s) from EFLO to ECS API (batch=%d):\n", len(active), migrateBatch)
		printNodeList(active)
		fmt.Println()
		fmt.Println("  The following actions will be performed for each node:")
		fmt.Println("    1. Cordon      — mark the node as unschedulable")
		fmt.Println("    2. Pre-check   — verify ENI state, OpenAPI tags, and API consistency")
		fmt.Println("    3. Migrate     — update the ENOApi annotation on the Node CR")
		fmt.Println("    4. Post-check  — wait for the controller to reconcile (up to 5m)")
		fmt.Println("    5. Uncordon    — restore the node to schedulable")
		fmt.Println()
		confirmed, promptErr := pterm.DefaultInteractiveConfirm.WithDefaultText("Proceed?").Show()
		if promptErr != nil {
			return fmt.Errorf("failed to get confirmation: %w", promptErr)
		}
		if !confirmed {
			showSummary = false
			pterm.Info.Println("Migration cancelled")
			return nil
		}
	}

	// ── Phase 2: Cordon ──
	printSectionHeader("Phase 2: Cordon")
	runPhase(ctx, active, migrateBatch, "Cordoning nodes", func(ctx context.Context, ns *nodeState) {
		phaseCordon(ctx, k8s, ns)
	})
	printCordonResults(active)

	activeAfterCordon := filterActive(active)
	if len(activeAfterCordon) == 0 {
		pterm.Error.Println("All nodes failed to cordon")
		return fmt.Errorf("migration completed with failures")
	}

	// ── Phase 2b: Stabilize — wait for transient pods to settle ──
	runPhase(ctx, activeAfterCordon, migrateBatch, "Waiting for pods to stabilize", func(ctx context.Context, ns *nodeState) {
		phaseStabilize(ctx, checker, ns)
	})
	printStabilizeResults(activeAfterCordon)

	// ── Phase 3: Pre-checks ──
	printSectionHeader("Phase 3: Pre-checks")
	runPhase(ctx, activeAfterCordon, migrateBatch, "Running pre-checks", func(ctx context.Context, ns *nodeState) {
		phasePreCheck(ctx, checker, ns)
	})
	printPreCheckResults(states)

	toMigrate := filterPreCheckPassed(activeAfterCordon)
	if len(toMigrate) == 0 {
		pterm.Error.Println("No nodes passed pre-checks")
		return fmt.Errorf("migration completed with failures")
	}

	// ── Phase 4: Migrate ──
	printSectionHeader("Phase 4: Migrate")
	runPhase(ctx, toMigrate, migrateBatch, "Updating annotations", func(ctx context.Context, ns *nodeState) {
		phaseMigrate(ctx, k8s, ns)
	})
	printMigrateResults(toMigrate)

	migrated := filterMigrated(toMigrate)
	if len(migrated) == 0 {
		pterm.Error.Println("No nodes were migrated successfully")
		return fmt.Errorf("migration completed with failures")
	}

	// ── Phase 5: Post-checks ──
	printSectionHeader("Phase 5: Post-checks")
	runPhase(ctx, migrated, migrateBatch, "Waiting for post-checks", func(ctx context.Context, ns *nodeState) {
		phasePostCheck(ctx, checker, ns)
	})
	printPostCheckResults(migrated)

	// Uncordon + final summary run via the deferred function above.
	for _, ns := range states {
		if ns.result == resultFailed || ns.result == resultPreFailed {
			return fmt.Errorf("migration completed with failures")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Phase implementations (each writes only to its own *nodeState)
// ---------------------------------------------------------------------------

func resolveNodeState(ctx context.Context, k8s k8sClient.Client, ns *nodeState) {
	k8sNode := &corev1.Node{}
	if err := k8s.Get(ctx, k8sClient.ObjectKey{Name: ns.nodeName}, k8sNode); err != nil {
		ns.result = resultFailed
		ns.detail = fmt.Sprintf("failed to get k8s node: %v", err)
		return
	}

	if k8sNode.Labels[types.LingJunNodeLabelKey] != True {
		ns.result = resultSkipped
		ns.detail = "not a LingJun node"
		return
	}

	crNode := &networkv1beta1.Node{}
	if err := k8s.Get(ctx, k8sClient.ObjectKey{Name: ns.nodeName}, crNode); err != nil {
		ns.result = resultFailed
		ns.detail = fmt.Sprintf("failed to get network Node CR: %v", err)
		return
	}

	ns.exclusiveMode = string(types.NodeExclusiveENIMode(crNode.Labels))
	ns.oldENOApi = crNode.Annotations[types.ENOApi]

	targetENOApi, skip := determineTargetENOApi(ns.oldENOApi)
	if skip {
		ns.result = resultSkipped
		ns.newENOApi = ns.oldENOApi
		ns.detail = fmt.Sprintf("already migrated (current: %s)", displayENOApi(ns.oldENOApi))
		return
	}
	ns.newENOApi = targetENOApi

	ns.instanceID = crNode.Spec.NodeMetadata.InstanceID
	if ns.instanceID == "" {
		ns.result = resultFailed
		ns.detail = "node CR has no instanceID"
		return
	}
}

func phaseCordon(ctx context.Context, k8s k8sClient.Client, ns *nodeState) {
	k8sNode := &corev1.Node{}
	if err := k8s.Get(ctx, k8sClient.ObjectKey{Name: ns.nodeName}, k8sNode); err != nil {
		ns.result = resultFailed
		ns.detail = fmt.Sprintf("cordon: failed to get node: %v", err)
		return
	}

	if k8sNode.Spec.Unschedulable {
		ns.wasUnschedulable = true
		return
	}

	patch := []byte(`{"spec":{"unschedulable":true}}`)
	if err := k8s.Patch(ctx, k8sNode, k8sClient.RawPatch(k8stypes.MergePatchType, patch)); err != nil {
		ns.result = resultFailed
		ns.detail = fmt.Sprintf("cordon: failed to patch: %v", err)
		return
	}
	ns.cordonedByUs = true
}

// phaseStabilize waits up to stabilizeTimeout for pods on the node to leave
// transient states (ContainerCreating, PodInitializing, Pending). This runs
// after cordon so that in-flight creations can finish before pre-checks.
func phaseStabilize(ctx context.Context, checker *migrationChecker, ns *nodeState) {
	deadline := time.Now().Add(stabilizeTimeout)
	for {
		ok, _ := checkCreatingPods(ctx, checker.k8s, ns.nodeName)
		if ok {
			ns.stabilized = true
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(stabilizeInterval):
		}
	}
}

func phasePreCheck(ctx context.Context, checker *migrationChecker, ns *nodeState) {
	items := checker.preCheckItems(ctx, ns.nodeName, ns.instanceID, ns.newENOApi, ns.exclusiveMode)
	passed, results := runChecks(items)
	ns.preCheckPassed = passed
	if !passed {
		var failures []string
		for _, r := range results {
			if !r.passed {
				failures = append(failures, r.msgs...)
			}
		}
		ns.preCheckFailures = failures
		ns.result = resultPreFailed
		ns.detail = strings.Join(failures, "; ")
	}
}

func phaseMigrate(ctx context.Context, k8s k8sClient.Client, ns *nodeState) {
	if err := updateNodeENOApi(ctx, k8s, ns.nodeName, ns.newENOApi); err != nil {
		ns.result = resultFailed
		ns.detail = fmt.Sprintf("migrate: %v", err)
		return
	}
	ns.migrated = true
}

func phasePostCheck(ctx context.Context, checker *migrationChecker, ns *nodeState) {
	deadline := time.Now().Add(postCheckTimeout)
	for {
		select {
		case <-ctx.Done():
			ns.result = resultFailed
			ns.detail = "context cancelled"
			return
		case <-time.After(postCheckInterval):
		}

		items := checker.postCheckItems(ctx, ns.nodeName, ns.newENOApi, ns.exclusiveMode)
		passed, results := runChecks(items)
		if passed {
			ns.postCheckPassed = true
			ns.result = resultSuccess
			return
		}

		if time.Now().After(deadline) {
			var failures []string
			for _, r := range results {
				if !r.passed {
					failures = append(failures, r.msgs...)
				}
			}
			ns.postCheckFailures = failures
			ns.result = resultFailed
			ns.detail = fmt.Sprintf("post-check timed out: %s", strings.Join(failures, "; "))
			return
		}
	}
}

func phaseUncordon(ctx context.Context, k8s k8sClient.Client, ns *nodeState) {
	k8sNode := &corev1.Node{}
	if err := k8s.Get(ctx, k8sClient.ObjectKey{Name: ns.nodeName}, k8sNode); err != nil {
		ns.detail = fmt.Sprintf("uncordon: failed to get node: %v", err)
		return
	}
	patch := []byte(`{"spec":{"unschedulable":false}}`)
	if err := k8s.Patch(ctx, k8sNode, k8sClient.RawPatch(k8stypes.MergePatchType, patch)); err != nil {
		ns.detail = fmt.Sprintf("uncordon: failed to patch: %v", err)
		return
	}
	ns.uncordoned = true
}

// ---------------------------------------------------------------------------
// Parallel execution helper
// ---------------------------------------------------------------------------

func runPhase(ctx context.Context, states []*nodeState, batch int, label string,
	fn func(ctx context.Context, ns *nodeState)) {
	total := len(states)
	if total == 0 {
		return
	}
	var done atomic.Int32
	spinner, _ := pterm.DefaultSpinner.Start(fmt.Sprintf("%s (0/%d)...", label, total))

	g := new(errgroup.Group)
	g.SetLimit(batch)
	for _, ns := range states {
		g.Go(func() error {
			fn(ctx, ns)
			n := done.Add(1)
			spinner.UpdateText(fmt.Sprintf("%s (%d/%d)...", label, n, total))
			return nil
		})
	}
	_ = g.Wait()
	spinner.Success(fmt.Sprintf("%s (%d/%d)", label, total, total))
}

// ---------------------------------------------------------------------------
// Filter helpers
// ---------------------------------------------------------------------------

func filterActive(states []*nodeState) []*nodeState {
	var out []*nodeState
	for _, ns := range states {
		if ns.result == "" {
			out = append(out, ns)
		}
	}
	return out
}

func filterCordonedByUs(states []*nodeState) []*nodeState {
	var out []*nodeState
	for _, ns := range states {
		if ns.cordonedByUs && !ns.uncordoned {
			out = append(out, ns)
		}
	}
	return out
}

func filterPreCheckPassed(states []*nodeState) []*nodeState {
	var out []*nodeState
	for _, ns := range states {
		if ns.preCheckPassed {
			out = append(out, ns)
		}
	}
	return out
}

func filterMigrated(states []*nodeState) []*nodeState {
	var out []*nodeState
	for _, ns := range states {
		if ns.migrated {
			out = append(out, ns)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Display: per-phase summaries
// ---------------------------------------------------------------------------

func printNodeList(states []*nodeState) {
	limit := confirmListMax
	if len(states) < limit {
		limit = len(states)
	}
	for i := 0; i < limit; i++ {
		fmt.Printf("  • %s\n", states[i].nodeName)
	}
	if remaining := len(states) - limit; remaining > 0 {
		fmt.Printf("  ... and %d more\n", remaining)
	}
}

func printResolveResults(states []*nodeState) {
	var active, failed, skipped int
	for _, ns := range states {
		switch ns.result {
		case "":
			active++
		case resultSkipped:
			skipped++
		default:
			failed++
		}
	}
	var parts []string
	if active > 0 {
		parts = append(parts, pterm.Green(fmt.Sprintf("%d eligible", active)))
	}
	if skipped > 0 {
		parts = append(parts, pterm.Yellow(fmt.Sprintf("%d skipped", skipped)))
	}
	if failed > 0 {
		parts = append(parts, pterm.Red(fmt.Sprintf("%d failed", failed)))
	}
	fmt.Printf("  %s\n", strings.Join(parts, ", "))

	for _, ns := range states {
		switch ns.result {
		case "":
			fmt.Printf("  %s %s: %s → %s (mode=%s)\n",
				pterm.Green("✓"), ns.nodeName,
				displayENOApi(ns.oldENOApi), displayENOApi(ns.newENOApi), ns.exclusiveMode)
		case resultSkipped:
			fmt.Printf("  %s %s: %s\n", pterm.Yellow("‒"), ns.nodeName, ns.detail)
		}
	}
	printFailedNodes(states)
}

func printCordonResults(states []*nodeState) {
	var cordoned, alreadyCordoned, failed int
	for _, ns := range states {
		switch {
		case ns.cordonedByUs:
			cordoned++
		case ns.wasUnschedulable:
			alreadyCordoned++
		case ns.result == resultFailed:
			failed++
		}
	}
	var parts []string
	if cordoned > 0 {
		parts = append(parts, pterm.Green(fmt.Sprintf("%d cordoned", cordoned)))
	}
	if alreadyCordoned > 0 {
		parts = append(parts, pterm.Yellow(fmt.Sprintf("%d already cordoned", alreadyCordoned)))
	}
	if failed > 0 {
		parts = append(parts, pterm.Red(fmt.Sprintf("%d failed", failed)))
	}
	fmt.Printf("  %s\n", strings.Join(parts, ", "))
	printFailedNodes(states)
}

func printStabilizeResults(states []*nodeState) {
	var stable, waiting int
	for _, ns := range states {
		if ns.stabilized {
			stable++
		} else {
			waiting++
		}
	}
	var parts []string
	if stable > 0 {
		parts = append(parts, pterm.Green(fmt.Sprintf("%d stable", stable)))
	}
	if waiting > 0 {
		parts = append(parts, pterm.Yellow(fmt.Sprintf("%d still have transient pods", waiting)))
	}
	fmt.Printf("  %s\n", strings.Join(parts, ", "))
}

func printPreCheckResults(all []*nodeState) {
	var passed, failed int
	for _, ns := range all {
		switch {
		case ns.preCheckPassed:
			passed++
		case ns.result == resultPreFailed:
			failed++
		}
	}
	if passed == 0 && failed == 0 {
		return
	}
	var parts []string
	if passed > 0 {
		parts = append(parts, pterm.Green(fmt.Sprintf("%d passed", passed)))
	}
	if failed > 0 {
		parts = append(parts, pterm.Red(fmt.Sprintf("%d failed", failed)))
	}
	fmt.Printf("  %s\n", strings.Join(parts, ", "))

	for _, ns := range all {
		if ns.result == resultPreFailed {
			detail := ns.detail
			if len(detail) > 100 {
				detail = detail[:97] + "..."
			}
			fmt.Printf("  %s %s: %s\n", pterm.Red("✗"), ns.nodeName, detail)
		}
	}
}

func printMigrateResults(states []*nodeState) {
	var ok, failed int
	for _, ns := range states {
		if ns.migrated {
			ok++
		} else {
			failed++
		}
	}
	var parts []string
	if ok > 0 {
		parts = append(parts, pterm.Green(fmt.Sprintf("%d migrated", ok)))
	}
	if failed > 0 {
		parts = append(parts, pterm.Red(fmt.Sprintf("%d failed", failed)))
	}
	fmt.Printf("  %s\n", strings.Join(parts, ", "))
	printFailedNodes(states)
}

func printPostCheckResults(states []*nodeState) {
	var passed, failed int
	for _, ns := range states {
		if ns.postCheckPassed {
			passed++
		} else if ns.result == resultFailed {
			failed++
		}
	}
	var parts []string
	if passed > 0 {
		parts = append(parts, pterm.Green(fmt.Sprintf("%d passed", passed)))
	}
	if failed > 0 {
		parts = append(parts, pterm.Red(fmt.Sprintf("%d timed out", failed)))
	}
	fmt.Printf("  %s\n", strings.Join(parts, ", "))
	printFailedNodes(states)
}

func printUncordonResults(all []*nodeState) {
	var restored, notRestored int
	for _, ns := range all {
		if ns.uncordoned {
			restored++
		} else if ns.cordonedByUs {
			notRestored++
		}
	}
	var parts []string
	if restored > 0 {
		parts = append(parts, pterm.Green(fmt.Sprintf("%d restored", restored)))
	}
	if notRestored > 0 {
		parts = append(parts, pterm.Red(fmt.Sprintf("%d failed to restore", notRestored)))
	}
	if len(parts) > 0 {
		fmt.Printf("  %s\n", strings.Join(parts, ", "))
	}
	for _, ns := range all {
		if ns.cordonedByUs && !ns.uncordoned && ns.detail != "" {
			fmt.Printf("  %s %s: %s\n", pterm.Red("✗"), ns.nodeName, ns.detail)
		}
	}
}

func printFailedNodes(states []*nodeState) {
	for _, ns := range states {
		if ns.result != resultFailed {
			continue
		}
		detail := ns.detail
		if len(detail) > 100 {
			detail = detail[:97] + "..."
		}
		fmt.Printf("  %s %s: %s\n", pterm.Red("✗"), ns.nodeName, detail)
	}
}

// ---------------------------------------------------------------------------
// Display: final summary table
// ---------------------------------------------------------------------------

func printFinalSummary(states []*nodeState) {
	printSectionHeader("Summary")

	tableData := pterm.TableData{
		{"Node", "Mode", "Before", "After", "Pre-check", "Migration", "Uncordon"},
	}

	for _, ns := range states {
		tableData = append(tableData, []string{
			ns.nodeName,
			ns.exclusiveMode,
			displayENOApi(ns.oldENOApi),
			displayENOApi(ns.newENOApi),
			preCheckColumn(ns),
			migrationColumn(ns),
			uncordonColumn(ns),
		})
	}

	_ = pterm.DefaultTable.WithHasHeader(true).WithData(tableData).Render()

	var success, dryrun, prefailed, failed, skipped int
	for _, ns := range states {
		switch ns.result {
		case resultSuccess:
			success++
		case resultDryRun:
			dryrun++
		case resultPreFailed:
			prefailed++
		case resultFailed:
			failed++
		case resultSkipped:
			skipped++
		}
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("%d total", len(states)))
	if success > 0 {
		parts = append(parts, pterm.Green(fmt.Sprintf("%d succeeded", success)))
	}
	if dryrun > 0 {
		parts = append(parts, pterm.Cyan(fmt.Sprintf("%d dry-run", dryrun)))
	}
	if prefailed > 0 {
		parts = append(parts, pterm.Red(fmt.Sprintf("%d pre-check failed", prefailed)))
	}
	if failed > 0 {
		parts = append(parts, pterm.Red(fmt.Sprintf("%d failed", failed)))
	}
	if skipped > 0 {
		parts = append(parts, pterm.Yellow(fmt.Sprintf("%d skipped", skipped)))
	}
	fmt.Printf("\n%s\n", strings.Join(parts, ", "))
}

func preCheckColumn(ns *nodeState) string {
	switch {
	case ns.preCheckPassed:
		return pterm.Green("✓ PASS")
	case ns.result == resultPreFailed:
		return pterm.Red("✗ FAIL")
	default:
		return "-"
	}
}

func migrationColumn(ns *nodeState) string {
	switch {
	case ns.result == resultSuccess:
		return pterm.Green("✓ SUCCESS")
	case ns.result == resultDryRun:
		return pterm.Cyan("● DRY-RUN")
	case ns.migrated && ns.result == resultFailed:
		return pterm.Red("✗ POST-FAIL")
	case ns.result == resultFailed && !ns.migrated:
		return pterm.Red("✗ FAILED")
	default:
		return "-"
	}
}

func uncordonColumn(ns *nodeState) string {
	switch {
	case ns.uncordoned:
		return pterm.Green("✓ restored")
	case ns.wasUnschedulable:
		return "was cordoned"
	case ns.cordonedByUs:
		return pterm.Red("✗ stuck")
	default:
		return "-"
	}
}

// ---------------------------------------------------------------------------
// Utility: display
// ---------------------------------------------------------------------------

func printSectionHeader(title string) {
	pterm.DefaultSection.
		WithLevel(2).
		Println(title)
}

func displayENOApi(api string) string {
	if api == "" {
		return "(eflo)"
	}
	return api
}

// ---------------------------------------------------------------------------
// Utility: migration logic
// ---------------------------------------------------------------------------

func determineTargetENOApi(current string) (target string, skip bool) {
	switch current {
	case "":
		return types.APIEcs, false
	case types.APIEnoHDeni:
		return types.APIEcsHDeni, false
	case types.APIEcs, types.APIEcsHDeni:
		return current, true
	default:
		return current, true
	}
}

func updateNodeENOApi(ctx context.Context, k8s k8sClient.Client, nodeName, targetENOApi string) error {
	crNode := &networkv1beta1.Node{}
	if err := k8s.Get(ctx, k8sClient.ObjectKey{Name: nodeName}, crNode); err != nil {
		return fmt.Errorf("get node CR: %w", err)
	}

	if crNode.Annotations == nil {
		crNode.Annotations = make(map[string]string)
	}
	crNode.Annotations[types.ENOApi] = targetENOApi

	if err := k8s.Update(ctx, crNode); err != nil {
		return fmt.Errorf("update node CR: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Utility: Kubernetes client
// ---------------------------------------------------------------------------

func newK8sClient() (k8sClient.Client, error) {
	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = version.UA
	return k8sClient.New(restConfig, k8sClient.Options{
		Scheme: types.Scheme,
		Mapper: types.NewRESTMapper(),
	})
}

func parseNodeNames(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			result = append(result, trimmed)
		}
	}
	return result
}

func resolveTargetNodes(ctx context.Context, k8s k8sClient.Client, nodesCSV, selectorRaw string) ([]string, error) {
	explicitNodes := parseNodeNames(nodesCSV)
	if selectorRaw == "" {
		return explicitNodes, nil
	}

	selector, err := labels.Parse(selectorRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid --node-selector %q: %w", selectorRaw, err)
	}

	nodeList := &corev1.NodeList{}
	if err := k8s.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("list kubernetes nodes: %w", err)
	}

	matched := make(map[string]struct{}, len(nodeList.Items))
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if selector.Matches(labels.Set(node.Labels)) {
			matched[node.Name] = struct{}{}
		}
	}

	if len(explicitNodes) == 0 {
		result := make([]string, 0, len(matched))
		for name := range matched {
			result = append(result, name)
		}
		sort.Strings(result)
		return result, nil
	}

	result := make([]string, 0, len(explicitNodes))
	for _, name := range explicitNodes {
		if _, ok := matched[name]; ok {
			result = append(result, name)
		}
	}
	return result, nil
}
