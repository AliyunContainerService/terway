# Terway trunk 模式

terway trunk 模式使用ECS 弹性网卡（ENI）构建，相比之前 ENI 多IP 模式，提供更加灵活的配置能力
trunk 模式和 ENI 多 IP 模式互不影响，trunk 不占用ENI 多 IP 的配额

trunk 模式提供下面功能

- 固定IP
- Pod 配置独立 vSwitch、安全组
- 可为一组 Pod或 namespace 进行配置

## design

## 支持情况

terway trunk 功能依赖 ECS 的 ENI 与 ENI 的 trunk 能力
目前在 `g6`、`g7` 系列部分机型上提供

[机型可以参考](https://help.aliyun.com/document_detail/25378.html)

## usage

在 eni trunking 模式中，用户通过自定义资源方式描述容器网络配置、Pod 映射关系
terway-controlplane 会观测用户定义的资源来决定 Pod 使用的网络模式

使用前需要在 `kube-system/eni-config` 里面配置启用 trunk 模式

```yaml
❯ kubectl get cm -n kube-system eni-config -oyaml
apiVersion: v1
data:
  10-terway.conf: |
    {
      "cniVersion": "0.4.0",
      "name": "terway",
      "eniip_virtual_type": "IPVlan",
      "type": "terway"
    }
  disable_network_policy: "false"
  eni_conf: |
    {
      "min_pool_size": 0,
      "enable_eni_trunking": true,   <----- 启用 Trunk
      ...
    }
kind: ConfigMap
```

> 当前 trunk 模式只能在 Terway 多IP 模式下启用
>
> 修改 config-map 后请重启 Terway pod 生效配置

terway-controlplane 需要通过  openAPI 去操作网络资源，请确保授权中包含以下 [`RAM 权限`](https://ram.console.aliyun.com/)

```json
{
  "Version": "1",
  "Statement": [{
    "Action": [
      "ecs:CreateNetworkInterface",
      "ecs:DescribeNetworkInterfaces",
      "ecs:AttachNetworkInterface",
      "ecs:DetachNetworkInterface",
      "ecs:DeleteNetworkInterface"
    ],
    "Resource": [
      "*"
    ],
    "Effect": "Allow"
  },
    {
      "Action": [
        "vpc:DescribeVSwitches"
      ],
      "Resource": [
        "*"
      ],
      "Effect": "Allow"
    }
  ]
}
```

### podNetworking 配置

`podNetworking` 是 trunk 模式下引入的自定义资源，用来描述一个网络平面的配置信息。 一个网络平面可以配置独立的 vSwitch、安全组等信息。集群内可以配置多个网络平面信息。
`podNetworking` 通过标签选择器来匹配 Pod，被匹配的 Pod 将使用 trunking 模式

配置总览

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: your-networking
spec:
  allocationType:
    type: Elastic/Fixed
    releaseStrategy: TTL
    releaseAfter: "5m0s"
  selector:
    podSelector:
      matchLabels:
        foo: bar
    namespaceSelector:
      matchLabels:
        foo: bar
  vSwitchOptions:
    - vsw-aaa
  securityGroupIDs:
    - sg-aaa
```

- allocationType: 描述 Pod IP 分配的策略
  - type
    - Elastic: 弹性IP 策略。Pod 删除后 IP 资源释放。
    - Fixed: 固定IP 策略。如果配置为固定IP，这个 PodNetworking 仅对有状态pod 生效。
  - releaseStrategy: IP回收策略，在`type` 配置为 `Fixed` 情况有效。
    - TTL: 延迟回收模式，当Pod 被删除一段时间后，释放 IP，最小值为 5m
  - releaseAfter: 延迟回收时间。仅在`releaseStrategy` 为 `TTl` 模式下生效

- selector: 用于配置标签选择器，同时配置 podSelector、namespaceSelector 时，将全部生效
  - podSelector: 用来匹配 pod 的 labels
  - namespaceSelector: 用来匹配 namespace 的 labels
- vSwitchOptions: 用于配置 Pod 使用的 vSwitch。多个vSwitchID 之间为或关系。Pod 仅能使用一个 vSwitch ，terway 将根据配置顺序、vSwitch region 选择一个 vSwitch
- securityGroupIDs: 可配置多个安全组 ID，配置多个安全组时将同时生效。安全组数量小于等于 5个

> 请确保 Pod 可以被唯一的 PodNetworking 配置匹配，避免歧义
>
> 我们强烈建议用户主动配置 vSwitchOptions、securityGroupIDs 字段，如果不配置，则使用 kube-system/eni-config 中的默认值

创建PodNetworking 后，controller 会对 PodNetworking 进行同步，当同步完成 PodNetworking 中  Status 会标记状态 `Ready`

```yaml
❯ kubectl get podnetworkings.network.alibabacloud.com  stateless -oyaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
...
status:
  status: Ready   <---- status
  updateAt: "2021-07-19T10:45:31Z"
  vSwitches:
    - id: vsw-bp1s5grzef87ikb5zz1px
      zone: cn-hangzhou-i
    - id: vsw-bp1sx0zhxd6bw6vpt0hbl
      zone: cn-hangzhou-i
```

### podENI 配置介绍

`podENI` 是 trunk 模式下引入的自定义资源，用于 Terway 记录每个Pod 使用的网络信息
每个 trunk 模式的 Pod 将有一个同名的资源

> **该配置由Terway 维护，请勿修改**

```yaml
❯ kubectl get podenis.network.alibabacloud.com -n default
apiVersion: network.alibabacloud.com/v1beta1
kind: PodENI
...
spec:
  allocation:
    eni:
      id: eni-bp16h6wuzpa9w2vdm5dn     <--- pod 使用的eni id
      mac: 00:16:3e:0d:7b:c2
      zone: cn-hangzhou-i
    ipType:
      releaseAfter: 0s
      type: Elastic                    <--- podIP 分配策略
    ipv4: 192.168.51.99
status:
  instanceID: i-bp1dkga3et5atja91ixt   <--- ecs 实例 ID
  podLastSeen: "2021-07-19T11:23:55Z"
  status: Bind
  trunkENIID: eni-bp16h6wuzpa9utho0t2o
```

### 非固定IP示例

下面定义名为 stateless 的配置
该配置意味着 default namespace 下，包含 `kind: front`、`app: nginx` 标签的 pod 会使用 trunk 模式

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: stateless
spec:
  allocationType:
    type: Elastic
  selector:
    podSelector:
      matchLabels:
        kind: front
        app: nginx
  vSwitchOptions:
    - vsw-bp1s5grzef87ikb5zz1px
    - vsw-bp1sx0zhxd6bw6vpt0hbl
  securityGroupIDs:
    - sg-bp172wuqj4y3f98x7ptm
```

创建 Pod

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-front
  labels:
    app: nginx
spec:
  replicas: 2
  selector:
    matchLabels:
      kind: front
      app: nginx
  template:
    metadata:
      labels:
        kind: front
        app: nginx
    spec:
      containers:
        - name: nginx
          image: nginx
```

### 固定IP示例

eni Trunking 模式支持固定 IP 模式，该模式只适用于有状态应用（StatefulSet）

下面的 PodNetworking 声明了将使用固定IP模式，当 Pod 被删除 5分钟后 PodIP 资源将被回收

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: fixed-ip
spec:
  allocationType:
    type: Fixed              <--- podIP 模式
    releaseStrategy: TTL
    releaseAfter: "5m0s"     <--- pod 删除后回收策略，golang 时间类型
  selector:
    podSelector:
      matchLabels:
        kind: backend
        app: sts-pod
...
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: "sts-fixed-ip"
spec:
  serviceName: "sts-fixed-ip"
  selector:
    matchLabels:
      kind: backend
      app: sts-pod
  replicas: 1
  template:
    metadata:
      labels:
        kind: backend
        app: sts-pod
    spec:
      containers:
        - name: nginx
          image: nginx
```

创建后可以在 CRD 资源里面看到固定IP 分配信息

```yaml
❯ kubectl get podenis.network.alibabacloud.com  sts-fixed-ip-0 -oyaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodENI
metadata:
  generation: 1
  name: sts-fixed-ip-0
  namespace: default
spec:
  allocation:
    eni:
      id: eni-bp161irhf5hrdxdzzodq
      mac: 00:16:3e:10:34:65
      zone: cn-hangzhou-i
    ipType:
      releaseAfter: 5m0s
      releaseStrategy: TTL
      type: Fixed
    ipv4: 192.168.63.206
status:
  instanceID: i-bp18kejuoajwm8jsb0er
  podLastSeen: "2021-07-12T08:21:16Z"
  status: Bind
  trunkENIID: eni-bp1b1plkgpoi8lxj1hcj
```

当 Pod 删除后，PodENI 记录会保留,且状态变为 Unbind

```yaml
❯ kubectl get podenis.network.alibabacloud.com  sts-fixed-ip-0 -oyaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodENI
metadata:
  name: sts-fixed-ip-0
  namespace: default
spec:
  allocation:
    eni:
      id: eni-bp161irhf5hrdxdzzodq
      mac: 00:16:3e:10:34:65
      zone: cn-hangzhou-i
    ipType:
      releaseAfter: 5m0s
      releaseStrategy: TTL
      type: Fixed
    ipv4: 192.168.63.206
status:
  instanceID: i-bp18kejuoajwm8jsb0er
  podLastSeen: "2021-07-12T08:21:16Z"
  status: Unbind
  trunkENIID: eni-bp1b1plkgpoi8lxj1hcj
```

第二次创建 sts pod 时，webhook 会为 pod 添加可用区的约束

```yaml
❯ kubectl get pod sts-fixed-ip-0 -oyaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    k8s.aliyun.com/pod-eni: "true"
  labels:
    app: sts-pod
    controller-revision-hash: sts-fixed-ip-56bf5859d6
    kind: backend
    statefulset.kubernetes.io/pod-name: sts-fixed-ip-0
  name: sts-fixed-ip-0
  namespace: default
spec:
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: topology.kubernetes.io/zone
            operator: In
            values:
            - cn-hangzhou-i
```

pod迁移到其他节点

```yaml
❯ kubectl get podenis.network.alibabacloud.com sts-fixed-ip-0 -oyaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodENI
metadata:
  name: sts-fixed-ip-0
  namespace: default
spec:
  allocation:
    eni:
      id: eni-bp161irhf5hrdxdzzodq
      mac: 00:16:3e:10:34:65
      zone: cn-hangzhou-i
    ipType:
      releaseAfter: 5m0s
      releaseStrategy: TTL
      type: Fixed
    ipv4: 192.168.63.206
status:
  instanceID: i-bp18kejuoajwm8jsb0es
  podLastSeen: "2021-07-12T08:21:16Z"
  status: Bind
  trunkENIID: eni-bp11zk87y8gq0piexey4
```
