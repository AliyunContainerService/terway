# Configure Exclusive ENI Network Mode for Node Pools

Exclusive ENI mode is a strategy that provides optimal network performance for Pods, particularly suitable for scenarios with strict network performance requirements. For example, when processing big data analytics, real-time stream processing, or running network-sensitive applications (such as video streaming, online gaming, or scientific computing), this network mode can provide high network throughput and extremely low network latency. In high-frequency trading scenarios, this network mode can provide multicast capabilities.

## Prerequisites and Limitations

### Version Requirements

- **ECS instances**: Terway version v1.11.0 or higher is required
- **Lingjun instances**: Terway version v1.14.3 or higher is required
- To upgrade the component version, refer to [Terway upgrade documentation](https://help.aliyun.com/document_detail/97467.html)

### Limitations

- After enabling dual-stack on the cluster, adding nodes will be subject to ECS instance type restrictions in shared ENI mode. The ratio of primary private IP address + secondary private IPv4 addresses to IPv6 addresses must be 1:1. For information about the number of IPv4 and IPv6 addresses supported by ECS instances, refer to [ECS Instance Specifications](https://help.aliyun.com/document_detail/25378.html)
- Lingjun instances do not support IPv6 dual-stack
- Pods using exclusive ENI do not support eBPF network acceleration and NetworkPolicy
- When using exclusive ENI, you must use newly created nodes. If you use existing nodes, the existing elastic network interfaces on the nodes will not be used
- **Exclusive ENI only takes effect for newly created nodes**. After configuration, it cannot be adjusted to shared ENI, and existing shared ENI nodes cannot be modified to exclusive ENI mode

## Configuration Overview

Exclusive ENI is a node pool mode provided by Terway. For a detailed comparison between exclusive ENI and shared ENI, refer to [Shared ENI Mode vs Exclusive ENI Mode](https://help.aliyun.com/document_detail/25378.html).

Follow the steps below to plan and create an exclusive ENI node pool. After successful creation, you can schedule Pods to the target node pool.

## Step 1: Plan the Exclusive ENI Node Pool

### Node Capacity Planning

- In exclusive ENI mode, the maximum number of Pods per node is relatively low
- Worker nodes must have more than 6 elastic network interfaces to join the cluster
- For information on how to calculate the number of elastic network interfaces, refer to [ECS Instance Specifications](https://help.aliyun.com/document_detail/25378.html)

### Network Configuration Planning

Plan the vSwitches and security groups to be used by Pods.

Terway supports multiple configuration methods with the following priority (from highest to lowest):

1. [Configure Fixed IP and Independent vSwitch and Security Group for Pods](./terway-trunk.md#podnetworking-配置)
2. [Node-level Network Configuration](./dynamic-config.md)
3. Cluster default configuration [Custom Terway Configuration Parameters](./dynamic-config.md)

> **Important**: Ensure that the vSwitches corresponding to the node availability zones are configured in the above configurations. Otherwise, Pods will fail to be created.
> **Note**: Lingjun node pools do not support [Configure Fixed IP and Independent vSwitch and Security Group for Pods](./terway-trunk.md#podnetworking-配置).

## Step 2: Create Exclusive ENI Node Pool and Verify

### Create the Node Pool

1. Create a new node pool following the [Create and Manage Node Pools](https://help.aliyun.com/document_detail/196931.html) guide
2. During node pool creation, add the label `k8s.aliyun.com/exclusive-mode-eni-type: eniOnly` to the nodes

   ```yaml
   labels:
     k8s.aliyun.com/exclusive-mode-eni-type: eniOnly
   ```

3. **Recommended**: Configure taints to prevent other Pods from being scheduled to the exclusive ENI node pool

   ```yaml
   taints:
     - key: exclusive-eni
       value: "true"
       effect: NoSchedule
   ```

> **Important**: Configure the label when creating the node pool. Nodes that have already been created cannot be switched to exclusive ENI mode. If you configure incorrectly, delete the node pool and recreate it.

### Verify Exclusive ENI Mode is Enabled

Execute the following command to query the node's allocatable resources and check if the node has successfully enabled exclusive ENI mode:

```bash
kubectl describe node <node-name>
```

Expected output:

```yaml
Capacity:
  aliyun/eni: 7
  cpu: 16
  ephemeral-storage: 123460788Ki
  hugepages-1Gi: 0
  hugepages-2Mi: 0
  memory: 31555380Ki
  pods: 213
Allocatable:
  aliyun/eni: 7
  cpu: 15890m
  ephemeral-storage: 113781462033
  hugepages-1Gi: 0
  hugepages-2Mi: 0
  memory: 28587828Ki
  pods: 213
```

The presence of `aliyun/eni` in the expected output indicates that exclusive ENI mode has been successfully enabled.

## Step 3: Schedule Pods to Exclusive ENI Node Pool

You can schedule Pods to the exclusive ENI node pool using NodeAffinity or the PodNetworking custom resource.

### Using NodeAffinity

**Limitation**: NodeAffinity does not support Pod-level configuration (using fixed IP, configuring independent vSwitch and security group).

Example:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-exclusive-eni
spec:
  replicas: 2
  template:
    spec:
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: k8s.aliyun.com/exclusive-mode-eni-type
                operator: In
                values:
                - eniOnly
      containers:
      - name: nginx
        image: nginx:latest
```

### Using PodNetworking

**Advantage**: PodNetworking supports configuring vSwitch and security group at the Pod level, as well as using Pod fixed IP. For specific operations, refer to [Configure Fixed IP and Independent vSwitch and Security Group for Pods](./terway-trunk.md#podnetworking-配置).

You can refer to the YAML example below. Set `eniType` to `ENI` in the `eniOptions` field to schedule Pods to the exclusive ENI node pool:

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: enionly
spec:
  eniOptions:
    eniType: ENI
  allocationType:
    type: Elastic
  selector:
    podSelector:
      matchLabels:
        network: enionly
  vSwitchOptions:
    - vsw-xxxxx
  securityGroupIDs:
    - sg-xxxxx
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-exclusive-eni
spec:
  replicas: 2
  template:
    metadata:
      labels:
        network: enionly
    spec:
      containers:
      - name: nginx
        image: nginx:latest
```

## Verify Pod is Using Exclusive ENI

Terway creates a PodENI resource with the same name and namespace as the Pod to record the network configuration information used.

You can query it using the following method:

```bash
kubectl get podeni <pod-name> -n <namespace> -oyaml
```

Expected output:

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodENI
metadata:
  annotations:
    k8s.aliyun.com/pod-uid: 05590939-fc51-47ab-a204-3dd187233bca
  creationTimestamp: "2024-09-13T08:09:27Z"
  finalizers:
    - pod-eni
  generation: 1
  labels:
    k8s.aliyun.com/node: cn-hangzhou.172.XX.XX.25
  name: example-9d557694f-rcdzs
  namespace: default
  resourceVersion: "1131123"
spec:
  allocations:
    - allocationType:
        type: Elastic
      eni:
        attachmentOptions: {}
        id: eni-xxxx
        mac: 00:16:3e:37:xx:xx
        securityGroupIDs:
          - sg-xxxx
        vSwitchID: vsw-xxxx
        zone: cn-hangzhou-j
      ipv4: 172.16.0.30
      ipv4CIDR: 172.16.0.0/24
      ipv6: 2408:4005:xxxx:xxxx:xxxx:xxxx:xxxx:9ad4
      ipv6CIDR: 2408:4005:39c:xxxx::/64
      zone: cn-hangzhou-j
status:
  eniInfos:
    eni-xxxx:
      id: eni-xxxx
      status: Bind
      type: Secondary
      instanceID: i-xxxx
  phase: Bind
```

In the output, check the `status.eniInfos` section. If the ENI type is `Secondary` (not `Trunk`), it indicates that the Pod is using exclusive ENI mode.

## Troubleshooting

### Pod Cannot Be Created

1. **Check node capacity**: Ensure the node has more than 6 ENIs available

   ```bash
   kubectl describe node <node-name> | grep "aliyun/eni"
   ```

2. **Check vSwitch configuration**: Ensure the vSwitches in the node's availability zone are configured correctly

   ```bash
   kubectl get podnetworking <podnetworking-name> -oyaml
   ```

3. **Check node labels**: Verify the node has the correct label

   ```bash
   kubectl get node <node-name> --show-labels | grep exclusive-mode-eni-type
   ```

### Pod Not Scheduled to Exclusive ENI Node

1. **Check NodeAffinity or PodNetworking**: Ensure the Pod's scheduling configuration matches the exclusive ENI node pool
2. **Check node taints**: If taints are configured, ensure the Pod has corresponding tolerations
3. **Check node resources**: Ensure the node has sufficient resources (CPU, memory, ENI)

## Best Practices

1. **Use dedicated node pools**: Create dedicated node pools for exclusive ENI mode to avoid mixing with shared ENI nodes
2. **Configure taints**: Use taints to prevent non-exclusive ENI Pods from being scheduled to exclusive ENI nodes
3. **Plan capacity**: Carefully plan the number of Pods per node based on ENI capacity
4. **Monitor resources**: Regularly monitor ENI usage and node capacity
5. **Network configuration**: Prefer PodNetworking for flexible network configuration at the Pod level

## Related Documentation

- [Terway Trunk Mode](./terway-trunk.md)
- [Multi-Network Configuration](./multi-network.md)
- [Dynamic Configuration](./dynamic-config.md)
- [Shared ENI Mode vs Exclusive ENI Mode](https://help.aliyun.com/document_detail/25378.html)
