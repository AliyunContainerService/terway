# Installation Guide

This guide provides step-by-step instructions for installing Terway CNI plugin in your Kubernetes cluster.

## Prerequisites

### 1. Cluster Requirements

- **Kubernetes Version**: 1.16+ (recommended 1.20+)
- **Container Runtime**: Docker, containerd, or CRI-O
- **Operating System**: Linux (kernel 3.10+, recommended 4.18+)

### 2. Alibaba Cloud Resources

- **VPC**: Virtual Private Cloud with appropriate CIDR
- **VSwitches**: At least one vSwitch in each availability zone
- **Security Groups**: Configured security groups for your workloads
- **RAM Role**: Service account with ENI permissions

### 3. Required RAM Permissions

Terway requires the following RAM permissions:

```json
{
  "Version": "1",
  "Statement": [
    {
      "Action": [
        "ecs:CreateNetworkInterface",
        "ecs:DescribeNetworkInterfaces",
        "ecs:AttachNetworkInterface",
        "ecs:DetachNetworkInterface",
        "ecs:DeleteNetworkInterface",
        "ecs:DescribeInstanceTypes",
        "ecs:DescribeInstances",
        "ecs:AssignPrivateIpAddresses",
        "ecs:UnassignPrivateIpAddresses",
        "ecs:DescribeSecurityGroups",
        "ecs:DescribeVSwitches",
        "vpc:DescribeVSwitches",
        "vpc:DescribeRouteTables",
        "vpc:DescribeRouteEntries"
      ],
      "Resource": "*",
      "Effect": "Allow"
    }
  ]
}
```

## Installation Methods

### Method 1: ACK Managed Clusters (Recommended)

For ACK (Alibaba Cloud Kubernetes) clusters, Terway is pre-installed and managed by Alibaba Cloud.

1. **Create ACK Cluster** with Terway network plugin selected
2. **Configure Network Settings** during cluster creation
3. **Verify Installation** after cluster is ready

### Method 2: Self-Managed Clusters

For self-managed Kubernetes clusters on Alibaba Cloud ECS instances:

#### Step 1: Prepare Configuration

Create the Terway configuration:

```bash
cat > terway-config.yaml << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config
  namespace: kube-system
data:
  eni_conf: |
    {
      "version": "1",
      "access_key": "YOUR_ACCESS_KEY",
      "access_secret": "YOUR_ACCESS_SECRET",
      "security_group": "sg-xxxxxxxxx",
      "service_cidr": "10.96.0.0/12",
      "vswitches": {
        "cn-hangzhou-a": ["vsw-xxxxxxxxx"],
        "cn-hangzhou-b": ["vsw-yyyyyyyyy"]
      },
      "max_pool_size": 5,
      "min_pool_size": 0
    }
  10-terway.conf: |
    {
      "cniVersion": "0.4.0",
      "name": "terway",
      "type": "terway",
      "eniip_virtual_type": "IPVlan"
    }
  disable_network_policy: "false"
EOF
```

#### Step 2: Deploy Terway

```bash
# Apply the configuration
kubectl apply -f terway-config.yaml

# Deploy Terway DaemonSet
kubectl apply -f https://raw.githubusercontent.com/AliyunContainerService/terway/main/terway.yml
```

#### Step 3: Verify Installation

```bash
# Check Terway pods
kubectl get pods -n kube-system | grep terway

# Check CNI configuration
ls -la /etc/cni/net.d/

# Test pod networking
kubectl run test-pod --image=nginx --rm -it -- /bin/bash
```

## Configuration Options

### Basic Configuration

| Parameter | Description | Default | Required |
|-----------|-------------|---------|----------|
| `access_key` | Alibaba Cloud Access Key | - | Yes |
| `access_secret` | Alibaba Cloud Access Secret | - | Yes |
| `security_group` | Default security group ID | - | Yes |
| `service_cidr` | Kubernetes service CIDR | 10.96.0.0/12 | Yes |
| `vswitches` | vSwitch mapping by zone | - | Yes |
| `max_pool_size` | Maximum ENI pool size | 5 | No |
| `min_pool_size` | Minimum ENI pool size | 0 | No |

### Advanced Configuration

```yaml
eni_conf: |
  {
    "version": "1",
    "access_key": "YOUR_ACCESS_KEY",
    "access_secret": "YOUR_ACCESS_SECRET",
    "security_group": "sg-xxxxxxxxx",
    "service_cidr": "10.96.0.0/12",
    "vswitches": {
      "cn-hangzhou-a": ["vsw-xxxxxxxxx"],
      "cn-hangzhou-b": ["vsw-yyyyyyyyy"]
    },
    "max_pool_size": 10,
    "min_pool_size": 2,
    "eni_tags": {"Owner": "Kubernetes"},
    "trunk_on": true,
    "ipv6": false,
    "custom_route_table_ids": ["rtb-xxxxxxxxx"]
  }
```

## Network Mode Selection

### ENI Mode (Default)
```json
{
  "eniip_virtual_type": "IPVlan"
}
```

### Trunking Mode
```json
{
  "eniip_virtual_type": "IPVlan",
  "trunk_on": true
}
```

### With eBPF Acceleration
```json
{
  "eniip_virtual_type": "IPVlan",
  "enable_ebpf": true
}
```

## Post-Installation Tasks

### 1. Verify Network Connectivity

```bash
# Create test pods
kubectl run pod1 --image=nginx
kubectl run pod2 --image=nginx

# Test connectivity
kubectl exec pod1 -- ping $(kubectl get pod pod2 -o jsonpath='{.status.podIP}')
```

### 2. Configure Network Policies (Optional)

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: test-network-policy
spec:
  podSelector:
    matchLabels:
      app: nginx
  policyTypes:
  - Ingress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: allowed
```

### 3. Enable Monitoring (Optional)

```bash
# Deploy Terway metrics service
kubectl apply -f terway-metric.yml
```

## Troubleshooting Installation

### Common Issues

1. **ENI Quota Exceeded**
   ```bash
   # Check ENI quota in console
   # Increase quota if needed
   ```

2. **Permission Denied**
   ```bash
   # Verify RAM permissions
   # Check access key/secret
   ```

3. **Network Connectivity Issues**
   ```bash
   # Check security group rules
   # Verify vSwitch configuration
   ```

### Logs and Debugging

```bash
# Check Terway logs
kubectl logs -n kube-system -l app=terway

# Check CNI logs
journalctl -u kubelet | grep CNI

# Check system logs
dmesg | grep terway
```

## Upgrading Terway

### Rolling Update

```bash
# Update Terway image
kubectl set image daemonset/terway terway=registry.cn-hangzhou.aliyuncs.com/acs/terway:latest -n kube-system

# Monitor rollout
kubectl rollout status daemonset/terway -n kube-system
```

### Configuration Updates

```bash
# Update configuration
kubectl edit configmap eni-config -n kube-system

# Restart Terway pods
kubectl delete pods -n kube-system -l app=terway
```

## Next Steps

After successful installation:

1. **[Quick Start Tutorial](Quick-Start-Tutorial.md)** - Deploy your first application
2. **[Configuration](user-guide/Configuration.md)** - Fine-tune your setup
3. **[Network Modes](user-guide/Network-Modes.md)** - Understand different modes
4. **[Security Policies](user-guide/Security-Policies.md)** - Secure your network

## Getting Help

If you encounter issues during installation:

- 📖 Check [Common Issues](troubleshooting/Common-Issues.md)
- 🐛 [Report bugs](https://github.com/AliyunContainerService/terway/issues)
- 💬 DingTalk group: `35924643`