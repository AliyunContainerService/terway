---
name: terway-e2e
description: |
  Run Terway E2E tests on an ACK cluster. Covers the full workflow:
  build image → create ACK cluster via Terraform → deploy Terway → run e2e tests → cleanup.

  Use this skill when:
  - Running any Terway E2E tests (Prefix, Connectivity, PodNetworking, SecurityGroup, MultiNetwork, etc.)
  - Creating ACK clusters for Terway testing
  - Building and deploying Terway images
  - Validating Terway features or troubleshooting E2E test failures
  - The user mentions "e2e", "terway test", "ack cluster", "prefix test", or any Terway feature validation
allowed-tools: Bash(ssh *), Bash(rsync *), Bash(git *), Bash(kubectl *), Bash(terraform *), Bash(helm *), Bash(make *), Bash(go test *)
---

# Terway E2E Test Workflow

Complete workflow for running Terway end-to-end tests on ACK clusters.

## Overview

Terway is Alibaba Cloud's CNI plugin for Kubernetes. E2E tests validate:

- **Prefix Mode**: /28 IPv4 or /80 IPv6 prefix allocation on ENIs
- **Connectivity**: Pod-to-Pod, Pod-to-Service, NodePort, LoadBalancer
- **PodNetworking**: Custom network configurations via CRD
- **SecurityGroup**: Security group isolation and rules
- **MultiNetwork**: Multiple network interfaces per pod
- **Trunk Mode**: Member ENI attachment via trunk
- **Exclusive ENI**: One pod per ENI mode

## Phase 1: Build & Push Image

> **Registry**: Replace `registry.cn-hangzhou.aliyuncs.com/acs` in all examples below
> with your own image registry namespace (e.g., `registry.cn-hangzhou.aliyuncs.com/<your-namespace>`).
> Make sure you have push access to that namespace before building.

```bash
# Record the image tag (git short commit)
IMAGE_TAG=$(git rev-parse --short HEAD)
echo "Image tag: $IMAGE_TAG"

# If running directly on the Linux dev server:
cd /root/opensource_terway
make BUILD_PLATFORMS=linux/amd64,linux/arm64 \
  REGISTRY=registry.cn-hangzhou.aliyuncs.com/acs \
  build-push-terway build-push-terway-controlplane

# If building from macOS/remote, sync first then build:
PROJECT_ROOT=$(git rev-parse --show-toplevel)
PROJECT_NAME=$(basename "$PROJECT_ROOT")
rsync -ar --exclude=bin/ --delete "$PROJECT_ROOT/" root@dev:"/root/$PROJECT_NAME/"
ssh dev "bash -l -c 'cd /root/$PROJECT_NAME && make BUILD_PLATFORMS=linux/amd64,linux/arm64 REGISTRY=registry.cn-hangzhou.aliyuncs.com/acs build-push-terway build-push-terway-controlplane'"
```

> **CRITICAL — Registry Endpoint**: Always use the **public** registry endpoint
> `registry.cn-hangzhou.aliyuncs.com` (NOT the VPC endpoint
> `registry-vpc.cn-hangzhou.aliyuncs.com`). The VPC endpoint causes
> `dial tcp ...: i/o timeout` from dev servers that are not inside the same VPC
> as the registry. The public endpoint works from any ECS in the same region.

## Phase 2: Create ACK Cluster via Terraform

```bash
cd hack/terraform/ack/

# Initialize (first time only)
terraform init

# Preview changes
terraform plan

# Create cluster (takes 10-20 minutes)
terraform apply -auto-approve

# Verify kubeconfig was generated
ls kubeconfig-tf-ack-*
export KUBECONFIG=$(ls kubeconfig-tf-ack-* | head -1)

# Verify cluster is accessible
kubectl get nodes
```

### Cluster Configuration

The Terraform configuration creates:

- **Default node pool**: Standard ECS nodes for shared ENI mode
- **Exclusive ENI node pool**: Nodes labeled for exclusive ENI mode
- **IP Prefix node pool**: Nodes labeled with `k8s.aliyun.com/ip-prefix=true` and `k8s.aliyun.com/ignore-by-terway=true` for dedicated prefix E2E testing. Initially isolated from terway scheduling; the E2E test removes the ignore label to bring them online.

> **IP Prefix Node Pool**: The `ip-prefix` node pool starts with `k8s.aliyun.com/ignore-by-terway=true` so terway does not schedule onto these nodes at cluster creation time. The E2E test setup flow (`setupIPPrefixNodes`) removes this label and waits for terway to become ready before running tests. Teardown restores the label.

Key configuration files:

- `terraform.tfvars`: Cluster configuration (region, instance types, CIDRs)
- `terraform_e2e.tf`: Main Terraform configuration

## Phase 3: Deploy Terway

```bash
cd hack/terraform/ack/

# Deploy with ENIMultiIP mode (required for Prefix/Trunk)
# --pull-policy Always ensures nodes re-pull the image on every restart (recommended for test builds)
./deploy-terway.sh \
  --tag <IMAGE_TAG> \
  --registry registry.cn-hangzhou.aliyuncs.com/acs \
  --daemon-mode ENIMultiIP \
  --pull-policy Always

# Or with Datapath V2 (kube-proxy replacement)
./deploy-terway.sh \
  --tag <IMAGE_TAG> \
  --registry registry.cn-hangzhou.aliyuncs.com/acs \
  --daemon-mode ENIMultiIP \
  --pull-policy Always \
  --enable-dp-v2 \
  --enable-network-policy

# Verify all pods are Running
kubectl get pods -n kube-system | grep -E 'terway'
# Expected:
#   terway-controlplane-xxx   2/2   Running
#   terway-eniip-xxx          2/2   Running  (one per node)
```

> **Note**: `deploy-terway.sh` reads cluster metadata (clusterID, VPC, security group,
> vSwitches) automatically from `terraform.tfstate`. No manual values.yaml editing needed.
> The generated `terway-values.yaml` is overwritten on each run.

### Deployment Options

| Option                    | Description                                                |
|---------------------------|------------------------------------------------------------|
| `--daemon-mode`           | `ENIMultiIP` (shared ENI) or `ENIDirectIP` (exclusive ENI) |
| `--ip-stack`              | `ipv4`, `ipv6`, or `dual`                                  |
| `--enable-dp-v2`          | Enable Datapath V2 (kube-proxy replacement)                |
| `--enable-network-policy` | Enable eBPF-based network policy                           |

## Phase 4: Run E2E Tests

### Test Categories

| Test              | File                                      | Description                                   |
|-------------------|-------------------------------------------|-----------------------------------------------|
| **Prefix**        | `prefix_basic_test.go`, `prefix_boundary_test.go`, `prefix_helpers_test.go` | Prefix allocation, boundary conditions, scale |
| **Prefix E2E**    | `prefix_e2e_test.go`                      | End-to-end prefix validation on dedicated node pool |
| **Prefix E2E**    | `prefix_e2e_test.go`                      | End-to-end Pod IP validation on dedicated prefix node pool |
| **Connectivity**  | `connective_test.go`, `connectivity_*.go` | Pod-to-Pod, Service connectivity              |
| **PodNetworking** | `pod_networking_test.go`                  | Custom network via CRD                        |
| **SecurityGroup** | `security_group_test.go`                  | SG isolation and rules                        |
| **MultiNetwork**  | `multi_network_test.go`                   | Multiple ENIs per pod                         |
| **Upgrade**       | `upgrade_test.go`                         | Upgrade compatibility                         |
| **ERDMA**         | `erdma/erdma_test.go`                     | ERDMA device support                          |

### Running Tests

```bash
# Set kubeconfig (e2e tests read ~/.kube/config by default)
cp hack/terraform/ack/kubeconfig-tf-ack-* ~/.kube/config
# OR: export KUBECONFIG=hack/terraform/ack/kubeconfig-tf-ack-*

# Run all prefix tests
cd /root/opensource_terway
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestPrefix' -region-id cn-hangzhou

# Run specific prefix suites
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestPrefix_Basic' -region-id cn-hangzhou

go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestPrefix_Boundary' -region-id cn-hangzhou

# Run connectivity tests
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestConnectivity' -region-id cn-hangzhou \
  -repo registry.cn-hangzhou.aliyuncs.com/acs
```

> **IMPORTANT**: Use `./tests` (not `./tests/...`) — the e2e tests live directly in
> the `tests` package. The `-region-id` flag is required for prefix tests (used to
> query ECS API). The test binary reads `~/.kube/config` automatically.

### Prefix Tests

```bash
# Basic prefix allocation
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestPrefix_Basic' -region-id cn-hangzhou

# Boundary conditions (max prefixes, exhaustion)
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestPrefix_Boundary' -region-id cn-hangzhou

# Dual stack and EFLO tests
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestPrefix_DualStack|TestPrefix_EFLO' -region-id cn-hangzhou

# State machine and scale tests
go test -v -count=1 -timeout 120m -tags e2e ./tests \
  -run 'TestPrefix_State|TestPrefix_Scale' -region-id cn-hangzhou

# Prefix E2E tests (dedicated node pool, end-to-end pod IP validation)
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestPrefix_E2E' -region-id cn-hangzhou
```

#### Prefix Node Capacity Reference (ECS g7ne)

| Instance     | ENI adapters | Prefixes/ENI | Max prefix capacity |
|--------------|--------------|--------------|---------------------|
| g7ne.2xlarge | 6            | 14           | **84**              |
| g7ne.4xlarge | 8            | 14           | **112**             |

#### Known Prefix Test Behavior

- **Cleanup timeout warnings** (`resetNodePrefixState`): Between tests, the 3-minute cleanup
  wait sometimes expires while `Deleting` prefixes are still being unassigned from ENIs via
  the ECS API. This is **expected** — tests emit a warning and proceed. The underlying
  `IPPrefixStatusDeleting` controller handles the ECS API call asynchronously.

- **`TestPrefix_Boundary_MaxValue`**: This test requests more prefixes than node capacity
  (e.g., 134 requested, 84 max). The test verifies no over-allocation occurs. If the system
  hasn't reached max capacity within the wait window, the test accepts any count `≤ maxCapacity`
  and still passes.

- **`TestPrefix_Boundary_ENICapacity`**: Requests 17 prefixes when max-per-ENI is 14, verifying
  the system spreads them across 2+ ENIs.

### Connectivity Tests

```bash
# All connectivity tests
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestConnectivity' -region-id cn-hangzhou \
  -repo registry.cn-hangzhou.aliyuncs.com/acs

# Specific scenarios
go test -v -count=1 -timeout 60m -tags e2e ./tests \
  -run 'TestConnectivity_NodePort' -region-id cn-hangzhou \
  -repo registry.cn-hangzhou.aliyuncs.com/acs
```

## Phase 5: Issue Discovery & Analysis

When tests fail or when validating feature implementations, analyze both the test code and the implementation to
discover issues.

### Check Test Implementation

1. **Test Prerequisites**: Check if tests verify cluster configuration correctly

   ```go
   // Example: Check IPAM type
   func checkIPAMType(t *testing.T) {
       cm := &corev1.ConfigMap{}
       err := client.Resources().Get(ctx, "eni-config", "kube-system", cm)
       // Verify ipam_type is "crd" for centralized IPAM
   }
   ```

2. **Node Selection**: Tests should use proper node affinity

   ```go
   // Tests need correct node type selection
   nodeInfo, _ := DiscoverNodeTypesWithCapacity(ctx, client)
   if len(nodeInfo.ECSSharedENINodes) == 0 {
       t.Skip("No ECS Shared ENI nodes available")
   }
   ```

3. **Resource Cleanup**: Verify tests clean up resources properly
    - PodENI CRs
    - PodNetworking CRs
    - ConfigMaps (Dynamic Config)
    - Node labels

### Check Feature Requirements

1. **Version Requirements**: Some features require specific Terway versions

   ```go
   // Example from tests
   if !RequireTerwayVersion("v1.17.0") {
       t.Skip("Requires Terway >= v1.17.0")
   }
   ```

2. **Cluster Configuration**:
    - Centralized IPAM (`ipam_type: crd`) required for Prefix
    - Trunk mode enabled for member ENI tests
    - Dual-stack enabled for IPv6 tests

3. **Node Labels**:
    - `terway-config: <configmap-name>` for Dynamic Config
    - `ip-prefix: "true"` for prefix mode
    - `k8s.aliyun.com/exclusive-mode-eni-type` for exclusive ENI

### Common Issues

| Issue                                          | Cause                                                | Solution                                                                              |
|------------------------------------------------|------------------------------------------------------|---------------------------------------------------------------------------------------|
| `dial tcp ...: i/o timeout` during push        | Using VPC registry endpoint from wrong network       | Switch to public endpoint: `registry.cn-hangzhou.aliyuncs.com` (NOT `registry-vpc.*`) |
| Tests skip with "ipam type is not crd"         | Cluster uses non-centralized IPAM                    | Ensure `ipam_type: crd` in eni-config                                                 |
| Tests skip with "No ECS Shared ENI nodes"      | Only Lingjun/EFLO nodes present                      | Add standard ECS nodes                                                                |
| Prefix not allocated                           | Missing node labels                                  | Add `ip-prefix: "true"` and `terway-config` labels                                    |
| Pod stuck in Creating                          | ENI quota exceeded                                   | Check ENI quota or use prefix mode                                                    |
| Connectivity failures                          | Security group rules                                 | Check SG allows pod traffic                                                           |
| `resetNodePrefixState` cleanup timeout warning | `Deleting` prefixes take >3m to unassign via ECS API | **Expected behavior** — test continues; no fix needed                                 |
| Nodes in NotReady state after cluster creation | CNI not yet installed                                | Deploy Terway via `deploy-terway.sh` first                                            |

### Analyzing Test Failures

1. **Check Pod Status**:

   ```bash
   kubectl get pods -n <namespace> -o wide
   kubectl describe pod <pod-name> -n <namespace>
   kubectl logs <pod-name> -n <namespace>
   ```

2. **Check Terway Logs**:

   ```bash
   kubectl logs -n kube-system <terway-eniip-pod> -c terway
   kubectl logs -n kube-system <terway-controlplane-pod>
   ```

3. **Check CR Status**:

   ```bash
   kubectl get podeni -A
   kubectl describe podeni <name> -n <namespace>
   kubectl get podnetworking
   kubectl describe podnetworking <name>
   ```

4. **Check Node Resources**:

   ```bash
   kubectl describe node <node-name> | grep -A 10 "Allocatable"
   # Check for aliyun/eni, aliyun/member-eni resources
   ```

## Phase 6: Cleanup

```bash
cd hack/terraform/ack/
terraform destroy -auto-approve
```

## Key Files Reference

| File                                  | Purpose                                   |
|---------------------------------------|-------------------------------------------|
| `hack/terraform/ack/`                 | Terraform configs for ACK cluster         |
| `hack/terraform/ack/deploy-terway.sh` | Helm-based Terway deployment script       |
| `tests/main_test.go`                  | Test initialization and cluster discovery |
| `tests/utils_test.go`                 | Test utilities (Pod, Service builders)    |
| `tests/node_utils_test.go`            | Node type discovery and capacity checks   |
| `tests/prefix_*.go`                   | Prefix mode tests                         |
| `tests/connective*.go`                | Connectivity tests                        |
| `tests/pod_networking_test.go`        | PodNetworking CRD tests                   |
| `tests/security_group_test.go`        | Security group tests                      |
| `types/daemon/dynamicconfig.go`       | Dynamic Config implementation             |

## Node Types

| Type                    | Label                                            | Description                         |
|-------------------------|--------------------------------------------------|-------------------------------------|
| `ecs-shared-eni`        | (default)                                        | Standard ECS nodes, shared ENI mode |
| `ecs-exclusive-eni`     | `k8s.aliyun.com/exclusive-mode-eni-type=eniOnly` | ECS nodes, exclusive ENI            |
| `ecs-ip-prefix`         | `k8s.aliyun.com/ip-prefix=true`                  | ECS nodes, dedicated IP Prefix E2E pool |
| `lingjun-shared-eni`    | `alibabacloud.com/lingjun-worker=true`           | Lingjun/EFLO nodes, shared ENI      |
| `lingjun-exclusive-eni` | Both labels above                                | Lingjun nodes, exclusive ENI        |

## Test Flags

| Flag                  | Default                                        | Description                   |
|-----------------------|------------------------------------------------|-------------------------------|
| `-repo`               | `registry.cn-hangzhou.aliyuncs.com/build-test` | Image registry                |
| `-timeout`            | `2m`                                           | Default test timeout          |
| `-enable-trunk`       | `true`                                         | Enable trunk mode tests       |
| `-region-id`          | `cn-hangzhou`                                  | Alibaba Cloud region          |
| `-vswitch-ids`        | `""`                                           | Additional vSwitch IDs        |
| `-security-group-ids` | `""`                                           | Additional security group IDs |
