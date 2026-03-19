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

```bash
# Sync local code to remote dev server
PROJECT_ROOT=$(git rev-parse --show-toplevel)
PROJECT_NAME=$(basename "$PROJECT_ROOT")
rsync -ar --exclude=bin/ --delete "$PROJECT_ROOT/" root@dev:"/root/$PROJECT_NAME/"

# Build and push on remote Linux dev server
ssh dev "bash -l -c 'cd /root/$PROJECT_NAME && make BUILD_PLATFORMS=linux/amd64 REGISTRY=registry.cn-hangzhou.aliyuncs.com/l1b0k build-push'"

# Record the image tag (git short commit)
IMAGE_TAG=$(git rev-parse --short HEAD)
echo "Image tag: $IMAGE_TAG"
```

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
- **Prefix test node pool**: Nodes labeled for prefix mode testing

Key configuration files:
- `terraform.tfvars`: Cluster configuration (region, instance types, CIDRs)
- `terraform_e2e.tf`: Main Terraform configuration

## Phase 3: Deploy Terway

```bash
cd hack/terraform/ack/

# Deploy with ENIMultiIP mode (required for Prefix/Trunk)
./deploy-terway.sh \
  --tag <IMAGE_TAG> \
  --registry registry.cn-hangzhou.aliyuncs.com/l1b0k \
  --daemon-mode ENIMultiIP \
  --ip-stack ipv4

# Or with Datapath V2 (kube-proxy replacement)
./deploy-terway.sh \
  --tag <IMAGE_TAG> \
  --registry registry.cn-hangzhou.aliyuncs.com/l1b0k \
  --daemon-mode ENIMultiIP \
  --enable-dp-v2 \
  --enable-network-policy

# Verify all pods are Running
kubectl get pods -n kube-system | grep -E 'terway'
# Expected:
#   terway-controlplane-xxx   2/2   Running
#   terway-eniip-xxx          2/2   Running  (one per node)
```

### Deployment Options

| Option | Description |
|--------|-------------|
| `--daemon-mode` | `ENIMultiIP` (shared ENI) or `ENIDirectIP` (exclusive ENI) |
| `--ip-stack` | `ipv4`, `ipv6`, or `dual` |
| `--enable-dp-v2` | Enable Datapath V2 (kube-proxy replacement) |
| `--enable-network-policy` | Enable eBPF-based network policy |

## Phase 4: Run E2E Tests

### Test Categories

| Test | File | Description |
|------|------|-------------|
| **Prefix** | `prefix_*.go` | Prefix allocation, boundary conditions, scale |
| **Connectivity** | `connective_test.go`, `connectivity_*.go` | Pod-to-Pod, Service connectivity |
| **PodNetworking** | `pod_networking_test.go` | Custom network via CRD |
| **SecurityGroup** | `security_group_test.go` | SG isolation and rules |
| **MultiNetwork** | `multi_network_test.go` | Multiple ENIs per pod |
| **Upgrade** | `upgrade_test.go` | Upgrade compatibility |
| **ERDMA** | `erdma/erdma_test.go` | ERDMA device support |

### Running Tests

```bash
# Set kubeconfig
export KUBECONFIG=hack/terraform/ack/kubeconfig-tf-ack-*

# Run all tests
go test -v ./tests/... -tags e2e -timeout 2h \
  -repo registry.cn-hangzhou.aliyuncs.com/l1b0k

# Run specific test pattern
go test -v ./tests/... -tags e2e -run "TestPrefix" -timeout 30m

# Run with specific flags
go test -v ./tests/... -tags e2e \
  -run "TestConnectivity" \
  -timeout 30m \
  -repo registry.cn-hangzhou.aliyuncs.com/l1b0k \
  -enable-trunk=true
```

### Prefix Tests

```bash
# Basic prefix allocation
go test -v ./tests/... -tags e2e -run "TestPrefix_Basic" -timeout 30m

# Boundary conditions (max prefixes, exhaustion)
go test -v ./tests/... -tags e2e -run "TestPrefix_Boundary" -timeout 30m

# Dual stack and EFLO tests
go test -v ./tests/... -tags e2e -run "TestPrefix_DualStack|TestPrefix_EFLO" -timeout 30m

# State machine and scale tests
go test -v ./tests/... -tags e2e -run "TestPrefix_State|TestPrefix_Scale" -timeout 60m
```

### Connectivity Tests

```bash
# All connectivity tests
go test -v ./tests/... -tags e2e -run "TestConnectivity" -timeout 30m

# Specific scenarios
go test -v ./tests/... -tags e2e -run "TestConnectivity_NodePort" -timeout 30m
```

## Phase 5: Issue Discovery & Analysis

When tests fail or when validating feature implementations, analyze both the test code and the implementation to discover issues.

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

| Issue | Cause | Solution |
|-------|-------|----------|
| Tests skip with "ipam type is not crd" | Cluster uses non-centralized IPAM | Ensure `ipam_type: crd` in eni-config |
| Tests skip with "No ECS Shared ENI nodes" | Only Lingjun/EFLO nodes present | Add standard ECS nodes |
| Prefix not allocated | Missing node labels | Add `ip-prefix: "true"` and `terway-config` labels |
| Pod stuck in Creating | ENI quota exceeded | Check ENI quota or use prefix mode |
| Connectivity failures | Security group rules | Check SG allows pod traffic |

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

| File | Purpose |
|------|---------|
| `hack/terraform/ack/` | Terraform configs for ACK cluster |
| `hack/terraform/ack/deploy-terway.sh` | Helm-based Terway deployment script |
| `tests/main_test.go` | Test initialization and cluster discovery |
| `tests/utils_test.go` | Test utilities (Pod, Service builders) |
| `tests/node_utils_test.go` | Node type discovery and capacity checks |
| `tests/prefix_*.go` | Prefix mode tests |
| `tests/connective*.go` | Connectivity tests |
| `tests/pod_networking_test.go` | PodNetworking CRD tests |
| `tests/security_group_test.go` | Security group tests |
| `types/daemon/dynamicconfig.go` | Dynamic Config implementation |

## Node Types

| Type | Label | Description |
|------|-------|-------------|
| `ecs-shared-eni` | (default) | Standard ECS nodes, shared ENI mode |
| `ecs-exclusive-eni` | `k8s.aliyun.com/exclusive-mode-eni-type=eniOnly` | ECS nodes, exclusive ENI |
| `lingjun-shared-eni` | `alibabacloud.com/lingjun-worker=true` | Lingjun/EFLO nodes, shared ENI |
| `lingjun-exclusive-eni` | Both labels above | Lingjun nodes, exclusive ENI |

## Test Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-repo` | `registry.cn-hangzhou.aliyuncs.com/build-test` | Image registry |
| `-timeout` | `2m` | Default test timeout |
| `-enable-trunk` | `true` | Enable trunk mode tests |
| `-region-id` | `cn-hangzhou` | Alibaba Cloud region |
| `-vswitch-ids` | `""` | Additional vSwitch IDs |
| `-security-group-ids` | `""` | Additional security group IDs |