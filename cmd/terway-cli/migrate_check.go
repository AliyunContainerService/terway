package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdkErrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	corev1 "k8s.io/api/core/v1"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"

	aliClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types"
)

type checkItem struct {
	name string
	fn   func() (bool, []string)
}

type checkResult struct {
	name   string
	passed bool
	msgs   []string
}

type migrationChecker struct {
	k8s         k8sClient.Client
	ecs         aliClient.ECS
	efloControl aliClient.EFLOControl
}

func (c *migrationChecker) preCheckItems(ctx context.Context, nodeName, instanceID, targetENOApi, exclusiveMode string) []checkItem {
	items := []checkItem{
		{"Creating pods", func() (bool, []string) { return checkCreatingPods(ctx, c.k8s, nodeName) }},
		{"Shared ENI state", func() (bool, []string) { return checkSharedENIState(ctx, c.k8s, nodeName) }},
		{"OpenAPI prerequisite", func() (bool, []string) { return checkOpenAPIPrerequisite(ctx, c.ecs, instanceID) }},
		{"ENOApi consistency", func() (bool, []string) {
			return checkENOApiConsistency(ctx, c.efloControl, instanceID, targetENOApi)
		}},
	}

	if exclusiveMode == string(types.ExclusiveENIOnly) {
		items = append(items[:2], append([]checkItem{
			{"Exclusive ENI state", func() (bool, []string) { return checkExclusiveENIState(ctx, c.k8s, nodeName) }},
		}, items[2:]...)...)
	}

	return items
}

func (c *migrationChecker) postCheckItems(ctx context.Context, nodeName, targetENOApi, exclusiveMode string) []checkItem {
	items := []checkItem{
		{"Creating pods", func() (bool, []string) { return checkCreatingPods(ctx, c.k8s, nodeName) }},
		{"Shared ENI state", func() (bool, []string) { return checkSharedENIState(ctx, c.k8s, nodeName) }},
		{"Annotation persisted", func() (bool, []string) {
			return checkAnnotationPersisted(ctx, c.k8s, nodeName, targetENOApi)
		}},
	}

	if exclusiveMode == string(types.ExclusiveENIOnly) {
		items = append(items[:2], append([]checkItem{
			{"Exclusive ENI state", func() (bool, []string) { return checkExclusiveENIState(ctx, c.k8s, nodeName) }},
		}, items[2:]...)...)
	}

	return items
}

func runChecks(items []checkItem) (bool, []checkResult) {
	allPassed := true
	results := make([]checkResult, 0, len(items))
	for _, item := range items {
		ok, msgs := item.fn()
		results = append(results, checkResult{name: item.name, passed: ok, msgs: msgs})
		if !ok {
			allPassed = false
		}
	}
	return allPassed, results
}

// checkCreatingPods verifies no pods are in a transient creating state on the node.
func checkCreatingPods(ctx context.Context, k8s k8sClient.Client, nodeName string) (bool, []string) {
	podList := &corev1.PodList{}
	if err := k8s.List(ctx, podList, k8sClient.MatchingFields{"spec.nodeName": nodeName}); err != nil {
		return false, []string{fmt.Sprintf("failed to list pods on node %s: %v", nodeName, err)}
	}

	var problems []string
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != nodeName {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if pod.Status.Phase == corev1.PodPending {
			problems = append(problems, fmt.Sprintf("pod %s/%s is Pending", pod.Namespace, pod.Name))
			continue
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil &&
				(cs.State.Waiting.Reason == "ContainerCreating" || cs.State.Waiting.Reason == "PodInitializing") {
				problems = append(problems, fmt.Sprintf("pod %s/%s container %s is %s",
					pod.Namespace, pod.Name, cs.Name, cs.State.Waiting.Reason))
			}
		}
		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.State.Waiting != nil &&
				(cs.State.Waiting.Reason == "ContainerCreating" || cs.State.Waiting.Reason == "PodInitializing") {
				problems = append(problems, fmt.Sprintf("pod %s/%s init-container %s is %s",
					pod.Namespace, pod.Name, cs.Name, cs.State.Waiting.Reason))
			}
		}
	}

	if len(problems) > 0 {
		return false, problems
	}
	return true, nil
}

// checkSharedENIState verifies all shared ENIs on the Node CR are in a stable state.
func checkSharedENIState(ctx context.Context, k8s k8sClient.Client, nodeName string) (bool, []string) {
	crNode := &networkv1beta1.Node{}
	if err := k8s.Get(ctx, k8sClient.ObjectKey{Name: nodeName}, crNode); err != nil {
		return false, []string{fmt.Sprintf("failed to get network Node CR: %v", err)}
	}

	var problems []string

	for eniID, nic := range crNode.Status.NetworkInterfaces {
		if nic == nil {
			continue
		}

		if nic.Status != aliClient.ENIStatusInUse {
			problems = append(problems, fmt.Sprintf("ENI %s status=%s (expected InUse)", eniID, nic.Status))
		}

		for ipAddr, ip := range nic.IPv4 {
			if ip == nil {
				continue
			}
			if ip.Status != networkv1beta1.IPStatusValid {
				problems = append(problems, fmt.Sprintf("ENI %s IPv4 %s status=%s", eniID, ipAddr, ip.Status))
			}
		}
		for ipAddr, ip := range nic.IPv6 {
			if ip == nil {
				continue
			}
			if ip.Status != networkv1beta1.IPStatusValid {
				problems = append(problems, fmt.Sprintf("ENI %s IPv6 %s status=%s", eniID, ipAddr, ip.Status))
			}
		}
		for _, prefix := range nic.IPv4Prefix {
			if prefix.Status != networkv1beta1.IPPrefixStatusValid {
				problems = append(problems, fmt.Sprintf("ENI %s IPv4Prefix %s status=%s", eniID, prefix.Prefix, prefix.Status))
			}
		}
		for _, prefix := range nic.IPv6Prefix {
			if prefix.Status != networkv1beta1.IPPrefixStatusValid {
				problems = append(problems, fmt.Sprintf("ENI %s IPv6Prefix %s status=%s", eniID, prefix.Prefix, prefix.Status))
			}
		}
	}

	if len(problems) > 0 {
		return false, problems
	}
	return true, nil
}

// checkExclusiveENIState verifies all NetworkInterface CRs on this node are in a stable phase.
func checkExclusiveENIState(ctx context.Context, k8s k8sClient.Client, nodeName string) (bool, []string) {
	niList := &networkv1beta1.NetworkInterfaceList{}
	if err := k8s.List(ctx, niList); err != nil {
		return false, []string{fmt.Sprintf("failed to list NetworkInterface CRs: %v", err)}
	}

	var problems []string

	for i := range niList.Items {
		ni := &niList.Items[i]
		if ni.Status.NodeName != nodeName {
			continue
		}

		phase := ni.Status.Phase
		if phase != networkv1beta1.ENIPhaseBind && phase != networkv1beta1.ENIPhaseUnbind {
			phaseStr := string(phase)
			if phaseStr == "" {
				phaseStr = "Initial"
			}
			problems = append(problems, fmt.Sprintf("NetworkInterface %s phase=%s", ni.Name, phaseStr))
		}
	}

	if len(problems) > 0 {
		return false, problems
	}
	return true, nil
}

// checkOpenAPIPrerequisite verifies the instance has migration tags and no Primary ENI.
// Conditions:
//  1. Must NOT have a Primary ENI (those are new ECS-linked instances, not migration targets)
//  2. Must have at least one ENI with both tags: leni_primary=true AND support_eni=true
func checkOpenAPIPrerequisite(ctx context.Context, ecsClient aliClient.ECS, instanceID string) (bool, []string) {
	enis, err := ecsClient.DescribeNetworkInterface2(ctx, &aliClient.DescribeNetworkInterfaceOptions{
		InstanceID: &instanceID,
	})
	if err != nil {
		return false, []string{fmt.Sprintf("failed to describe ENIs for instance %s: %v requestID=%s",
			instanceID, err, extractRequestID(err))}
	}

	if len(enis) == 0 {
		return false, []string{fmt.Sprintf("no ENIs found for instance %s", instanceID)}
	}

	hasPrimary, hasMigrationTags := aliClient.ClassifyENILinkCapability(enis)

	var problems []string
	if hasPrimary {
		problems = append(problems, "instance has a Primary ENI (already ECS-linked, not a migration target)")
	}
	if !hasMigrationTags {
		summaries := make([]string, 0, len(enis))
		for _, eni := range enis {
			summaries = append(summaries, formatENISummary(eni))
		}
		problems = append(problems, appendOpenAPIDetail(
			"no ENI found with tags leni_primary=true AND acs:ecs:support_eni=true",
			nil,
			summaries,
		))
	}

	return len(problems) == 0, problems
}

// extractRequestID returns the request ID from an Alibaba Cloud SDK error.
// Falls back to Recommend() when RequestId() is empty, since some SDK constructors
// store the request ID in the recommend field.
func extractRequestID(err error) string {
	var se *sdkErrors.ServerError
	if errors.As(err, &se) {
		if id := se.RequestId(); id != "" {
			return id
		}
		// NewServerError stores manually-passed IDs in comment; Recommend comes from response body.
		if id := se.Recommend(); id != "" {
			return id
		}
		return se.Comment()
	}
	return ""
}

// formatENISummary returns a compact representation of an ENI for diagnostic output.
func formatENISummary(eni *aliClient.NetworkInterface) string {
	var sb strings.Builder
	sb.WriteString(eni.NetworkInterfaceID)
	fmt.Fprintf(&sb, ",type=%s,status=%s,deviceIndex=%d", eni.Type, eni.Status, eni.DeviceIndex)
	for _, tag := range eni.Tags {
		fmt.Fprintf(&sb, ",%s=%s", tag.TagKey, tag.TagValue)
	}
	return sb.String()
}

// appendOpenAPIDetail appends requestIDs and ENI summaries (capped at 5) to msg.
func appendOpenAPIDetail(msg string, requestIDs []string, eniSummaries []string) string {
	var sb strings.Builder
	sb.WriteString(msg)
	if len(requestIDs) > 0 {
		fmt.Fprintf(&sb, " requestIDs=%s", strings.Join(requestIDs, ","))
	}
	if len(eniSummaries) > 0 {
		const maxDisplay = 5
		displayed := eniSummaries
		extra := 0
		if len(eniSummaries) > maxDisplay {
			displayed = eniSummaries[:maxDisplay]
			extra = len(eniSummaries) - maxDisplay
		}
		s := strings.Join(displayed, "; ")
		if extra > 0 {
			s += fmt.Sprintf("; ...(%+d more)", extra)
		}
		fmt.Fprintf(&sb, " observedENIs=%s", s)
	}
	return sb.String()
}

// checkENOApiConsistency validates that the CLI's target ENOApi matches what the controller
// would decide based on the instance's hardware profile (Adapters vs HighDenseQuantity).
func checkENOApiConsistency(ctx context.Context, efloControl aliClient.EFLOControl, instanceID, targetENOApi string) (bool, []string) {
	describeNodeReq := &aliClient.DescribeNodeRequestOptions{
		NodeID: &instanceID,
	}
	nodeResp, err := efloControl.DescribeNode(ctx, describeNodeReq)
	if err != nil {
		return false, []string{fmt.Sprintf("failed to describe node %s: %v", instanceID, err)}
	}

	limit, err := aliClient.GetLimitProvider().GetLimit(efloControl, nodeResp.NodeType)
	if err != nil {
		return false, []string{fmt.Sprintf("failed to get limits for node type %s: %v", nodeResp.NodeType, err)}
	}

	var controllerTarget string
	if limit.Adapters <= 1 && limit.HighDenseQuantity > 0 {
		controllerTarget = types.APIEcsHDeni
	} else {
		controllerTarget = types.APIEcs
	}

	if targetENOApi != controllerTarget {
		return false, []string{fmt.Sprintf(
			"CLI target %q conflicts with controller decision %q (adapters=%d, highDenseQuantity=%d)",
			targetENOApi, controllerTarget, limit.Adapters, limit.HighDenseQuantity,
		)}
	}

	return true, nil
}

// checkAnnotationPersisted verifies the Node CR annotation still matches the migration target
// after the controller has had a chance to reconcile.
func checkAnnotationPersisted(ctx context.Context, k8s k8sClient.Client, nodeName, targetENOApi string) (bool, []string) {
	crNode := &networkv1beta1.Node{}
	if err := k8s.Get(ctx, k8sClient.ObjectKey{Name: nodeName}, crNode); err != nil {
		return false, []string{fmt.Sprintf("failed to get network Node CR: %v", err)}
	}

	actual := crNode.Annotations[types.ENOApi]
	if actual != targetENOApi {
		return false, []string{fmt.Sprintf(
			"annotation %s=%q, expected %q (controller may have overwritten)",
			types.ENOApi, actual, targetENOApi,
		)}
	}

	return true, nil
}
