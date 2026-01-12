---
name: terway-troubleshooting
description: Troubleshoot Terway CNI issues in Kubernetes using Kubernetes events and Terway logs. Use when diagnosing "cni plugin not initialized", Pod create/delete failures, or ENI/IPAM problems in Terway (centralized or non-centralized IPAM).
---

# Terway Troubleshooting SOP

## When to use this Skill

Use this Skill whenever the user:

- Reports **"cni plugin not initialized"** or similar CNI errors on nodes
- Reports **Pod creation or deletion failures** in a cluster using Terway as the CNI
- Suspects **ENI/IPAM/resource issues** related to Terway (centralized or non-centralized)

Always assume the cluster is running Kubernetes and Terway is the CNI plugin.

## High-level troubleshooting flow

Follow this order unless the user has already done some steps:

1. **Gather cluster-level configuration first**
   - Run the cluster configuration inspection script to understand the environment:

     ```bash
     ./scripts/inspect-terway-cluster.sh
     ```

   - This provides Terway version, IPAM type (centralized vs non-centralized), service CIDR, kube-proxy mode, and key Terway feature flags.
   - Use this information to guide the rest of the troubleshooting flow.

2. **Check Terway components health**
   - Verify Terway DaemonSet Pod is created and running on the node.
   - If using centralized IPAM (identified in step 1), also verify the Terway controlplane Pod.

3. **Inspect the problematic Pod and Node configuration**
   - Once you've identified the problematic Pod and its Node:
     - Run `./scripts/inspect-terway-pod.sh <namespace> <pod-name>` to check Pod-level config (hostNetwork, pod-eni, annotation-based config source).
     - Run `./scripts/inspect-terway-node.sh <node-name>` to check Node-level config (ENI mode, dynamic config, LingJun status, ignore-by-terway, no-kube-proxy).

4. **Use Kubernetes Events as the primary signal**
   - For any problematic Pod, inspect its Events first.
   - Map Terway-specific event reasons to likely causes and next checks.

5. **Inspect Terway IPAM / ENI controllers**
   - Depending on centralized vs non-centralized IPAM (from step 1), check relevant CRDs and their Events.

6. **Only then, inspect logs**
   - Use Terway daemon and controlplane logs to deepen analysis when Events are missing or unclear.

Keep answers structured: first restate what has been checked, then propose next verification steps.

## Step 1 – Terway and CNI initialization

1. **If the user reports "cni plugin not initialized" or similar:**
   - Do **not** immediately blame Terway IPAM logic.
   - First ensure Terway Pods (daemon and, if present, controlplane) are **created, scheduled, and running** on the node.
   - If Terway Pod is missing:
     - Ask the user to check Mutating/Validating Webhooks, runtime, and CNI configuration (kubelet cni dirs, etc.).
   - If Terway Pod is CrashLooping:
     - Ask for the Pod description/logs and help debug that before going to Pod-level network issues.

2. **Only after Terway is confirmed running on the node**, proceed to Pod create/delete failures and Events.

## Step 2 – Always start from Kubernetes Events

For any Pod with network-related failures:

1. **Inspect Pod Events**
   - Instruct the user to run `kubectl describe pod <pod> -n <ns>` and paste relevant Events.
   - Focus on Terway-related reasons (case-sensitive):
     - `AllocIPFailed` (Warning, Pod)
     - `AllocIPSucceed` (Normal, Pod)
     - `VirtualModeChanged` (Warning, Pod)
     - `CniPodCreateError` (Warning, Pod)
     - `CniPodDeleteError` (Warning, Pod)
     - `CniCreateENIError` (Warning, Pod)
     - `CniPodENIDeleteErr` (Warning, Pod)

2. **Interpret common Pod event reasons**
   - **`AllocIPFailed` (Warning, Pod)**
     - Means CNI ADD reached Terway backend but IP allocation failed.
     - Likely causes:
       - ENI quota exhausted (`ErrEniPerInstanceLimitExceeded`).
       - VSwitch IP exhaustion (`InvalidVSwitchID.IPNotEnough`, `QuotaExceeded.PrivateIPAddress`).
       - OpenAPI permission or configuration errors.
     - Next checks:
       - Node-level Events on the Node and Node CR (if centralized IPAM).
       - Terway daemon logs around the same time.
   - **`AllocIPSucceed` (Normal, Pod)**
     - IP allocation succeeded; if the Pod still fails, the issue is likely **after** IP allocation (datapath setup, routes, iptables, etc.).
   - **`VirtualModeChanged` (Warning, Pod)**
     - IPvlan datapath is unavailable, Terway falls back to veth.
     - Usually not fatal but indicates kernel or capability problems on the node.
   - **`CniPodCreateError` (Warning, Pod)**
     - From the controlplane Pod controller. Means Pod create path failed (annotation parsing, PodENI/PodNetworking, vswitch selection, etc.).
     - Ask for the full event message; it usually contains the specific error string.
   - **`CniPodDeleteError` (Warning, Pod)**
     - Failure in Pod delete cleanup (PodENI/ENI status or detach). Investigate PodENI and Node CR status.
   - **`CniCreateENIError` / `CniPodENIDeleteErr` (Warning, Pod)**
     - Emitted by the PodENI controller when ENI creation/deletion for the Pod fails. Use PodENI CR Events for more details.

3. **If no Terway-specific Events are present**
   - Confirm that the Pod is scheduled to a node where Terway is running.
   - Then move to node-level and CRD-level Events.

## Step 3 – Node and Node CR Events

Distinguish between:

- **Kubernetes Node object** (`corev1.Node`).
- **Terway Node CRD** (`network.alibabacloud.com/v1beta1 Node`) used in centralized IPAM.

1. **On the Kubernetes Node (`corev1.Node`)**
   - Important Terway-related event reasons:
     - `AllocIPFailed` (Warning, Node)
       - From local IPAM; indicates ENI/IP issues at node level.
     - `ConfigError` (Warning, Node)
       - From Terway node controllers when `eni-config` or node capabilities are invalid.
   - Use these to distinguish between misconfiguration vs. resource exhaustion.

2. **On the Terway Node CRD (centralized IPAM)**
   - When centralized IPAM is enabled, a `Node` CR under `network.alibabacloud.com` exists.
   - Terway emits events on this CR for ENI lifecycle and pool operations, using reasons defined in `types/k8s.go`, such as:
     - `CreateENISucceed` / `CreateENIFailed`
     - `AttachENISucceed` / `AttachENIFailed`
     - `DetachENISucceed` / `DetachENIFailed`
     - `DeleteENISucceed` / `DeleteENIFailed`
   - Use these events to answer questions like:
     - Is the IP pool being warmed correctly?
     - Are new ENIs failing to create because of OpenAPI errors or configuration?

3. **Link Node events to Pod failures**
   - If Pods report `AllocIPFailed` or `CniPodCreateError`, check whether the corresponding Node / Node CR shows ENI/IPAM failures.
   - Use that correlation to explain whether the problem is capacity, config, or bug.

## Step 4 – Centralized vs non-centralized IPAM behavior

When reasoning about Terway behavior, always clarify which IPAM mode is in use.

1. **Detect mode from context**
   - Centralized IPAM indicators:
     - Presence of Terway controlplane deployment.
     - CRDs like `podenis.network.alibabacloud.com`, `nodes.network.alibabacloud.com`, `podnetworkings.network.alibabacloud.com`.
     - Helm/config flag `centralizedIPAM: true` or controlplane config with `CentralizedIPAM` set.
   - Non-centralized/local IPAM indicators:
     - IPAM type in `eni-config` is `default`.
     - Node-local IPAM logic in the daemon is responsible for ENI/IP management.

2. **If centralized IPAM**
   - In addition to Pod and Node events, always consider:
     - **PodENI CR** (per-pod ENI and IP state): events like `CreateENIFailed`, `AttachENIFailed`, `UpdatePodENIFailed`.
     - **Node CR**: ENI pool and warmup behavior.
     - **PodNetworking CR**: Events `SyncPodNetworkingSucceed/Failed` when syncing vswitch lists.
   - For Pod failures:
     - Check Pod Events (Cni* reasons) → PodENI Events → Node CR Events → controlplane logs.

3. **If non-centralized IPAM**
   - Focus on:
     - Node Events (`AllocIPFailed`, `ConfigError`).
     - `eni-config` ConfigMap correctness (vswitches, security groups, ip_stack, trunk/erdma flags, etc.).
     - Terway daemon logs on the affected node.

## Step 5 – Using logs only when Events are insufficient

1. **When to move to logs**
   - Events point to a failure but not the exact cause (e.g., only `AllocIPFailed` without OpenAPI error details).
   - There are **no** Terway Events on the relevant Pod/Node/CR, but the behavior clearly involves Terway.

2. **Which logs to inspect**
   - **Terway daemon logs** on the affected node:
     - Look for:
       - The Pod name / namespace.
       - OpenAPI errors (quota, IP shortage, permission issues).
       - Internal errors in ENI/route/datapath setup.
   - **Terway controlplane logs** (centralized IPAM):
     - Look for:
       - Errors in Pod controller, PodENI controller, Node controller.
       - PodNetworking sync failures.

3. **How to combine logs with Events**
   - Use Event timestamps and reasons as an index into the logs.
   - Explain to the user:
     - Which event indicates the failure.
     - Which log line confirms the root cause.

## Utility scripts

### Cluster-level configuration

Before starting troubleshooting, gather cluster-wide Terway configuration:

```bash
./scripts/inspect-terway-cluster.sh
```

This script inspects:

- **Terway version** from the `terway-eniip` DaemonSet image tag
- **Service CIDR** and **IP stack** from `ack-cluster-profile` ConfigMap
- **Kube-proxy mode** (iptables/ipvs) and **cluster CIDR** from `kube-proxy-worker` ConfigMap
- **IPAM type** (`crd` for centralized, `default` for non-centralized) from `eni-config` ConfigMap
- **Terway feature flags**: `enable_eni_trunking`, `enable_erdma`, `vswitch_selection_policy`, `max_pool_size`, `min_pool_size`, etc.

Use this information to determine whether centralized IPAM is enabled and which Terway features are active. This guides the rest of the troubleshooting flow.

### Node-level configuration

To inspect Terway-related node configuration for a problematic Pod, first identify the Pod's node (for example via `kubectl get pod -o wide`). Then, from the repository root, run:

```bash
./scripts/inspect-terway-node.sh <node-name>
```

This prints ENI mode (shared vs exclusive), node-level dynamic config (`terway-config`), LingJun node flags, `k8s.aliyun.com/ignore-by-terway` and `k8s.aliyun.com/no-kube-proxy` labels, and the ENO API type from the `nodes.network.alibabacloud.com` CR. Use this information as input to the troubleshooting steps above when you have located the Pod's node.

### Pod-level configuration

To inspect Terway-related Pod configuration, run:

```bash
./scripts/inspect-terway-pod.sh <namespace> <pod-name>
```

This checks:

- Whether the Pod uses `hostNetwork` (if true, Terway CNI does not process it).
- Whether the Pod has `k8s.aliyun.com/pod-eni: "true"` annotation (indicating trunk/exclusive ENI mode).
- Which annotation-based config source is used, following the webhook priority order:
  1. `k8s.aliyun.com/pod-networks` (explicit pod-networks config)
  2. `k8s.aliyun.com/pod-networks-request` (pod-networks-request config)
  3. `k8s.aliyun.com/pod-networking` (matched PodNetworking resource)
  4. Fallback to `eni-config` default on eth0 if none of the above are set.

Use this to determine if the Pod should be managed by Terway, whether it uses PodENI, and which configuration source drives its ENI/IP allocation.

## Response style guidelines

When this Skill is active:

- **Always start from Events** when diagnosing Pod or node-level issues; do not jump straight into logs unless Events are missing.
- **Reference concrete Terway event reasons** (e.g., `AllocIPFailed`, `CniPodCreateError`, `CreateENIFailed`) and explain what they mean.
- **Ask for specific artifacts** when needed:
  - `kubectl describe pod` output for the problematic Pod.
  - Node and Node CR describe output when centralized IPAM is used.
  - Excerpts from Terway daemon/controlplane logs around the relevant time.
- Keep answers structured and concise, but be explicit about next steps (what to inspect next and why).
- Clearly distinguish between **configuration issues**, **resource exhaustion/quota**, and **potential Terway bugs** based on Events and logs.
