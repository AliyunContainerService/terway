# Network Modes

Terway supports multiple network modes to meet different requirements. This guide explains each mode, their use cases, and how to configure them.

## Overview

Terway provides several network modes, each with different characteristics:

| Mode | Performance | Security | Resource Usage | Complexity |
|------|------------|----------|----------------|------------|
| ENI Multi-IP | High | Good | Medium | Low |
| Trunking | Very High | Excellent | High | Medium |
| VPC (Deprecated) | Medium | Good | Low | Low |
| Exclusive ENI (Deprecated) | Very High | Excellent | Very High | Medium |

## ENI Multi-IP Mode (Default)

### Overview

ENI Multi-IP mode is the default and recommended mode for most use cases. In this mode:

- Each node uses one or more ENIs
- Multiple IP addresses are assigned to each ENI
- Pods share ENIs but get unique IP addresses
- Traffic routing is handled by IPVlan or policy routing

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Node                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │   Pod    │  │   Pod    │  │   Pod    │  │   Pod    │   │
│  │ 10.0.1.5 │  │ 10.0.1.6 │  │ 10.0.1.7 │  │ 10.0.1.8 │   │
│  └─────┬────┘  └─────┬────┘  └─────┬────┘  └─────┬────┘   │
│        └─────────────┼─────────────┼─────────────┘        │
│                 ┌────▼─────────────▼────┐                 │
│                 │       ENI             │                 │
│                 │  Primary: 10.0.1.4    │                 │
│                 │  Secondary: 10.0.1.5  │                 │
│                 │  Secondary: 10.0.1.6  │                 │
│                 │  Secondary: 10.0.1.7  │                 │
│                 │  Secondary: 10.0.1.8  │                 │
│                 └───────────────────────┘                 │
└─────────────────────────────────────────────────────────────┘
```

### Configuration

Basic configuration for ENI Multi-IP mode:

```json
{
  "cniVersion": "0.4.0",
  "name": "terway",
  "type": "terway",
  "eniip_virtual_type": "IPVlan"
}
```

ConfigMap settings:
```json
{
  "version": "1",
  "access_key": "your-access-key",
  "access_secret": "your-access-secret",
  "security_group": "sg-xxxxxxxxx",
  "service_cidr": "10.96.0.0/12",
  "vswitches": {
    "cn-hangzhou-a": ["vsw-xxxxxxxxx"],
    "cn-hangzhou-b": ["vsw-yyyyyyyyy"]
  },
  "max_pool_size": 5,
  "min_pool_size": 0
}
```

### Virtual Types

#### IPVlan Mode
```json
{
  "eniip_virtual_type": "IPVlan"
}
```

**Characteristics:**
- Best performance
- Requires kernel 4.2+
- eBPF acceleration support
- Recommended for most workloads

#### Policy Routing Mode
```json
{
  "eniip_virtual_type": "PolicyRoute"
}
```

**Characteristics:**
- Compatible with older kernels
- Uses policy routing for traffic steering
- Good compatibility
- Slightly lower performance than IPVlan

### Use Cases

**Best for:**
- General containerized applications
- Microservices architectures
- Applications requiring good performance
- Multi-tenant environments

**Not suitable for:**
- Applications requiring dedicated ENIs
- Extreme performance requirements
- Complex security group requirements per pod

## Trunking Mode

### Overview

Trunking mode provides the highest level of flexibility and performance by allowing:

- Each pod to have its own dedicated ENI
- Per-pod security group assignment
- Per-pod vSwitch selection
- Maximum network isolation

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Node                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │   Pod    │  │   Pod    │  │   Pod    │  │   Pod    │   │
│  │          │  │          │  │          │  │          │   │
│  └─────┬────┘  └─────┬────┘  └─────┬────┘  └─────┬────┘   │
│        │             │             │             │        │
│   ┌────▼────┐   ┌────▼────┐   ┌────▼────┐   ┌────▼────┐  │
│   │  ENI-1  │   │  ENI-2  │   │  ENI-3  │   │  ENI-4  │  │
│   │ SG-A    │   │ SG-B    │   │ SG-A    │   │ SG-C    │  │
│   │ VSW-1   │   │ VSW-2   │   │ VSW-1   │   │ VSW-1   │  │
│   └─────────┘   └─────────┘   └─────────┘   └─────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### Configuration

Enable trunking in CNI configuration:
```json
{
  "cniVersion": "0.4.0",
  "name": "terway",
  "type": "terway",
  "eniip_virtual_type": "IPVlan",
  "trunk_on": true
}
```

ConfigMap settings:
```json
{
  "version": "1",
  "trunk_on": true,
  "access_key": "your-access-key",
  "access_secret": "your-access-secret",
  "security_group": "sg-default",
  "service_cidr": "10.96.0.0/12",
  "vswitches": {
    "cn-hangzhou-a": ["vsw-xxxxxxxxx", "vsw-yyyyyyyyy"],
    "cn-hangzhou-b": ["vsw-zzzzzzzzz", "vsw-aaaaaaaaa"]
  }
}
```

### Pod Annotations

Pods can specify custom network settings:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: custom-network-pod
  annotations:
    # Enable trunking for this pod
    k8s.aliyun.com/trunk-on: "true"
    
    # Custom security group
    k8s.aliyun.com/security-group-id: "sg-custom123"
    
    # Custom vSwitch
    k8s.aliyun.com/vswitch-id: "vsw-custom456"
    
    # Fixed IP (optional)
    k8s.aliyun.com/pod-ip: "192.168.1.100"
spec:
  containers:
  - name: app
    image: nginx
```

### Namespace-level Configuration

Configure trunking for entire namespaces:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: secure-namespace
  annotations:
    k8s.aliyun.com/trunk-on: "true"
    k8s.aliyun.com/security-group-id: "sg-secure123"
    k8s.aliyun.com/vswitch-id: "vsw-secure456"
```

### Use Cases

**Best for:**
- High-security requirements
- Applications needing dedicated network resources
- Complex multi-tenant scenarios
- Applications with specific security group needs
- Gaming, financial, or high-performance computing workloads

**Considerations:**
- Higher resource consumption
- Limited by instance ENI quota
- More complex configuration
- Not available in all self-managed clusters

## Deprecated Modes

### VPC Mode (Deprecated)

> ⚠️ **Deprecated**: VPC mode is deprecated and should not be used for new deployments.

VPC mode used VPC routing for direct communication but lacked the performance benefits of ENI-based modes.

### Exclusive ENI Mode (Deprecated)

> ⚠️ **Deprecated**: Replaced by node pool configuration for dedicated ENI usage.

Previously allowed direct ENI attachment to pods, now replaced by more flexible node pool configurations.

## Choosing the Right Mode

### Decision Matrix

| Requirement | Recommended Mode |
|-------------|-----------------|
| General web applications | ENI Multi-IP |
| Microservices architecture | ENI Multi-IP |
| High-performance computing | Trunking |
| Gaming applications | Trunking |
| Financial services | Trunking |
| IoT workloads | ENI Multi-IP |
| Development/testing | ENI Multi-IP |
| Legacy applications | ENI Multi-IP (Policy Route) |

### Performance Comparison

| Mode | Throughput | Latency | CPU Usage |
|------|------------|---------|-----------|
| ENI Multi-IP (IPVlan) | High | Low | Low |
| ENI Multi-IP (Policy Route) | Medium | Medium | Medium |
| Trunking | Very High | Very Low | Low |

### Resource Requirements

| Mode | ENIs per Node | IPs per ENI | Instance Types |
|------|---------------|-------------|----------------|
| ENI Multi-IP | 1-8 | 2-50 | All supported |
| Trunking | 1-15 | 1 | g6, g7, c6, c7 series |

## Migration Between Modes

### ENI Multi-IP to Trunking

1. **Prepare**: Ensure cluster supports trunking
2. **Plan**: Identify pods requiring trunking
3. **Configure**: Update CNI configuration
4. **Migrate**: Gradually move workloads
5. **Verify**: Test network functionality

### Configuration Update Process

```bash
# 1. Update CNI configuration
kubectl edit configmap eni-config -n kube-system

# 2. Restart Terway pods
kubectl delete pods -n kube-system -l app=terway

# 3. Verify configuration
kubectl get pods -n kube-system | grep terway

# 4. Test with sample workload
kubectl apply -f test-trunking-pod.yaml
```

## Monitoring and Troubleshooting

### Check Current Mode

```bash
# Check CNI configuration
cat /etc/cni/net.d/10-terway.conf

# Check ConfigMap
kubectl get configmap eni-config -n kube-system -o yaml
```

### Monitor Resource Usage

```bash
# Check ENI usage
TERWAY_POD=$(kubectl get pods -n kube-system -l app=terway -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n kube-system $TERWAY_POD -- terway-cli show factory

# Check IP pool status
kubectl exec -n kube-system $TERWAY_POD -- terway-cli show resource_pool
```

### Common Issues

#### ENI Quota Exceeded
```bash
# Error: QuotaExceeded.Eni
# Solution: Increase ENI quota or use ENI Multi-IP mode
```

#### Trunking Not Supported
```bash
# Error: Trunking not supported on instance type
# Solution: Use compatible instance types (g6, g7, c6, c7 series)
```

#### IP Address Exhaustion
```bash
# Error: No available IP addresses
# Solution: Add more vSwitches or expand CIDR ranges
```

## Best Practices

### Configuration
1. **Start with ENI Multi-IP**: Use for general workloads
2. **Reserve trunking**: Use only when specifically needed
3. **Plan capacity**: Monitor ENI and IP usage
4. **Test changes**: Validate in staging before production

### Security
1. **Default security groups**: Use restrictive defaults
2. **Network policies**: Implement Kubernetes NetworkPolicy
3. **Segmentation**: Use different security groups for different tiers
4. **Monitoring**: Monitor network traffic and security events

### Performance
1. **Enable eBPF**: For maximum performance in ENI Multi-IP mode
2. **Use IPVlan**: Prefer over policy routing when possible
3. **Instance selection**: Choose instances with good network performance
4. **Monitor metrics**: Track network performance and resource usage

## See Also

- [Configuration Guide](Configuration.md) - Detailed configuration options
- [Security Policies](Security-Policies.md) - Network security configuration  
- [IPv6 Support](IPv6-Support.md) - Dual-stack configuration
- [Troubleshooting](../troubleshooting/Common-Issues.md) - Common issues and solutions