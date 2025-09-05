# Quick Start Tutorial

This tutorial will guide you through deploying your first application with Terway CNI in just a few minutes.

## Prerequisites

Before starting, ensure you have:

- ✅ **ACK cluster** or self-managed Kubernetes cluster with Terway installed
- ✅ **kubectl** configured to access your cluster
- ✅ **Basic familiarity** with Kubernetes concepts

> **New to Terway?** Check the [Installation Guide](Installation-Guide.md) first.

## Step 1: Verify Terway Installation

First, let's verify that Terway is running correctly in your cluster:

```bash
# Check Terway pods are running
kubectl get pods -n kube-system | grep terway

# Expected output:
# terway-eniip-xxxxx   4/4     Running   0          1d
# terway-eniip-yyyyy   4/4     Running   0          1d
```

```bash
# Check CNI configuration
ls -la /etc/cni/net.d/

# Expected output should include:
# 10-terway.conf or 10-terway.conflist
```

## Step 2: Deploy Your First Application

Let's deploy a simple nginx application to test Terway networking:

```bash
# Create a test namespace
kubectl create namespace terway-demo

# Deploy nginx application
cat << EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-demo
  namespace: terway-demo
  labels:
    app: nginx-demo
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx-demo
  template:
    metadata:
      labels:
        app: nginx-demo
    spec:
      containers:
      - name: nginx
        image: nginx:1.20
        ports:
        - containerPort: 80
        resources:
          limits:
            cpu: 200m
            memory: 256Mi
          requests:
            cpu: 100m
            memory: 128Mi
---
apiVersion: v1
kind: Service
metadata:
  name: nginx-service
  namespace: terway-demo
spec:
  selector:
    app: nginx-demo
  ports:
  - port: 80
    targetPort: 80
  type: ClusterIP
EOF
```

## Step 3: Verify Pod Networking

Check that your pods have been assigned IP addresses and are running:

```bash
# Check pod status and IP addresses
kubectl get pods -n terway-demo -o wide

# Expected output:
# NAME                         READY   STATUS    RESTARTS   AGE   IP              NODE
# nginx-demo-xxxx-xxxxx        1/1     Running   0          1m    192.168.1.10    node-1
# nginx-demo-yyyy-yyyyy        1/1     Running   0          1m    192.168.1.11    node-2
# nginx-demo-zzzz-zzzzz        1/1     Running   0          1m    192.168.1.12    node-1
```

> **Note**: The IP addresses shown are from the VPC subnet, demonstrating Terway's direct ENI integration.

## Step 4: Test Network Connectivity

### Pod-to-Pod Communication

Test direct pod-to-pod communication:

```bash
# Get pod IPs
POD1=$(kubectl get pods -n terway-demo -o jsonpath='{.items[0].metadata.name}')
POD2_IP=$(kubectl get pods -n terway-demo -o jsonpath='{.items[1].status.podIP}')

# Test connectivity from pod1 to pod2
kubectl exec -n terway-demo $POD1 -- ping -c 3 $POD2_IP
```

Expected output:
```
PING 192.168.1.11 (192.168.1.11): 56 data bytes
64 bytes from 192.168.1.11: seq=0 ttl=64 time=0.123 ms
64 bytes from 192.168.1.11: seq=1 ttl=64 time=0.089 ms
64 bytes from 192.168.1.11: seq=2 ttl=64 time=0.095 ms
```

### Service Discovery

Test Kubernetes service discovery:

```bash
# Test service resolution
kubectl exec -n terway-demo $POD1 -- nslookup nginx-service

# Test HTTP connectivity to service
kubectl exec -n terway-demo $POD1 -- curl -s nginx-service
```

### External Connectivity

Test connectivity to external services:

```bash
# Test DNS resolution
kubectl exec -n terway-demo $POD1 -- nslookup google.com

# Test HTTP connectivity
kubectl exec -n terway-demo $POD1 -- curl -s -I https://www.alibaba.com
```

## Step 5: Explore Terway Features

### Check ENI Information

Use Terway CLI to inspect network resources:

```bash
# Get Terway pod name
TERWAY_POD=$(kubectl get pods -n kube-system -l app=terway -o jsonpath='{.items[0].metadata.name}')

# Check resource mapping
kubectl exec -n kube-system $TERWAY_POD -- terway-cli mapping

# Check ENI information
kubectl exec -n kube-system $TERWAY_POD -- terway-cli show factory
```

### Monitor Resource Usage

```bash
# Check IP pool status
kubectl exec -n kube-system $TERWAY_POD -- terway-cli show resource_pool

# Check network service status
kubectl exec -n kube-system $TERWAY_POD -- terway-cli show network_service
```

## Step 6: Test Network Policies (Optional)

Let's test Kubernetes NetworkPolicy with Terway:

```bash
# Create a test client pod
cat << EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: test-client
  namespace: terway-demo
  labels:
    app: test-client
spec:
  containers:
  - name: client
    image: busybox:1.35
    command: ["sleep", "3600"]
EOF
```

```bash
# Create a network policy to deny all ingress to nginx pods
cat << EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-nginx-ingress
  namespace: terway-demo
spec:
  podSelector:
    matchLabels:
      app: nginx-demo
  policyTypes:
  - Ingress
  ingress: []  # Empty ingress rules = deny all
EOF
```

```bash
# Test that connectivity is now blocked
kubectl exec -n terway-demo test-client -- curl -m 5 nginx-service

# This should timeout/fail due to the network policy
```

```bash
# Allow specific ingress from test-client
cat << EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-test-client
  namespace: terway-demo
spec:
  podSelector:
    matchLabels:
      app: nginx-demo
  policyTypes:
  - Ingress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: test-client
    ports:
    - protocol: TCP
      port: 80
EOF
```

```bash
# Test that connectivity is now allowed
kubectl exec -n terway-demo test-client -- curl -s nginx-service
```

## Step 7: Performance Testing (Optional)

Test network performance between pods:

```bash
# Deploy iperf3 server
cat << EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: iperf3-server
  namespace: terway-demo
spec:
  containers:
  - name: iperf3
    image: networkstatic/iperf3
    command: ["iperf3", "-s"]
    ports:
    - containerPort: 5201
EOF
```

```bash
# Deploy iperf3 client and run test
cat << EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: iperf3-client
  namespace: terway-demo
spec:
  containers:
  - name: iperf3
    image: networkstatic/iperf3
    command: ["sleep", "3600"]
EOF
```

```bash
# Wait for pods to be ready
kubectl wait --for=condition=Ready pod/iperf3-server -n terway-demo --timeout=60s
kubectl wait --for=condition=Ready pod/iperf3-client -n terway-demo --timeout=60s

# Get server IP
SERVER_IP=$(kubectl get pod iperf3-server -n terway-demo -o jsonpath='{.status.podIP}')

# Run performance test
kubectl exec -n terway-demo iperf3-client -- iperf3 -c $SERVER_IP -t 10
```

## Step 8: Cleanup

When you're done experimenting, clean up the resources:

```bash
# Delete the demo namespace (removes all resources)
kubectl delete namespace terway-demo
```

## What's Next?

Congratulations! You've successfully:

✅ Verified Terway installation  
✅ Deployed applications with Terway networking  
✅ Tested pod-to-pod communication  
✅ Explored Terway CLI tools  
✅ Tested network policies  
✅ Measured network performance  

### Continue Learning

Now that you have the basics working, explore more advanced topics:

1. **[Network Modes](user-guide/Network-Modes.md)** - Learn about different networking modes
2. **[Configuration](user-guide/Configuration.md)** - Advanced configuration options
3. **[Security Policies](user-guide/Security-Policies.md)** - Advanced network security
4. **[IPv6 Support](user-guide/IPv6-Support.md)** - Dual-stack networking
5. **[eBPF Acceleration](user-guide/eBPF-Acceleration.md)** - High-performance networking
6. **[Troubleshooting](troubleshooting/Debugging-Guide.md)** - Debugging network issues

### Production Considerations

Before deploying to production:

- **[Security Policies](user-guide/Security-Policies.md)** - Implement proper network segmentation
- **[Monitoring](reference/Metrics.md)** - Set up monitoring and alerting
- **[High Availability](Installation-Guide.md#configuration-options)** - Configure for HA
- **[Resource Planning](troubleshooting/FAQ.md#q-how-many-enis-can-i-use-per-node)** - Plan ENI and IP requirements

## Getting Help

If you encounter issues:

- 📖 Check [FAQ](troubleshooting/FAQ.md) for common questions
- 🐛 [Report issues](https://github.com/AliyunContainerService/terway/issues) on GitHub
- 💬 Join DingTalk group: `35924643`
- 📧 Security issues: [kubernetes-security@service.aliyun.com](mailto:kubernetes-security@service.aliyun.com)

Happy networking with Terway! 🚀