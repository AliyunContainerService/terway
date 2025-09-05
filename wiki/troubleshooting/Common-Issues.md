# Common Issues

This guide covers the most frequently encountered issues when using Terway CNI plugin and their solutions.

## Installation Issues

### Issue: Terway Pods Stuck in CrashLoopBackOff

**Symptoms:**
```bash
kubectl get pods -n kube-system | grep terway
# terway-eniip-xxxxx   2/4     CrashLoopBackOff   5          10m
```

**Common Causes & Solutions:**

#### 1. Invalid Access Credentials
```bash
# Check logs for authentication errors
kubectl logs -n kube-system <terway-pod> -c terway

# Look for errors like:
# "InvalidAccessKeyId" or "InvalidAccessKeySecret"
```

**Solution:**
```bash
# Update ConfigMap with correct credentials
kubectl edit configmap eni-config -n kube-system
# Update access_key and access_secret fields
```

#### 2. Insufficient RAM Permissions
**Error:** `Forbidden.Ram.User: User not authorized to operate on the specified resource`

**Solution:**
Ensure your RAM role has the required permissions:
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
        "ecs:DeleteNetworkInterface"
      ],
      "Resource": "*",
      "Effect": "Allow"
    }
  ]
}
```

#### 3. Invalid Security Group or vSwitch
**Error:** `InvalidSecurityGroup.NotFound` or `InvalidVSwitchId.NotFound`

**Solution:**
```bash
# Verify security group exists and is in the correct region
kubectl edit configmap eni-config -n kube-system
# Update security_group and vswitches fields with valid IDs
```

### Issue: CNI Configuration Not Found

**Symptoms:**
```bash
# Pod creation fails with:
# "failed to find CNI configuration file"
```

**Solution:**
```bash
# Check CNI configuration exists
ls -la /etc/cni/net.d/

# If missing, restart Terway pods
kubectl delete pods -n kube-system -l app=terway

# Verify CNI config is created
cat /etc/cni/net.d/10-terway.conf
```

## Pod Creation Issues

### Issue: Pods Stuck in ContainerCreating State

**Symptoms:**
```bash
kubectl get pods
# NAME        READY   STATUS              RESTARTS   AGE
# test-pod    0/1     ContainerCreating   0          5m
```

**Diagnosis Steps:**

#### 1. Check Pod Events
```bash
kubectl describe pod <pod-name>
# Look for events like:
# "failed to set up network"
# "failed to allocate IP"
```

#### 2. Check Terway Logs
```bash
kubectl logs -n kube-system -l app=terway -c terway --tail=100
```

#### 3. Check CNI Logs
```bash
# On the node where pod is scheduled
journalctl -u kubelet | grep CNI | tail -20
```

**Common Causes & Solutions:**

#### 1. ENI Quota Exceeded
**Error:** `QuotaExceeded.Eni: ENI quota exceeded`

**Solution:**
- Check ENI quota in Alibaba Cloud Console
- Request quota increase if needed
- Consider using ENI multi-IP mode instead of exclusive ENI mode

#### 2. No Available IP Addresses
**Error:** `InvalidVSwitchId.IpNotEnough: There are not enough IPs in the vSwitch`

**Solution:**
```bash
# Check vSwitch IP availability in console
# Add more vSwitches or expand CIDR range
kubectl edit configmap eni-config -n kube-system
# Add additional vSwitches to the configuration
```

#### 3. Security Group Restrictions
**Error:** Network interface creation fails due to security group rules

**Solution:**
- Verify security group allows required traffic
- Check security group attachments to vSwitches
- Ensure security group rules allow pod communication

### Issue: IP Allocation Timeouts

**Symptoms:**
```bash
# Pods take very long to get IP addresses
# Intermittent IP allocation failures
```

**Diagnosis:**
```bash
# Check ENI pool status
TERWAY_POD=$(kubectl get pods -n kube-system -l app=terway -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n kube-system $TERWAY_POD -- terway-cli show resource_pool
```

**Solutions:**

#### 1. Optimize Pool Configuration
```bash
kubectl edit configmap eni-config -n kube-system
# Increase pool sizes:
{
  "max_pool_size": 10,
  "min_pool_size": 3
}
```

#### 2. Check Node Resource Limits
```bash
# Verify node can accommodate more ENIs
kubectl exec -n kube-system $TERWAY_POD -- terway-cli show factory
```

## Network Connectivity Issues

### Issue: Pod-to-Pod Communication Failures

**Symptoms:**
```bash
# Ping between pods fails
kubectl exec pod1 -- ping <pod2-ip>
# ping: connect: Network is unreachable
```

**Diagnosis Steps:**

#### 1. Check Network Policies
```bash
# List network policies that might block traffic
kubectl get networkpolicies --all-namespaces

# Check specific policy details
kubectl describe networkpolicy <policy-name> -n <namespace>
```

#### 2. Check Security Groups
- Verify security group rules allow pod-to-pod communication
- Check if pods are in different security groups

#### 3. Check Route Tables
```bash
# On nodes, check route tables
ip route show table all
```

**Solutions:**

#### 1. Update Network Policies
```bash
# Allow required traffic in NetworkPolicy
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-pod-communication
spec:
  podSelector: {}
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - podSelector: {}
  egress:
  - to:
    - podSelector: {}
```

#### 2. Update Security Group Rules
Add rules to allow traffic between pods:
- Protocol: All
- Port Range: All  
- Source: Security Group ID (self-referencing)

### Issue: Service Discovery Failures

**Symptoms:**
```bash
# DNS resolution fails for services
kubectl exec pod -- nslookup kubernetes.default.svc.cluster.local
# ** server can't find kubernetes.default.svc.cluster.local: NXDOMAIN
```

**Solutions:**

#### 1. Check CoreDNS Status
```bash
kubectl get pods -n kube-system | grep coredns
kubectl logs -n kube-system <coredns-pod>
```

#### 2. Verify Service CIDR Configuration
```bash
kubectl edit configmap eni-config -n kube-system
# Ensure service_cidr matches cluster service CIDR
```

#### 3. Check Network Policy for DNS
```bash
# Ensure DNS traffic is allowed
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-dns
spec:
  podSelector: {}
  policyTypes:
  - Egress
  egress:
  - to: []
    ports:
    - protocol: UDP
      port: 53
```

## Performance Issues

### Issue: High Network Latency

**Symptoms:**
```bash
# High RTT in ping tests
kubectl exec pod1 -- ping pod2-ip
# 64 bytes from pod2-ip: icmp_seq=1 ttl=64 time=50.2 ms
```

**Solutions:**

#### 1. Enable eBPF Acceleration
```bash
kubectl edit configmap eni-config -n kube-system
# Add or update:
{
  "enable_ebpf": true,
  "eniip_virtual_type": "IPVlan"
}
```

#### 2. Optimize Instance Types
- Use instances with higher network performance
- Ensure instances support ENI multi-IP
- Consider instances with SR-IOV support

#### 3. Check Network Configuration
```bash
# Verify no unnecessary routing
kubectl exec -n kube-system $TERWAY_POD -- terway-cli mapping
```

### Issue: Low Network Throughput

**Symptoms:**
```bash
# Poor iperf3 results between pods
kubectl exec client -- iperf3 -c server-ip
# [ ID] Interval           Transfer     Bitrate
# [  5]   0.00-10.00  sec   100 MBytes   83.9 Mbits/sec
```

**Solutions:**

#### 1. Enable Datapath v2
```bash
kubectl edit configmap eni-config -n kube-system
# Add:
{
  "enable_datapath_v2": true
}
```

#### 2. Tune Network Settings
```bash
# On nodes, tune network parameters
echo 'net.core.rmem_max = 134217728' >> /etc/sysctl.conf
echo 'net.core.wmem_max = 134217728' >> /etc/sysctl.conf
sysctl -p
```

## Monitoring and Observability Issues

### Issue: Missing Metrics

**Symptoms:**
- Terway metrics not appearing in monitoring systems
- No visibility into ENI usage

**Solutions:**

#### 1. Enable Metrics Service
```bash
# Deploy metrics service
kubectl apply -f terway-metric.yml
```

#### 2. Configure Prometheus Scraping
```yaml
# Add to Prometheus configuration
- job_name: 'terway'
  kubernetes_sd_configs:
  - role: pod
    namespaces:
      names:
      - kube-system
  relabel_configs:
  - source_labels: [__meta_kubernetes_pod_label_app]
    action: keep
    regex: terway
```

### Issue: Log Collection Problems

**Symptoms:**
- Unable to collect Terway logs
- Logs not forwarded to central logging

**Solutions:**

#### 1. Configure Log Collection
```yaml
# Fluentd/Fluent Bit configuration
<source>
  @type tail
  path /var/log/pods/*terway*/terway/*.log
  pos_file /var/log/fluentd-terway.log.pos
  tag kubernetes.terway.*
  format json
</source>
```

#### 2. Increase Log Verbosity
```bash
kubectl edit daemonset terway -n kube-system
# Add environment variable:
- name: LOG_LEVEL
  value: "debug"
```

## Degradation and Recovery

### Issue: Terway Controlplane Unavailable

**Symptoms:**
- New pod creation fails
- IP allocation errors
- Degradation mode activated

**Solutions:**

#### 1. Check Controlplane Status
```bash
kubectl get pods -n kube-system -l app=terway-controlplane
kubectl logs -n kube-system -l app=terway-controlplane
```

#### 2. Verify Degradation Configuration
```bash
kubectl edit configmap eni-config -n kube-system
# Check degradation settings:
{
  "degradation": {
    "enable": true,
    "l0_threshold": 10,
    "l1_threshold": 5
  }
}
```

#### 3. Manual Recovery
```bash
# Force reconciliation of resources
kubectl annotate nodes --all terway.aliyun.com/reconcile=true
```

## Getting Help

If you can't resolve the issue:

### 1. Collect Diagnostic Information
```bash
# Create a diagnostic bundle
kubectl get pods -n kube-system -o wide > terway-pods.txt
kubectl describe pods -n kube-system -l app=terway > terway-describe.txt
kubectl logs -n kube-system -l app=terway --tail=1000 > terway-logs.txt
kubectl get configmap eni-config -n kube-system -o yaml > terway-config.yaml
```

### 2. Check System Resources
```bash
# Node resource usage
kubectl top nodes
kubectl describe nodes

# Check disk space and system logs
df -h
dmesg | tail -50
```

### 3. Report Issues
- 🐛 [GitHub Issues](https://github.com/AliyunContainerService/terway/issues)
- 📖 [FAQ](FAQ.md) for additional questions
- 💬 DingTalk group: `35924643`
- 📧 Security issues: [kubernetes-security@service.aliyun.com](mailto:kubernetes-security@service.aliyun.com)

When reporting issues, include:
- Kubernetes version
- Terway version
- Instance types and specifications
- Configuration files (sanitized)
- Logs and error messages
- Steps to reproduce

## Prevention Tips

1. **Regular Monitoring**: Set up alerts for ENI quota usage
2. **Resource Planning**: Plan ENI and IP requirements in advance
3. **Testing**: Test network policies and configurations in staging
4. **Updates**: Keep Terway updated to latest stable version
5. **Documentation**: Maintain runbooks for common operations