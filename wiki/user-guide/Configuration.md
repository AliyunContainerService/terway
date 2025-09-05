# Configuration Reference

This guide provides comprehensive reference for all Terway configuration options.

## CNI Configuration

The CNI configuration file is typically located at `/etc/cni/net.d/10-terway.conf` or `/etc/cni/net.d/10-terway.conflist`.

### Basic CNI Configuration

```json
{
  "cniVersion": "0.4.0",
  "name": "terway", 
  "type": "terway",
  "eniip_virtual_type": "IPVlan"
}
```

### CNI Configuration Options

| Parameter | Type | Description | Default | Required |
|-----------|------|-------------|---------|----------|
| `cniVersion` | string | CNI specification version | `"0.4.0"` | Yes |
| `name` | string | Network name | `"terway"` | Yes |
| `type` | string | CNI plugin name | `"terway"` | Yes |
| `eniip_virtual_type` | string | Virtual networking type | `"IPVlan"` | No |
| `trunk_on` | bool | Enable trunking mode | `false` | No |
| `enable_ebpf` | bool | Enable eBPF acceleration | `false` | No |
| `ip_stack` | string | IP stack type | `"ipv4"` | No |

### Virtual Type Options

#### IPVlan Mode
```json
{
  "eniip_virtual_type": "IPVlan"
}
```
- **Best performance**: Direct kernel networking
- **Requirements**: Kernel 4.2+
- **Features**: eBPF acceleration support

#### Policy Route Mode  
```json
{
  "eniip_virtual_type": "PolicyRoute"
}
```
- **Compatibility**: Works with older kernels
- **Performance**: Good, slightly lower than IPVlan
- **Use case**: Legacy systems

### Advanced CNI Configuration

#### Dual Stack IPv6
```json
{
  "cniVersion": "0.4.0",
  "name": "terway",
  "type": "terway", 
  "eniip_virtual_type": "IPVlan",
  "ip_stack": "dual"
}
```

#### eBPF Acceleration
```json
{
  "cniVersion": "0.4.0",
  "name": "terway",
  "type": "terway",
  "eniip_virtual_type": "IPVlan", 
  "enable_ebpf": true
}
```

#### Trunking Mode
```json
{
  "cniVersion": "0.4.0",
  "name": "terway",
  "type": "terway",
  "eniip_virtual_type": "IPVlan",
  "trunk_on": true
}
```

### CNI Chain Configuration

For custom CNI chains, use the conflist format:

```json
{
  "cniVersion": "0.4.0",
  "name": "terway-chain",
  "plugins": [
    {
      "type": "terway",
      "eniip_virtual_type": "IPVlan"
    },
    {
      "type": "bandwidth",
      "capabilities": {"bandwidth": true}
    }
  ]
}
```

## ConfigMap Configuration

The main Terway configuration is stored in the `eni-config` ConfigMap in the `kube-system` namespace.

### Basic ConfigMap Structure

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config
  namespace: kube-system
data:
  eni_conf: |
    {
      "version": "1",
      "access_key": "your-access-key",
      "access_secret": "your-access-secret", 
      "security_group": "sg-xxxxxxxxx",
      "service_cidr": "10.96.0.0/12",
      "vswitches": {
        "cn-hangzhou-a": ["vsw-xxxxxxxxx"],
        "cn-hangzhou-b": ["vsw-yyyyyyyyy"]
      }
    }
  10-terway.conf: |
    {
      "cniVersion": "0.4.0",
      "name": "terway",
      "type": "terway",
      "eniip_virtual_type": "IPVlan"
    }
  disable_network_policy: "false"
```

### Core Configuration Parameters

#### Authentication
| Parameter | Type | Description | Required |
|-----------|------|-------------|----------|
| `access_key` | string | Alibaba Cloud Access Key ID | Yes |
| `access_secret` | string | Alibaba Cloud Access Key Secret | Yes |

#### Network Configuration
| Parameter | Type | Description | Required |
|-----------|------|-------------|----------|
| `security_group` | string | Default security group ID | Yes |
| `service_cidr` | string | Kubernetes service CIDR | Yes |
| `vswitches` | object | vSwitch mapping by availability zone | Yes |

#### Pool Management
| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `max_pool_size` | int | Maximum ENI pool size per node | 5 |
| `min_pool_size` | int | Minimum ENI pool size per node | 0 |

### Advanced Configuration Options

#### Centralized IPAM
```json
{
  "version": "1",
  "centralized_ipam": true,
  "access_key": "your-access-key",
  "access_secret": "your-access-secret"
}
```

#### Custom Route Tables
```json
{
  "custom_route_table_ids": ["rtb-xxxxxxxxx", "rtb-yyyyyyyyy"]
}
```

#### ENI Tags
```json
{
  "eni_tags": {
    "Owner": "Kubernetes",
    "Environment": "Production", 
    "Project": "MyApp"
  }
}
```

#### IPv6 Configuration
```json
{
  "ipv6": true,
  "ip_stack": "dual"
}
```

#### Trunking Configuration
```json
{
  "trunk_on": true,
  "trunk_security_group": "sg-trunk123"
}
```

### Regional Configuration

#### Multi-Zone vSwitch Configuration
```json
{
  "vswitches": {
    "cn-hangzhou-a": ["vsw-aaa", "vsw-bbb"],
    "cn-hangzhou-b": ["vsw-ccc", "vsw-ddd"], 
    "cn-hangzhou-c": ["vsw-eee", "vsw-fff"]
  }
}
```

#### Multi-Region Support
```json
{
  "region_id": "cn-hangzhou",
  "vswitches": {
    "cn-hangzhou-a": ["vsw-xxxxxxxxx"],
    "cn-hangzhou-b": ["vsw-yyyyyyyyy"]
  }
}
```

## Environment Variables

Terway supports configuration through environment variables in the DaemonSet.

### Core Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `CLUSTER_ID` | Kubernetes cluster identifier | - |
| `REGION_ID` | Alibaba Cloud region | - |
| `LOG_LEVEL` | Logging level (debug, info, warn, error) | `info` |
| `NODE_NAME` | Kubernetes node name | Auto-detected |

### Network Configuration
| Variable | Description | Default |
|----------|-------------|---------|
| `KUBE_CONTROLLER_BRIDGE_NAME` | Bridge name for controller | `kube-bridge` |
| `ENABLE_NETWORK_POLICY` | Enable NetworkPolicy support | `true` |
| `ENABLE_TRUNK` | Enable trunking support | `false` |

### Performance Tuning
| Variable | Description | Default |
|----------|-------------|---------|
| `ENABLE_EBPF` | Enable eBPF acceleration | `false` |
| `DATAPATH_VERSION` | Datapath version (v1, v2) | `v1` |
| `POOL_WORKER_NUM` | Number of pool workers | `4` |

### Example DaemonSet Environment Configuration
```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: terway
spec:
  template:
    spec:
      containers:
      - name: terway
        env:
        - name: CLUSTER_ID
          value: "cluster-123"
        - name: REGION_ID  
          value: "cn-hangzhou"
        - name: LOG_LEVEL
          value: "info"
        - name: ENABLE_EBPF
          value: "true"
        - name: DATAPATH_VERSION
          value: "v2"
```

## Pod and Namespace Annotations

### Pod Annotations

#### Trunking Configuration
```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    # Enable trunking for this pod
    k8s.aliyun.com/trunk-on: "true"
    
    # Custom security group
    k8s.aliyun.com/security-group-id: "sg-custom123"
    
    # Custom vSwitch  
    k8s.aliyun.com/vswitch-id: "vsw-custom456"
    
    # Fixed IP assignment
    k8s.aliyun.com/pod-ip: "192.168.1.100"
    
    # Custom ENI tags
    k8s.aliyun.com/eni-tags: '{"Project": "MyApp", "Owner": "TeamA"}'
```

#### Network Performance
```yaml
metadata:
  annotations:
    # Bandwidth limits (requires QoS support)
    kubernetes.io/ingress-bandwidth: "10M"
    kubernetes.io/egress-bandwidth: "10M"
    
    # Force eBPF acceleration
    k8s.aliyun.com/force-ebpf: "true"
```

#### IP Management
```yaml
metadata:
  annotations:
    # Request specific IP from pool
    k8s.aliyun.com/request-ip: "192.168.1.50"
    
    # IP allocation strategy
    k8s.aliyun.com/ip-allocation-strategy: "prefer-existing-eni"
```

### Namespace Annotations

#### Default Trunking Configuration
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: secure-namespace
  annotations:
    # Enable trunking for all pods in namespace
    k8s.aliyun.com/trunk-on: "true"
    
    # Default security group for namespace
    k8s.aliyun.com/security-group-id: "sg-secure123"
    
    # Default vSwitch for namespace
    k8s.aliyun.com/vswitch-id: "vsw-secure456"
```

## Network Policy Configuration

### Basic NetworkPolicy
```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-all
spec:
  podSelector: {}
  policyTypes:
  - Ingress
  - Egress
```

### Allow Specific Traffic
```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-app-traffic
spec:
  podSelector:
    matchLabels:
      app: myapp
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: frontend
    ports:
    - protocol: TCP
      port: 8080
  egress:
  - to:
    - podSelector:
        matchLabels:
          app: database
    ports:
    - protocol: TCP  
      port: 5432
```

## QoS Configuration

### Bandwidth Limiting
```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    kubernetes.io/ingress-bandwidth: "100M"
    kubernetes.io/egress-bandwidth: "50M"
spec:
  containers:
  - name: app
    image: nginx
```

### Traffic Shaping Classes
```yaml
metadata:
  annotations:
    # Traffic class (requires advanced QoS)
    k8s.aliyun.com/traffic-class: "gold"
    
    # Priority
    k8s.aliyun.com/traffic-priority: "high"
```

## Monitoring Configuration

### Metrics Collection
```yaml
# ConfigMap addition for metrics
data:
  enable_metrics: "true"
  metrics_port: "9090"
  metrics_path: "/metrics"
```

### Prometheus Configuration
```yaml
apiVersion: v1
kind: ServiceMonitor
metadata:
  name: terway-metrics
spec:
  selector:
    matchLabels:
      app: terway
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

## Troubleshooting Configuration

### Debug Configuration
```json
{
  "version": "1",
  "debug": true,
  "log_level": "debug",
  "trace_enabled": true
}
```

### Performance Profiling
```yaml
env:
- name: ENABLE_PPROF
  value: "true"
- name: PPROF_PORT
  value: "6060"
```

## Configuration Validation

### Validation Script
```bash
#!/bin/bash
# Validate Terway configuration

# Check CNI config
if [ ! -f /etc/cni/net.d/10-terway.conf ]; then
    echo "ERROR: CNI configuration missing"
    exit 1
fi

# Validate JSON syntax
if ! jq empty /etc/cni/net.d/10-terway.conf; then
    echo "ERROR: Invalid JSON in CNI config"
    exit 1
fi

# Check ConfigMap
kubectl get configmap eni-config -n kube-system > /dev/null || {
    echo "ERROR: eni-config ConfigMap not found"
    exit 1
}

echo "Configuration validation passed"
```

### Common Validation Errors

#### Invalid JSON Syntax
```bash
# Check JSON syntax
jq empty /etc/cni/net.d/10-terway.conf
# parse error: Invalid numeric literal at line 5, column 15
```

#### Missing Required Fields
```bash
# Check required fields
kubectl get configmap eni-config -n kube-system -o jsonpath='{.data.eni_conf}' | jq .access_key
# null (ERROR: access_key missing)
```

#### Invalid Resource IDs
```bash
# Validate security group exists
aliyun ecs DescribeSecurityGroups --SecurityGroupIds.1 sg-xxxxxxxxx
# ERROR: InvalidSecurityGroup.NotFound
```

## See Also

- [Network Modes](Network-Modes.md) - Network mode selection and configuration
- [Dynamic Configuration](Dynamic-Configuration.md) - Runtime configuration changes
- [Environment Variables](Environment-Variables.md) - Complete environment variable reference
- [Common Issues](../troubleshooting/Common-Issues.md) - Configuration troubleshooting