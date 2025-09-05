# Frequently Asked Questions (FAQ)

This document answers the most commonly asked questions about Terway CNI plugin.

## General Questions

### Q: What is Terway and why should I use it?

**A:** Terway is a CNI plugin specifically designed for Alibaba Cloud's VPC and ENI technology. You should use Terway if you:
- Run Kubernetes on Alibaba Cloud
- Need high-performance networking without encapsulation overhead
- Want to leverage cloud-native security groups and network policies
- Require IPv6 dual-stack support
- Need eBPF acceleration for maximum performance

### Q: How does Terway differ from other CNI plugins?

**A:** Terway is unique because it:
- **Cloud-native**: Built specifically for Alibaba Cloud VPC/ENI
- **No encapsulation**: Direct communication through VPC routing
- **Security integration**: Native support for Alibaba Cloud security groups
- **High performance**: eBPF acceleration and optimized data path
- **Flexible modes**: Multiple network modes for different scenarios

### Q: Is Terway compatible with standard Kubernetes?

**A:** Yes, Terway is fully compatible with standard Kubernetes and implements the CNI specification. It supports:
- Standard Kubernetes NetworkPolicy
- Service networking
- Ingress controllers
- All standard Kubernetes networking features

## Installation and Setup

### Q: Can I use Terway in self-managed Kubernetes clusters?

**A:** Yes, Terway works in both ACK (managed) and self-managed Kubernetes clusters on Alibaba Cloud ECS instances. However, some features like Trunking may not be available in self-managed clusters.

### Q: What are the minimum requirements for Terway?

**A:** 
- **Kubernetes**: 1.16+ (recommended 1.20+)
- **Kernel**: 3.10+ (recommended 4.18+ for eBPF features)
- **Runtime**: Docker, containerd, or CRI-O
- **Cloud**: Alibaba Cloud ECS instances in VPC

### Q: How many ENIs can I use per node?

**A:** This depends on your ECS instance type. Each instance type has different ENI limits:
- **Small instances** (e.g., ecs.t5.small): 2 ENIs
- **Medium instances** (e.g., ecs.c6.large): 3 ENIs  
- **Large instances** (e.g., ecs.c6.xlarge): 4-8 ENIs
- **Extra large instances**: Up to 15+ ENIs

Check the [ECS documentation](https://help.aliyun.com/document_detail/25378.html) for specific limits.

## Network Modes and Configuration

### Q: Which network mode should I choose?

**A:** Choose based on your requirements:

- **ENI Mode**: Default choice for most scenarios
  - Good performance and security
  - Supports network policies
  - Moderate resource usage

- **Trunking Mode**: For advanced scenarios
  - Maximum flexibility
  - Per-pod security groups
  - Higher resource requirements

### Q: Can I change network modes after installation?

**A:** Changing network modes requires careful planning:
- **Minor changes**: Can be done with configuration updates
- **Major changes**: May require pod restarts or cluster recreation
- **Recommendation**: Test changes in a development environment first

### Q: What happens when I run out of ENI IPs?

**A:** Terway handles IP exhaustion gracefully:
1. **Pool management**: Maintains a pool of available IPs
2. **Dynamic allocation**: Creates new ENIs when needed (within limits)
3. **Degradation mode**: Falls back to shared ENI mode if limits reached
4. **Error handling**: Pods will remain in pending state until IPs are available

## Performance and Troubleshooting

### Q: How can I optimize Terway performance?

**A:** Several optimization strategies:

1. **Enable eBPF acceleration**:
   ```json
   {
     "enable_ebpf": true,
     "eniip_virtual_type": "IPVlan"
   }
   ```

2. **Tune pool sizes**:
   ```json
   {
     "max_pool_size": 10,
     "min_pool_size": 2
   }
   ```

3. **Use appropriate instance types** with sufficient ENI capacity

4. **Optimize kernel settings** for your workload

### Q: Why are my pods stuck in "ContainerCreating" state?

**A:** Common causes and solutions:

1. **ENI quota exceeded**: Check your ENI limits in the console
2. **Insufficient IPs**: Verify vSwitch has available IP addresses
3. **Permission issues**: Ensure RAM permissions are correct
4. **Network connectivity**: Check security groups and routing

**Debug steps**:
```bash
# Check Terway logs
kubectl logs -n kube-system -l app=terway

# Check CNI logs
journalctl -u kubelet | grep CNI

# Check pod events
kubectl describe pod <pod-name>
```

### Q: How do I troubleshoot network connectivity issues?

**A:** Follow this debugging sequence:

1. **Check pod IP assignment**:
   ```bash
   kubectl get pods -o wide
   ```

2. **Test basic connectivity**:
   ```bash
   kubectl exec pod1 -- ping <pod2-ip>
   ```

3. **Check security groups**: Ensure proper ingress/egress rules

4. **Verify routing**: Check route tables and ENI associations

5. **Check Terway status**:
   ```bash
   kubectl exec -n kube-system <terway-pod> -- terway-cli list
   ```

## Security and Network Policies

### Q: How do network policies work with Terway?

**A:** Terway supports both Kubernetes NetworkPolicy and Alibaba Cloud security groups:

- **NetworkPolicy**: Enforced by Terway at the CNI level
- **Security Groups**: Enforced at the ENI level by Alibaba Cloud
- **Combined effect**: Both policies are applied (intersection)

### Q: Can I use custom security groups for specific pods?

**A:** Yes, with Trunking mode you can assign specific security groups to pods:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    k8s.aliyun.com/trunk-on: "true"
    k8s.aliyun.com/security-group-id: "sg-custom123"
spec:
  # ... pod spec
```

### Q: How do I secure communication between namespaces?

**A:** Use NetworkPolicy to control inter-namespace communication:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-cross-namespace
  namespace: production
spec:
  podSelector: {}
  policyTypes:
  - Ingress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: production
```

## Advanced Features

### Q: Does Terway support IPv6?

**A:** Yes, Terway supports IPv6 dual-stack:

```json
{
  "ip_stack": "dual",
  "ipv6": true
}
```

Requirements:
- IPv6-enabled VPC and vSwitches
- Kubernetes 1.20+ with dual-stack enabled
- Compatible instance types

### Q: Can I use Terway with service mesh (Istio)?

**A:** Yes, Terway is compatible with service mesh solutions:
- Works with Istio, Linkerd, and other service meshes
- Supports CNI chaining when needed
- Network policies work alongside mesh policies

### Q: How does Terway handle node failures?

**A:** Terway has built-in resilience:
- **ENI recovery**: Automatically detects and recovers orphaned ENIs
- **IP cleanup**: Releases unused IP addresses
- **Pod migration**: Supports pod rescheduling to healthy nodes
- **State management**: Maintains consistent state across failures

## Monitoring and Observability

### Q: How can I monitor Terway performance?

**A:** Terway provides several monitoring options:

1. **Metrics endpoint**: Prometheus-compatible metrics
2. **Terway CLI**: Real-time debugging information
3. **Logs**: Structured logging for troubleshooting
4. **Hubble integration**: Network flow observability

### Q: What metrics does Terway expose?

**A:** Key metrics include:
- ENI allocation/deallocation rates
- IP pool utilization
- Network policy enforcement stats
- Error rates and latency metrics
- Resource usage statistics

## Migration and Compatibility

### Q: Can I migrate from other CNI plugins to Terway?

**A:** Migration is possible but requires planning:
- **From Flannel/Calico**: Requires cluster network reconfiguration
- **From cloud-provider CNIs**: Usually straightforward
- **Recommendation**: Test migration in staging environment first

### Q: Is Terway compatible with Windows nodes?

**A:** Terway has limited Windows support:
- Basic ENI functionality works
- Some advanced features may not be available
- Linux nodes are recommended for full feature support

## Getting Help

### Q: Where can I get support for Terway?

**A:** Multiple support channels available:

- **Documentation**: [Terway Wiki](Home.md)
- **GitHub Issues**: [Report bugs and feature requests](https://github.com/AliyunContainerService/terway/issues)
- **DingTalk Group**: Join group `35924643`
- **Security Issues**: [kubernetes-security@service.aliyun.com](mailto:kubernetes-security@service.aliyun.com)

### Q: How do I contribute to Terway?

**A:** We welcome contributions! See our [Contributing Guide](developer-guide/Contributing.md) for:
- Code contribution guidelines
- Testing requirements
- Review process
- Community guidelines

---

**Didn't find your question?** 

- Check our [Troubleshooting Guide](troubleshooting/Debugging-Guide.md)
- Search [existing issues](https://github.com/AliyunContainerService/terway/issues)
- Ask in our [DingTalk group](https://www.dingtalk.com/) (ID: 35924643)