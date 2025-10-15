# Pod 多网卡配置 (Multi-Network Configuration for Pods)

Pod 可以配置多个弹性网卡（ENI），本文描述了如何为 Pod 配置多个网卡的操作。

## 使用限制 (Limitations)

+ ECS 支持自定义路由配置（defaultRoute 和 routes 参数）
+ ACS 不支持自定义路由配置（defaultRoute 和 routes 参数）
+ 在ACK 上，你可以通过 eniType 的设置来控制 Pod 使用 Trunk 还是使用独占 ENI

## 配置方式概述 (Configuration Overview)

**PodNetworking 资源说明：**

+ `PodNetworking` 是一个集群级别的自定义资源，用于描述一个网络平面的配置，包含交换机（vSwitch）、安全组（SecurityGroup）等网络配置
+ Pod 可以引用 1-n 个网络平面配置
+ 在 Pod 中，只能有一个网络平面设置为 default 路由，其他网络平面可以配置明细路由
+ **注意**：目前 ACS 不支持路由配置功能

**配置关系：**

+ Pod 与 PodNetworking 呈现多对多的引用关系
+ 只有在创建 Pod 时，PodNetworking 配置才会被应用
+ 修改 PodNetworking 不会影响已创建的 Pod

### 配置示例

创建 PodNetworking 的示例如下：

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: example
spec:
  allocationType:
    type: Fixed # Pod IP分配的策略，可以取值为 Elastic 或 Fixed
    releaseStrategy: TTL # 只有将 type 配置为 Fixed 时，releaseStrategy 参数才有效
    releaseAfter: "1h" # 在 releaseStrategy 为 TTL 模式下，releaseAfter 参数才有效
  selector: { } # 在多网卡场景下不可填写
  securityGroupIDs:
  - sg-bpxxxx
  vSwitchOptions:
  - vsw-bpxxxx
```

### 参数说明

| 参数                                        | 子参数                    | 说明                                                                                                                                                                                                                                                                                         |
|:------------------------------------------|:-----------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **allocationType**<br/>（Pod IP 分配策略）      | type                   | 取值范围：<br/>• `Elastic`：弹性 IP 策略。Pod 删除后，IP 资源被释放<br/>• `Fixed`：固定 IP 策略。启用后，该 PodNetworking 只对有状态（StatefulSet）的 Pod 生效。关于 StatefulSet，请参见 [Kubernetes有状态服务-StatefulSet使用最佳实践](https://developer.aliyun.com/article/629007)<br/><br/>**说明**：如果您使用了固定 IP 策略，Pod 重建后的可用区将被添加约束，来确保和第一次调度的可用区匹配 |
|                                           | releaseStrategy        | IP 释放策略，只有将 `type` 配置为 `Fixed` 时，该参数才有效。参数取值范围如下：<br/>• `TTL`：延迟释放 IP 策略。当 Pod 被删除一段时间后，才会释放 IP，最小值为 5 分钟<br/>• `Never`：不释放 IP 策略。当您无需使用 IP 时，需要自行删除 PodENI 资源                                                                                                                             |
|                                           | releaseAfter           | 延迟回收时间。仅在 `releaseStrategy` 为 `TTL` 模式下生效，支持时间格式为 Go time type，例如 `2h45m`、`5m0s`。关于 Go time type，请参见 [Go time type](https://pkg.go.dev/time#ParseDuration)                                                                                                                                 |
| **selector**<br/>（标签选择器，用于选择应用该网络配置的 Pod） | -                      | **在多网络平面场景中，不可配置此字段**。多网卡通过 Pod 注解 `k8s.aliyun.com/pod-networks-request` 进行配置                                                                                                                                                                                                              |
| **vSwitchOptions**                        | -                      | 用于配置 Pod 使用的 vSwitch，多个 vSwitchID 之间为**或**的关系。Pod 仅能使用一个 vSwitch，Terway 将选择一个符合条件的 vSwitch<br/>• 您的 Pod 部署的可用区将被添加约束，来确保这些可用区保持和您配置的 vSwitchOptions 列表中的可用区一致<br/>• 请确保 vSwitchOptions 中的 vSwitch 所在可用区与您指定调度的节点可用区一致，并且拥有足够的剩余 IP 资源，否则 Pod 将无法创建                                         |
| **vSwitchSelectOptions**<br/>（交换机选择策略）    | vSwitchSelectionPolicy | 取值范围：<br/>• `ordered`：默认值，按填写的交换机顺序选择<br/>• `most`：优先使用剩余 IP 多的交换机<br/>• `random`：随机选择交换机                                                                                                                                                                                                  |
| **securityGroupIDs**                      | -                      | 可配置多个安全组 ID，配置多个安全组时将同时生效，安全组数量小于等于 10 个                                                                                                                                                                                                                                                   |
| **eniOptions**<br/>（弹性网卡类型配置）             | eniType                | 在多网络平面场景中，ACK 可以配置此字段，取值范围 `Default`, `ENI`, `Trunk` ACS 只可以填写 `Default`。 多个网络平面配置必须一致，才可以被pod 使用。                                                                                                                                                                                         |

### 验证 PodNetworking 状态

创建 PodNetworking 后，请确保 PodNetworking 状态变为 `status: Ready`：

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  generation: 1
  name: default
  resourceVersion: "5744"
  uid: 65903fc5-9bd0-4e3e-bfe2-c9f1081dd680
spec:
  allocationType:
    type: Elastic
  eniOptions:
    eniType: Default
  securityGroupIDs:
  - sg-bp15u1lb14uqfp5ytexk
  vSwitchOptions:
  - vsw-a
  - vsw-b
  - vsw-c
  vSwitchSelectOptions:
    vSwitchSelectionPolicy: ordered
status:
  status: Ready  # 确保状态为 Ready
  updateAt: "2025-03-24T06:10:00Z"
  vSwitches:
  - id: vsw-a
    zone: cn-hangzhou-g
  - id: vsw-b
    zone: cn-hangzhou-i
  - id: vsw-c
    zone: cn-hangzhou-h
```

## 步骤三：为 Pod 关联网络平面 (Step 3: Associate Network Planes to Pod)

通过在 Pod 的 annotation 中添加 `k8s.aliyun.com/pod-networks-request` 注解来配置多网卡：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: multi-network-pod
  annotations:
    k8s.aliyun.com/pod-networks-request: |
      [
        {"interfaceName":"eth0","network":"default","defaultRoute": true},
        {"interfaceName":"eth1","network":"secondary"}
      ]
spec:
  containers:
  - name: nginx
    image: nginx:latest
```

### 注解参数说明

| 参数                | 说明                                                                                                                           |
|-------------------|------------------------------------------------------------------------------------------------------------------------------|
| **interfaceName** | 类型：string<br/>接口名称，必须唯一<br/>**必填**                                                                                           |
| **network**       | 类型：string<br/>PodNetworking 资源的名称<br/>**必填**                                                                                 |
| **defaultRoute**  | 类型：bool<br/>是否设置为默认路由，默认 eth0 为 default<br/>可选<br/><br/>**注意**：当前 ACS 环境下暂不支持此参数                                             |
| **routes**        | 类型：Route 数组<br/>额外的明细路由配置<br/>可选<br/><br/>**注意**：当前 ACS 环境下暂不支持此参数<br/><br/>Route 结构：<br/>`type Route struct { Dst string }` |

### 完整配置示例

以下是一个完整的多网卡 Pod 配置示例：

```yaml
# 1. 创建主网络平面
---
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: primary-network
spec:
  allocationType:
    type: Elastic
  securityGroupIDs:
  - sg-primary-xxxx
  vSwitchOptions:
  - vsw-primary-a
  - vsw-primary-b

# 2. 创建辅助网络平面
---
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: secondary-network
spec:
  allocationType:
    type: Elastic
  securityGroupIDs:
  - sg-secondary-xxxx
  vSwitchOptions:
  - vsw-secondary-a
  - vsw-secondary-b

# 3. 创建多网卡 Pod
---
apiVersion: v1
kind: Pod
metadata:
  name: multi-network-app
  annotations:
    k8s.aliyun.com/pod-networks-request: |
      [
        {"interfaceName":"eth0","network":"primary-network","defaultRoute": true},
        {"interfaceName":"eth1","network":"secondary-network"}
      ]
spec:
  containers:
  - name: app
    image: nginx:latest
    ports:
    - containerPort: 80
```

## 注意事项 (Important Notes)

1. **ENI 类型限制**：多网卡功能目前仅支持 Trunk ENI或者独占 ENI 。不支持灵骏节点。
2. **可用区约束**：使用多网卡时，Pod 调度会受到 vSwitch 所在可用区的约束
3. **IP 地址分配**：每个网络接口都会从对应的 vSwitch 中分配一个 IP 地址
4. **路由配置**：当前 ACS 环境暂不支持自定义路由配置（defaultRoute 和 routes 参数）
5. **资源清理**：删除 Pod 后，对应的网络资源会根据 allocationType 配置进行回收
6. **安全组数量**：每个 PodNetworking 最多可配置 10 个安全组
