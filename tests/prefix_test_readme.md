# IP Prefix E2E Tests

This directory contains end-to-end (E2E) tests for the IP Prefix feature in Terway.

## Overview

IP Prefix is a Terway feature that allocates CIDR blocks (prefixes) instead of individual IP addresses to ENIs. Each /28 prefix contains 16 IP addresses. This mode is suitable for high-density pod deployments.

## Test Files

| File | Description |
|------|-------------|
| `prefix_basic_test.go` | Basic functionality tests: enable/disable, single ENI, multi-ENI distribution |
| `prefix_boundary_test.go` | Boundary value tests: API limits, ENI capacity, zero/min/max values |
| `prefix_dualstack_eflo_test.go` | Dual-stack tests (IPv4/IPv6 1:1 ratio) and EFLO exclusion tests |
| `prefix_state_scale_test.go` | State transition tests and large-scale/performance tests |
| `prefix_helpers_test.go` | Helper functions for prefix tests |

## Prerequisites

1. **Cluster Requirements**:
   - Kubernetes cluster with Terway CNI installed
   - Centralized IPAM mode (`ipam_type: crd`)
   - Terway version >= v1.17.0
   - ECS Shared ENI nodes (Lingjun/EFLO and Exclusive ENI nodes are excluded from prefix mode)
   - No dedicated prefix node pool required; tests dynamically configure prefix mode on existing multi-IP nodes

2. **Test Environment**:
   - `terway-eniip` daemonset (shared ENI mode)
   - Nodes with sufficient capacity for prefix allocation
   - vSwitch CIDR reservations for prefix allocation (managed by terraform)

3. **Permissions**:
   - Ability to modify ConfigMap `kube-system/eni-config`
   - Ability to modify node labels
   - Ability to restart terway-eniip daemonset

## How Prefix Tests Work (No Dedicated Node Pool)

Prefix tests dynamically configure prefix mode on existing ECS Shared ENI nodes
from the `default` node pool. The workflow for each test is:

1. **Setup Phase**:
   - Save original `eni-config` ConfigMap
   - Create node-specific Dynamic Config ConfigMap (`eni-config-prefix-<nodeName>`)
   - Add `network.alibabacloud.com/ip-prefix=true` label to the target node
   - Set `terway-config` label on node to point to the Dynamic Config ConfigMap
   - Restart Terway to apply configuration

2. **Test Phase**:
   - Wait for prefix allocation on the Node CR
   - Verify prefix counts, distribution, and status

3. **Teardown Phase**:
   - Remove `network.alibabacloud.com/ip-prefix` label from all nodes
   - Delete node-specific Dynamic Config ConfigMaps
   - Remove `terway-config` labels from nodes
   - Restore original `eni-config` ConfigMap

This approach ensures:
- No dedicated prefix node pool is needed (saves 2 ECS instances)
- Tests are self-contained with proper setup and cleanup
- Prefix mode is only active during test execution
- Other tests are not affected by prefix configuration

## Running the Tests

### Run All Prefix Tests

```bash
cd /Users/lbk/projects/opensource_terway
go test -v ./tests/... -tags e2e -run "TestPrefix_" -timeout 60m
```

### Run Specific Test Categories

```bash
# Basic functionality tests
go test -v ./tests/... -tags e2e -run "TestPrefix_Basic" -timeout 30m

# Boundary tests
go test -v ./tests/... -tags e2e -run "TestPrefix_Boundary" -timeout 30m

# Dual-stack tests
go test -v ./tests/... -tags e2e -run "TestPrefix_DualStack" -timeout 30m

# EFLO exclusion tests
go test -v ./tests/... -tags e2e -run "TestPrefix_EFLO" -timeout 20m

# State and scale tests
go test -v ./tests/... -tags e2e -run "TestPrefix_State" -timeout 40m
go test -v ./tests/... -tags e2e -run "TestPrefix_Scale" -timeout 60m
```

### Test Flags

```bash
# Specify region
-region-id=cn-hangzhou

# Specify image repo
-repo=registry.cn-hangzhou.aliyuncs.com/build-test

# Specify timeout
-timeout=5m

# Run with specific kubeconfig
export KUBECONFIG=/path/to/kubeconfig
```

## Test Cases

### Basic Functionality Tests

| Test | Description |
|------|-------------|
| `TestPrefix_Basic_EnableDisable` | Tests enabling and disabling prefix mode via node labels |
| `TestPrefix_Basic_SingleENI` | Tests allocating prefixes on a single ENI |
| `TestPrefix_Basic_MultiENIDistribution` | Tests prefix distribution across multiple ENIs |

### Boundary Value Tests

| Test | Description |
|------|-------------|
| `TestPrefix_Boundary_APILimit` | Tests the 10-prefix API limit boundary |
| `TestPrefix_Boundary_ENICapacity` | Tests ENI capacity limits |
| `TestPrefix_Boundary_ZeroAndMin` | Tests zero and minimum prefix count |
| `TestPrefix_Boundary_MaxValue` | Tests maximum value boundary |

### Dual-Stack Tests

| Test | Description |
|------|-------------|
| `TestPrefix_DualStack_1to1Ratio` | Tests IPv4/IPv6 prefix 1:1 ratio |
| `TestPrefix_DualStack_CapacityConstraint` | Tests dual-stack capacity constraints |

### EFLO Exclusion Tests

| Test | Description |
|------|-------------|
| `TestPrefix_EFLO_Disabled` | Tests that EFLO/Lingjun nodes disable prefix mode |

### State Transition Tests

| Test | Description |
|------|-------------|
| `TestPrefix_State_Transition` | Tests ENI state machine transitions |
| `TestPrefix_State_DynamicScaleUp` | Tests dynamic scaling up of prefixes |

### Scale Tests

| Test | Description |
|------|-------------|
| `TestPrefix_Scale_LargeScale` | Tests large-scale prefix allocation (50 prefixes) |
| `TestPrefix_Scale_HighFrequencyPodCreation` | Tests high-frequency pod creation with prefixes |

## Key Test Scenarios

### 1. Prefix Mode Enable/Disable

Tests that:
- Adding `network.alibabacloud.com/ip-prefix=true` label enables prefix mode
- Removing the label stops prefix allocation
- Prefix count is controlled by `ipv4_prefix_count` in eni-config

### 2. API Limit Boundary (10 Prefixes)

Tests the API limitation:
- Single API call handles up to 10 prefixes
- Requests >10 prefixes are batched across multiple API calls
- 11 prefixes require 2 API calls (10+1)
- 20 prefixes require 2 API calls (10+10)

### 3. ENI Capacity Boundary

Tests ENI capacity limits:
- Max prefixes per ENI = `IPv4PerAdapter - 1` (1 slot reserved for primary IP)
- Excess prefixes are distributed to additional ENIs
- Multiple ENIs are created when capacity is exceeded

### 4. Dual-Stack 1:1 Ratio

Tests dual-stack allocation:
- IPv4 and IPv6 prefixes are allocated in 1:1 ratio
- Both stacks have equal prefix counts
- Capacity constraints apply to both stacks equally

### 5. EFLO Exclusion

Tests Lingjun/EFLO node exclusion:
- Prefix mode is disabled on Lingjun nodes
- Prefix label is ignored on EFLO nodes
- Traditional IP mode is used instead

## Test Configuration

### Node Labels

```bash
# Enable prefix mode on a node
kubectl label node <node-name> network.alibabacloud.com/ip-prefix=true

# Disable prefix mode
kubectl label node <node-name> network.alibabacloud.com/ip-prefix-
```

### ConfigMap Configuration

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config
  namespace: kube-system
data:
  eni_conf: |
    {
      "ipv4_prefix_count": 10,
      "ip_stack": "ipv4"
    }
```

## Verification Checklist

After running tests, verify:

- [ ] Prefixes are allocated in Node CR `.status.networkInterfaces[*].ipv4Prefix`
- [ ] All prefixes have status `Valid`
- [ ] Actual count matches `ipv4_prefix_count` configuration
- [ ] Prefixes are distributed across multiple ENIs when needed
- [ ] API calls don't exceed 10 prefixes per call
- [ ] ENI capacity limits are respected (`IPv4PerAdapter - 1`)
- [ ] Dual-stack maintains 1:1 IPv4/IPv6 ratio
- [ ] EFLO nodes don't have prefixes allocated
- [ ] Pods can successfully get IPs from prefix ranges

## Troubleshooting

### Test Failures

1. **Prefix allocation timeout**:
   - Check controller logs: `kubectl logs -n kube-system -l app=terway-controlplane | grep -i prefix`
   - Verify node capacity: `kubectl get nodes.network.alibabacloud.com <node> -o yaml`

2. **API limit errors**:
   - Check ECS API quotas
   - Verify `maxPrefixPerAPICall` constant in code

3. **EFLO node issues**:
   - Verify node has `alibabacloud.com/lingjun-worker=true` label
   - Check that prefix mode is correctly disabled

### Debug Commands

```bash
# Check node CR for prefix allocation
kubectl get nodes.network.alibabacloud.com <node-name> -o yaml

# Check prefix count
kubectl get nodes.network.alibabacloud.com <node-name> -o jsonpath='{.spec.eni.ipPrefixCount}'

# View all prefixes
kubectl get nodes.network.alibabacloud.com <node-name> -o jsonpath='{.status.networkInterfaces[*].ipv4Prefix}'

# Check controller logs
kubectl logs -n kube-system -l app=terway-controlplane | grep -i prefix

# Check node labels
kubectl get node <node-name> --show-labels | grep ip-prefix
```

## Design Notes

### Key Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `maxPrefixPerAPICall` | 10 | Max prefixes per API call |
| `IPv4PerAdapter - 1` | Varies | Max prefixes per ENI |
| Prefix capacity | 16 IPs | Each /28 prefix has 16 IPs |

### Prefix Allocation Flow

1. **New ENI Creation**: `assignEniPrefixWithOptions()`
   - Calculate capacity: `IPv4PerAdapter - 1`
   - Consider existing/pending ENIs
   - Distribute prefixes across new ENIs
   - Respect API limit (10 per call)

2. **Existing ENI Sync**: `syncPrefixAllocation()`
   - Only processes `InUse` ENIs
   - Calculate slot availability
   - Dual-stack: maintain 1:1 ratio

### Mutual Exclusion

- When `addIPv4PrefixN > 0`, `addIPv4N = 0`
- Prefix mode and independent IP mode are mutually exclusive
- Once prefix mode is enabled, all IPs come from prefixes
