## design

## usage

在 eni trunking 模式中，通过自定义资源方式定义容器网络配置、Pod 映射关系
使用前需要创建自定义资源

### podNetworking 配置

`podNetworking` 是 trunk 模式下引入的自定义资源，用来描述一个网络平面的配置信息。
一个网络平面可以配置独立的 vSwitch、安全组等信息。集群内可以配置多个网络平面信息。
`podNetworking` 通过标签选择器来匹配 Pod，被匹配的 Pod 将使用 trunking 模式

配置总览

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: your-networking
spec:
  ipType:
    type: Elastic/Fixed
    releaseStrategy: TTL
    releaseAfter: "5m0s"
  selector:
    podSelector:
      matchLabels:
        foo: bar
    namesapceSelector:
      matchLabels:
        foo: bar
```

- ipType: 描述 Pod IP 分配的策略
    - type
      - Elastic: 弹性IP 策略。Pod 删除后 IP 资源释放。
      - Fixed: 固定IP 策略。如果配置为固定IP，这个 PodNetworking 仅对有状态pod 生效。
    - releaseStrategy: IP回收策略，在`type` 配置为 `Fixed` 情况有效。
      - TTL: 延迟回收模式，当Pod 被删除一段时间后，释放 IP
    - releaseAfter: 延迟回收时间。仅在`releaseStrategy` 为 `TTl` 模式下生效

- selector: 用于配置

> 请确保 Pod 可以被唯一的 PodNetworking 配置匹配

### 非固定IP

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: stateless
spec:
  ipType:
    type: Elastic
  selector:
    podSelector:
      matchLabels:
        kind: front
        app: nginx
```

```sh
❯ kubectl get podnetworkings.network.alibabacloud.com  stateless -oyaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: stateless
spec:
  ipType:
    type: Elastic
  selector:
    podSelector:
      matchLabels:
        app: nginx
        kind: front
status:
  status: Ready
  updateAt: "2021-07-12T06:24:10Z"
```

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
          image: nginx:1.7.9
```

### 固定IP

eni Trunking 模式支持固定 IP 模式，改模式只适用于有状态应用（State

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: fixed-ip
spec:
  ipType:
    type: Fixed
    releaseStrategy: TTL
    releaseAfter: "5m0s"
  selector:
    podSelector:
      matchLabels:
        kind: backend
        app: sts-pod
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: "sts-fixed-ip"
spec:
  serviceName: "sts-fixed-ip"
  selector:
    matchLabels:
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
          image: nginx:1.7.9
```

创建后可以在 CRD 资源里面看到固定IP 分配信息

```sh
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

```shell
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

```shell
❯ kubectl get pod sts-fixed-ip-0 -oyaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    k8s.aliyun.com/pod-eni: "true"
    kubernetes.io/psp: ack.privileged
  creationTimestamp: "2021-07-12T08:30:03Z"
  generateName: sts-fixed-ip-
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

```shell
❯ kubectl get podenis.network.alibabacloud.com sts-fixed-ip-0 -oyaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodENI
metadata:
  creationTimestamp: "2021-07-12T08:21:16Z"
  generation: 1
  name: sts-fixed-ip-0
  namespace: default
  resourceVersion: "82325"
  selfLink: /apis/network.alibabacloud.com/v1beta1/namespaces/default/podenis/sts-fixed-ip-0
  uid: 2a8f1d14-09d3-4c38-831f-9a755d18e709
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

## 指定 vSwitch

默认情况 vSwitch 将使用 kube-system/eni-config ComfigMap 中的配置的 vSwitch
用户也可以主动配置 Trunking Pod 使用的 vSwitch

> 指定 vSwitch 后，将不再使用默认 vSwitch

> 可以指定多个 vSwitch，Terway 将根据配置的顺序使用 vSwitch 

```yaml
apiVersion: network.alibabacloud.com/v1beta1
kind: PodNetworking
metadata:
  name: stateless
spec:
  ipType:
    type: Elastic
  selector:
    podSelector:
      matchLabels:
        kind: front
        app: nginx
  vSwitchIDs:
    - vsw-bp1uc5uhrqzzbl7weix3s
```