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
- Needs to interpret **specific Kubernetes events** or **critical log messages** from Terway components.

Always assume the cluster is running Kubernetes and Terway is the CNI plugin.

## High-level troubleshooting flow

Follow this order to diagnose Terway issues efficiently:

1. **Verify Terway Component Health**
   - When CNI errors like "plugin not initialized" or "socket not found" occur, first check if Terway Pods are `Running`.
   - `kubectl get pods -n kube-system -l app=terway-eniip -o wide`
   - If a Pod is not `Running`, check its events and logs (`terway-init` and `terway` containers) using the patterns in **Step 1**.

2. **Gather Necessary Context (As needed)**
   - If the cause isn't obvious from Pod status, gather cluster and node configuration.
   - You can use the following scripts or run `kubectl` commands directly:
     - Cluster config: `./scripts/inspect-terway-cluster.sh`
     - Node config: `./scripts/inspect-terway-node.sh <node-name>`
     - Pod config: `./scripts/inspect-terway-pod.sh <namespace> <pod-name>`

3. **Use Kubernetes Events as the primary signal**
   - For any problematic Pod, inspect its Events first: `kubectl describe pod <pod> -n <ns>`.
   - Map Terway-specific event reasons (e.g., `AllocIPFailed`, `CniPodCreateError`) to likely causes.

4. **Inspect Terway IPAM / ENI controllers**
   - Depending on IPAM type (`crd` vs `default`), check relevant CRDs (`PodENI`, `Node`) and their Events.

5. **Deep Dive into Logs**
   - Use logs only when Events are missing or point to an ambiguous failure.

Keep answers structured: first restate what has been checked, then propose next verification steps.

## Step 1 – Terway and CNI initialization

1. **If the user reports "cni plugin not initialized" or "dial unix ... eni.socket: no such file or directory":**
   - **Phenomenon:** The CNI plugin cannot communicate with the Terway Daemon.
   - **Immediate Action:** Check the Terway Pod status: `kubectl get pods -n kube-system -l app=terway-eniip -o wide`.

2. **Diagnose by Pod Status and Log Patterns:**

   - **If the Pod is in `Init:Error`:** Inspect `terway-init` logs (`kubectl logs <pod> -n kube-system -c terway-init`).
     - **`exclusive eni mode changed`**:
       - *Explanation:* The label `k8s.aliyun.com/exclusive-mode-eni-type` was modified on an existing node. Exclusive mode only works for **newly created nodes**.
       - *Fix:* Revert the label or recreate the node.
     - **`get node ... error`**:
       - *Explanation:* Terway cannot reach the Kubernetes API to fetch node labels (network or RBAC issue).
     - **`unsupport kernel version, require >=5.10`**:
       - *Explanation:* The node's kernel is too old for the configured features.
     - **`failed process input`**:
       - *Explanation:* `/etc/eni/eni_conf` (from `eni-config` CM) is missing or invalid JSON.
     - **`mount failed`**:
       - *Explanation:* Failed to mount `bpffs` on `/sys/fs/bpf` (privilege or kernel support issue).
     - **`Init erdma driver` failure**:
       - *Explanation:* `modprobe erdma` failed on an ERDMA-enabled node.

   - **If the Pod is in `CrashLoopBackOff` or `Error`:** Inspect main container logs.
     - **Daemon (`terway`) Patterns:**
       - `error restart device plugin after kubelet restart`: Check permissions/mounts for `/var/lib/kubelet/device-plugins`.
       - `unable to set feature gates`: Invalid flag in `--feature-gates`.
       - `error create trunk eni`: OpenAPI failure during trunk ENI initialization (check Aliyun credentials/quota).
     - **Controlplane (`terway-controlplane`) Patterns:**
       - `failed to create controller`: Check RBAC permissions or CRD availability.
       - `failed to setup webhooks`: TLS certificate or WebhookConfiguration issues.

   - **If the Pod is `Running` but the socket error persists:**
     - Verify that the `var/run/eni/` directory is correctly shared between the host and the container via hostPath volume.

3. **Only after Terway is confirmed running on the node**, proceed to Pod create/delete failures and Events.

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
     - Message starts with `cmdAdd: error alloc ip`: Backend communication failure (daemon to controlplane or daemon internal).
     - Message: `eth0 config is missing`: Backend failed to return configuration for the primary interface.
     - Message contains OpenAPI errors:
       - `InvalidVSwitchID.IPNotEnough` / `QuotaExceeded.PrivateIPAddress`: VSwitch IP exhaustion.
       - `ErrEniPerInstanceLimitExceeded`: Node-level ENI quota reached.
   - **`AllocIPSucceed` (Normal, Pod)**
     - Message: `Alloc IP %s took %s`.
     - IP allocation succeeded; if the Pod still fails, the issue is likely **after** IP allocation (datapath setup, routes, iptables, etc.).
   - **`VirtualModeChanged` (Warning, Pod)**
     - Message: `IPVLan seems unavailable, use Veth instead`.
     - The node’s kernel version is likely below 4.19 or lacks required capabilities. IPVLan-based data plane acceleration cannot be enabled, but Pod creation is unaffected. Networking falls back to Veth mode safely.
   - **`CniPodCreateError` (Warning, Pod)**
     - From the controlplane Pod controller.
     - Message: `error parse pod annotation`: `k8s.aliyun.com/pod-networks` is malformed.
     - Message: `podNetworking is empty`: `k8s.aliyun.com/pod-networking` annotation is present but empty.
     - Message: `error get podNetworking %s`: The referenced `PodNetworking` CR is missing.
     - Message: `can not found available vSwitch for zone %s`: No available VSwitch in the current zone matching the selector.
   - **`CniPodDeleteError` (Warning, Pod)**
     - Failure in Pod delete cleanup.
   - **`CniCreateENIError` / `CniPodENIDeleteErr` (Warning, Pod)**
     - From the PodENI controller. Message contains specific OpenAPI errors or `rollbackErr`.

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
   - Terway emits events on this CR for ENI lifecycle and pool operations:
     - `CreateENIFailed`: Message: `Failed to create ENI type=%s vsw=%s: %v`. Check for OpenAPI errors like `InvalidVSwitchID.IPNotEnough`.
     - `AttachENIFailed`: Message: `trunk eni id not found` (agent not ready) or `trunk eni is not allowed for eniOnly pod` (scheduling/config mismatch).
     - `DeleteENIFailed`: Message: `Failed to delete ENI %s: %v`.
   - Node Conditions on Node CR:
     - `SufficientIP`: If `False`, reason is `IPResInsufficient`, meaning the node pool cannot be filled.

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
