# Terway CLI Reference

Terway CLI is a debugging tool packaged in the Terway pod that provides runtime information and debugging capabilities through gRPC communication with the Terway daemon.

## Overview

The Terway CLI tool enhances the observability of Terway components, making it easier to quickly identify issues when problems occur. It communicates with the Terway daemon via gRPC to retrieve key information.

## Architecture

The Terway daemon includes a tracing component where key components (`network_service`, `pool`, `factory`, etc.) register themselves. These components provide key information or execute commands through interfaces. The tracing component provides an external gRPC interface for communication with `terway-cli`.

![terway_tracing](../docs/images/terway_tracing.png)

For future scalability and compatibility, components in tracing are registered in the form of `(type, resource_name)`.

## Commands

Terway CLI provides 5 available commands:

### `list [type]`
Lists all currently registered resource types. If a type is specified, lists all resources of that type.

**Usage:**
```bash
# List all resource types
kubectl exec -n kube-system <terway-pod> -- terway-cli list

# List resources of specific type
kubectl exec -n kube-system <terway-pod> -- terway-cli list network_service
```

### `show <type> [resource_name]`
Gets configuration and tracing information for a specific resource. If no resource name is specified, defaults to the first resource of that type.

**Usage:**
```bash
# Show first resource of type
kubectl exec -n kube-system <terway-pod> -- terway-cli show resource_pool

# Show specific resource
kubectl exec -n kube-system <terway-pod> -- terway-cli show resource_pool eni-pool
```

### `mapping`
Gets the Pod -- Pool -- Factory (Aliyun API) resource mapping relationships.

When using mapping, results may show the following states:

- **Normal (white)** - Resource mapping is normal, with pods using the resource
- **Idle (blue)** - Resource mapping is normal, no pods using the resource  
- **Error (red)** - Resource mapping error (Pool and Factory resources cannot correspond one-to-one)

When errors occur, `terway-cli` will exit with "error exists in mapping" and return error code 1.

![terway_cli_mapping](../docs/images/terway_cli_mapping.png)

**Usage:**
```bash
kubectl exec -n kube-system <terway-pod> -- terway-cli mapping
```

### `execute <type> <resource_name> <command> args...`
Executes defined commands on a specific resource.

Currently available commands:
- **mapping command**: Available on all resources to view current layer resource mapping list
- **audit command**: Available on `eniip factory` for local multi-IP factory resource reconciliation with Aliyun API resources

Since you can directly use the mapping command to replace both functions, direct use is not recommended.

**Usage:**
```bash
kubectl exec -n kube-system <terway-pod> -- terway-cli execute factory eniip mapping
kubectl exec -n kube-system <terway-pod> -- terway-cli execute factory eniip audit
```

### `metadata`
Gets resource information through ECS metadata API.

This command queries network resources returned by the ECS metadata API and presents them in tree form. It's used to check if metadata resources correspond with local resources.

![terway_cli_metadata](../docs/images/terway_cli_metadata.png)

**Usage:**
```bash
kubectl exec -n kube-system <terway-pod> -- terway-cli metadata
```

## Resource Configuration and Tracing Information

Currently registered information includes:

### `network_service`
- `name` - Name
- `daemon_mode` - Daemon running mode (`ENIMultiIP`, `ENIOnly`, `VPC`)
- `config_file_path` - Configuration file path
- `kubeconfig` - Kubernetes configuration path
- `master` - Kubernetes master address, auto-obtained if empty along with kubeconfig
- `pending_pods_count` - Number of pods waiting for allocation
- `pods` - Resource allocation status for each pod

### `resource_pool`
- `name` - Name
- `max_idle` - Maximum idle resources (high watermark)
- `min_idle` - Minimum idle resources (low watermark)  
- `idle` - Currently idle resources
- `inuse` - Currently in-use resources

### `factory(eniip)`
- `name` - Name
- `eni_max_ip` - Maximum IP count per ENI
- `primary_ip` - Primary IP
- `eni_count` - Current number of ENIs
- `secondary_ip_count` - Current number of secondary IPs
- `eni` details:
  - `pending` - Number of IPs waiting for allocation
  - `secondary_ips` - Secondary IPs on this ENI
  - `ip_alloc_inhibit_expire_at` - IP allocation inhibit expiration time

### `factory(eni)`
- `name` - Name
- `vswitches` - Number of virtual switches owned
- `vswitch_selection_policy` - Switch selection policy
- `cache_expire_at` - Switch list cache expiration time
- `vswitch` details:
  - `ip_count` - Number of allocatable IPs

## Common Debugging Workflows

### 1. Check Overall Status
```bash
# List all resource types
kubectl exec -n kube-system <terway-pod> -- terway-cli list

# Check network service status
kubectl exec -n kube-system <terway-pod> -- terway-cli show network_service
```

### 2. Debug IP Allocation Issues
```bash
# Check resource pool status
kubectl exec -n kube-system <terway-pod> -- terway-cli show resource_pool

# Check factory status for ENI multi-IP mode
kubectl exec -n kube-system <terway-pod> -- terway-cli show factory

# Check resource mapping
kubectl exec -n kube-system <terway-pod> -- terway-cli mapping
```

### 3. Verify Cloud Resource Consistency
```bash
# Check metadata from ECS API
kubectl exec -n kube-system <terway-pod> -- terway-cli metadata

# Audit factory resources against cloud API
kubectl exec -n kube-system <terway-pod> -- terway-cli execute factory eniip audit
```

### 4. Monitor Resource Usage
```bash
# Continuously monitor mapping
watch -n 5 'kubectl exec -n kube-system <terway-pod> -- terway-cli mapping'

# Check pending pods
kubectl exec -n kube-system <terway-pod> -- terway-cli show network_service | grep pending
```

## Best Practices

1. **Regular Health Checks**: Use `mapping` command to regularly verify resource consistency
2. **Performance Monitoring**: Monitor resource pool utilization to optimize pool sizes
3. **Troubleshooting**: Use `show` commands to get detailed information when investigating issues
4. **Resource Auditing**: Periodically run audit commands to ensure cloud resource consistency

## Integration with Monitoring

Terway CLI can be integrated with monitoring systems:

```bash
#!/bin/bash
# Example monitoring script
TERWAY_POD=$(kubectl get pods -n kube-system -l app=terway -o jsonpath='{.items[0].metadata.name}')

# Check for mapping errors
if ! kubectl exec -n kube-system $TERWAY_POD -- terway-cli mapping > /dev/null 2>&1; then
    echo "ERROR: Resource mapping issues detected"
    exit 1
fi

# Check resource pool utilization
POOL_INFO=$(kubectl exec -n kube-system $TERWAY_POD -- terway-cli show resource_pool)
# Parse and alert based on utilization metrics
```

## See Also

- [Debugging Guide](../troubleshooting/Debugging-Guide.md) - General debugging procedures
- [Common Issues](../troubleshooting/Common-Issues.md) - Common problems and solutions
- [Metrics](Metrics.md) - Monitoring metrics reference