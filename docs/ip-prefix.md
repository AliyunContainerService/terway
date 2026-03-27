# IP Prefix 功能

## 概述

IP Prefix 功能是 Terway 提供的一种基于前缀的 IPAM（IP 地址管理）模式。与传统模式分配独立 IP 地址不同，Prefix 模式在
ENI（弹性网卡）上分配 CIDR 格式的前缀（如 `/28`），每个前缀包含一段连续的 IP 地址。

### 与传统模式的区别

| 特性    | 传统 IP 模式                    | IP Prefix 模式                  |
|-------|-----------------------------|-------------------------------|
| 分配单位  | 单个 IP 地址（如 `10.244.17.119`） | CIDR 前缀（如 `10.244.126.48/28`） |
| IP 数量 | 每个 ENI 固定数量                 | 每个前缀包含 16 个 IP（/28）           |
| 适用场景  | 小规模集群、IP 需求分散               | 大规模集群、IP 需求密集                 |
| 管理模式  | Controller 管理 Pod↔IP 绑定     | Controller 仅分配前缀，Daemon 管理绑定  |

### 适用场景

- **大规模集群**：需要为大量 Pod 分配 IP 地址
- **高密度部署**：每个节点运行大量 Pod
- **快速启动**：减少 API 调用次数，提高 Pod 启动速度
- **IPv6 双栈**：同时需要 IPv4 和 IPv6 地址的场景

## 工作原理

### 前缀分配机制

1. **ENI 创建时分配**：创建新 ENI 时，根据 `ipv4_prefix_count` 配置分配 IPv4 前缀；双栈模式下同时自动分配 1 个 IPv6 前缀
2. **已有 ENI 补充**：在现有 `InUse` 状态的 ENI 上补充前缀，直到达到目标数量
3. **跨 ENI 分布**：前缀会均匀分布在多个 ENI 上，提高容错性

### API 限制

- **单次调用限制**：每次 API 调用最多分配 10 个前缀（`maxPrefixPerAPICall = 10`）
- **容量计算**：每个 ENI 的最大前缀数 = `IPv4PerAdapter - 1`（预留 1 个给主 IP）

### IPv6 前缀分配策略

| 网络模式        | IPv4 前缀                  | IPv6 前缀                            |
|-------------|--------------------------|------------------------------------|
| **IPv4 单栈** | 由 `ipv4_prefix_count` 控制 | 不分配                                |
| **IPv6 单栈** | 不分配                      | 由 `ipv6_prefix_count` 控制（取值 0 或 1） |
| **双栈**      | 由 `ipv4_prefix_count` 控制 | **自动**：每个 ENI 恰好 1 个，无需配置          |

在双栈模式下，Controller 会自动确保每个拥有 IPv4 前缀的 ENI 恰好有 1 个 IPv6 前缀。如果 ENI 已有 IPv6 前缀则跳过，不会重复分配。

### 与 ECS ENI 的集成

IP Prefix 功能使用阿里云 ECS 的以下 API：

- `AssignPrivateIpAddresses`：分配 IPv4 前缀（`Ipv4PrefixCount` 参数）
- `AssignIpv6Addresses`：分配 IPv6 前缀（`Ipv6PrefixCount` 参数）
- `CreateNetworkInterface`：创建 ENI 时分配前缀

## 配置方法

### 前提条件

1. 实例规格必须支持 Prefix 模式（非 LingJun/EFLO 节点）
2. 为目标节点池创建独立的动态配置 ConfigMap（如 `eni-config-prefix`），并在其 `eni_conf` 中设置 `enable_ip_prefix: true`
3. 为目标节点打上 `terway-config: <config_name>` 标签，使 Terway Daemon 加载对应的动态配置

### 开启 Prefix 模式

推荐通过**动态配置（Dynamic Config）**为特定节点池单独启用 Prefix 模式，而不是修改全局的 `kube-system/eni-config`
，以避免影响集群中所有节点。

#### 步骤一：创建动态配置 ConfigMap

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config-prefix
  namespace: kube-system
data:
  eni_conf: |
    {
      "enable_ip_prefix": true,
      "ipv4_prefix_count": 5
    }
EOF
```

#### 步骤二：为目标节点打上标签

```bash
kubectl label node <node-name> terway-config=eni-config-prefix
```

Terway Daemon 启动时会读取节点的 `terway-config` 标签，加载对应名称的 ConfigMap，并以 MergePatch 方式合并到默认配置上。

> **注意**：`enable_ip_prefix` 配置仅在 Node CR 首次创建时生效，对已有节点无效。修改动态配置后需重启 Daemon
> 才能生效。详见下方[不可变性说明](#不可变性说明)。

### ConfigMap 配置

为目标节点池创建独立的动态配置 ConfigMap，在 `eni_conf` 字段中同时设置 `enable_ip_prefix` 和 `ipv4_prefix_count`：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config-prefix
  namespace: kube-system
data:
  eni_conf: |
    {
      "enable_ip_prefix": true,
      "ipv4_prefix_count": 5
    }
```

然后为目标节点打上标签，使其加载该动态配置：

```bash
kubectl label node <node-name> terway-config=eni-config-prefix
```

### 配置参数说明

| 参数                  | 类型   | 默认值   | 说明                                                                                |
|---------------------|------|-------|-----------------------------------------------------------------------------------|
| `enable_ip_prefix`  | bool | false | 是否启用 IP Prefix 模式，仅在 Node CR 首次创建时生效                                              |
| `ipv4_prefix_count` | int  | 0     | 节点 IPv4 前缀总数。在 IPv4 单栈和双栈模式下有效                                                    |
| `ipv6_prefix_count` | int  | 0     | 节点 IPv6 前缀总数。**仅在 IPv6 单栈模式下有效**，取值范围 0 或 1。双栈模式下忽略此字段（IPv6 前缀由系统自动管理，每个 ENI 1 个） |

### 双栈配置示例

双栈模式下只需配置 `ipv4_prefix_count`，Controller 会为每个 ENI 自动分配 1 个 IPv6 前缀：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config-prefix-dual
  namespace: kube-system
data:
  eni_conf: |
    {
      "enable_ip_prefix": true,
      "ipv4_prefix_count": 5
    }
```

然后为目标节点打上标签：

```bash
kubectl label node <node-name> terway-config=eni-config-prefix-dual
```

> **注意**：双栈模式下无需配置 `ipv6_prefix_count`。系统会自动为每个拥有 IPv4 前缀的 ENI 分配恰好 1 个 IPv6 前缀。

### IPv6 单栈配置示例

IPv6 单栈模式下使用 `ipv6_prefix_count`（取值 0 或 1）：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config-prefix-v6only
  namespace: kube-system
data:
  eni_conf: |
    {
      "enable_ip_prefix": true,
      "ipv6_prefix_count": 1
    }
```

然后为目标节点打上标签：

```bash
kubectl label node <node-name> terway-config=eni-config-prefix-v6only
```

> **注意**：`ipv6_prefix_count` 仅在 IPv6 单栈模式下生效，取值只能为 0 或 1。

## 约束与限制

### 实例规格要求

1. **容量限制**：每个 ENI 的最大前缀数 = `IPv4PerAdapter - 1`
    - 例如：某规格 `IPv4PerAdapter=20`，则每个 ENI 最多 19 个前缀
    - 19 个前缀 × 16 IP/前缀 = 304 个 IP

2. **不支持 LingJun (EFLO) 节点**：Prefix 模式明确排除 EFLO 节点

### 互斥性

- **与独立 IP 模式互斥**：启用 Prefix 模式后，不再分配独立 IP 地址
- **互斥逻辑**：当 `addIPv4PrefixN > 0` 时，`addIPv4N = 0`

### API 调用限制

- **单次调用**：最多 10 个前缀
- **多次调用**：如需分配更多，会自动分多次 API 调用完成

### 其他限制

- **预热机制**：Prefix 模式跳过常规 IPAM 流程（pool 管理、预热等）

## 不可变性说明

`enable_ip_prefix` 配置具有不可变性约束：

- **仅首次生效**：`enable_ip_prefix` 仅在 Node CR 首次创建时从动态配置 ConfigMap 读取并写入 `ENISpec`，后续对动态配置
  ConfigMap 的修改不会影响已有节点
- **变更被忽略**：若动态配置 ConfigMap 中的 `enable_ip_prefix` 与已有 Node CR 的 `ENISpec.EnableIPPrefix` 不一致，系统会产生
  Warning 事件，但不会修改节点配置
- **如需变更**：若需要对已有节点启用或禁用 Prefix 模式，必须将节点从集群中删除并重新加入，新节点加入时会读取最新的
  动态配置 ConfigMap 配置

> **注意**：修改动态配置 ConfigMap 后，仅对新加入集群的节点生效，已有节点不受影响。

## 状态说明

### IPPrefix 状态

| 状态         | 说明          |
|------------|-------------|
| `Valid`    | 前缀正常可用      |
| `Frozen`   | 前缀被冻结（暂不使用） |
| `Invalid`  | 前缀无效        |
| `Deleting` | 前缀正在删除中     |

### 查看前缀分配情况

查看 Node CR 状态：

```bash
kubectl get node <node-name> -n kube-system -o yaml
```

在 `.status.networkInterfaces` 中查看前缀分配：

```yaml
status:
  networkInterfaces:
    eni-xxx:
      id: eni-xxx
      status: InUse
      ipv4Prefix:
      - prefix: 10.244.126.48/28
        status: Valid
      - prefix: 10.244.126.64/28
        status: Valid
      # 双栈模式下，每个 ENI 自动分配 1 个 IPv6 前缀
      ipv6Prefix:
      - prefix: 2408:4005:3ab:100::/80
        status: Valid
```

### 查看前缀统计

```bash
# 查看节点的 IP Prefix 数量
kubectl get nodes.network.alibabacloud.com <node-name> -o jsonpath='{.spec.eni.ipPrefixCount}'

# 查看所有前缀
kubectl get nodes.network.alibabacloud.com <node-name> -o jsonpath='{.status.networkInterfaces[*].ipv4Prefix}'
```

## 最佳实践

### 前缀数量规划

1. **计算所需前缀数**：

   ```text
   所需前缀数 = ceil(预计 Pod 数 / 16)
   ```

   每个 /28 前缀包含 16 个 IP 地址

2. **预留余量**：建议预留 20% 余量以应对 Pod 增长

3. **多 ENI 分布**：前缀会自动分布在多个 ENI 上，建议每个 ENI 保留一定容量

### 配置示例

假设节点预计运行 200 个 Pod：

```yaml
# 计算：200 / 16 = 12.5，向上取整为 13，预留 20% 余量 ≈ 16
ipv4_prefix_count: 16
```

### 监控和告警

建议监控以下指标：

1. **前缀使用率**：

   ```text
   已用前缀数 / 总前缀数
   ```

2. **ENI 容量**：

   ```text
   已分配前缀数 / (IPv4PerAdapter - 1)
   ```

3. **API 调用频率**：Prefix 分配涉及 ECS API 调用，需监控限流

## 故障排查

### 常见问题

#### 1. 前缀未分配

**现象**：节点上没有分配前缀

**排查步骤**：

```bash
# 1. 检查节点是否打了 terway-config 标签
kubectl get node <node-name> --show-labels | grep terway-config

# 2. 获取节点对应的动态配置 ConfigMap 名称
CONFIG_NAME=$(kubectl get node <node-name> -o jsonpath='{.metadata.labels.terway-config}')
echo "动态配置 ConfigMap: ${CONFIG_NAME}"

# 3. 检查动态配置 ConfigMap 中 enable_ip_prefix 是否为 true
kubectl get cm -n kube-system ${CONFIG_NAME} -o jsonpath='{.data.eni_conf}' | python3 -m json.tool | grep enable_ip_prefix

# 4. 检查动态配置 ConfigMap 中 ipv4_prefix_count 是否大于 0
kubectl get cm -n kube-system ${CONFIG_NAME} -o jsonpath='{.data.eni_conf}' | python3 -m json.tool | grep ipv4_prefix_count

# 5. 检查 Node CR 中 ENISpec 的 enableIPPrefix 字段（仅首次创建时写入）
kubectl get nodes.network.alibabacloud.com <node-name> -o jsonpath='{.spec.eniSpec.enableIPPrefix}'

# 6. 查看 Node CR 事件（若动态配置 ConfigMap 与 Node CR 不一致，会有 Warning 事件）
kubectl describe nodes.network.alibabacloud.com <node-name> -n kube-system
```

**可能原因**：

- 节点未打 `terway-config` 标签，导致 Daemon 未加载动态配置
- 动态配置 ConfigMap 不存在或名称与标签值不匹配
- 动态配置 ConfigMap 的 `eni_conf` 中 `enable_ip_prefix` 未设置为 `true`（注意：此配置仅对新加入节点生效）
- `ipv4_prefix_count` 设置为 0
- 节点是 EFLO 节点（不支持 Prefix 模式）
- 节点在 `enable_ip_prefix: true` 写入 ConfigMap 之前已加入集群（已有节点不受 ConfigMap 变更影响）

#### 2. 前缀分配不足

**现象**：分配的前缀数量少于配置

**可能原因**：

- ENI 容量不足（`IPv4PerAdapter - 1` 限制）
- API 调用失败（查看 controller 日志）

**解决方法**：

- 增加 ENI 数量（调整 `min_eni` / `max_eni`）
- 检查阿里云配额

#### 3. Pod 无法获取 IP

**现象**：Pod 处于 ContainerCreating 状态，事件显示 IP 分配失败

**排查步骤**：

```bash
# 查看 Pod 事件
kubectl describe pod <pod-name>

# 查看 Terway Daemon 日志
kubectl logs -n kube-system -l app=terway --container=terway

# 查看 Node CR 前缀状态
kubectl get nodes.network.alibabacloud.com <node-name> -o yaml
```

**注意**：Controller 只确保前缀存在，Pod↔IP 绑定由 Daemon 管理。如果前缀已分配但 Pod 无法获取 IP，请检查 Daemon 日志。

### 日志排查

查看 controller 日志：

```bash
# 查看 Prefix 相关日志
kubectl logs -n kube-system -l app=terway-controlplane | grep -i prefix
```

关键日志关键字：

- `syncPrefixAllocation`：前缀分配同步
- `assignIPv4Prefix`：IPv4 前缀分配
- `assignIPv6Prefix`：IPv6 前缀分配

### 恢复步骤

如果 Prefix 状态异常，可以尝试：

1. **重启 Terway Pod**：

   ```bash
   kubectl rollout restart deployment terway-controlplane -n kube-system
   ```

2. **对已有节点变更 Prefix 模式**：

   由于 `enable_ip_prefix` 具有不可变性，若需要对已有节点启用或禁用 Prefix 模式，需重建节点：

   ```bash
   # 1. 先更新动态配置 ConfigMap 中的 enable_ip_prefix 配置
   kubectl edit cm -n kube-system <动态配置 ConfigMap 名称>

   # 2. 驱逐节点上的 Pod
   kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data

   # 3. 从集群中删除节点
   kubectl delete node <node-name>

   # 4. 在阿里云控制台将该 ECS 实例重新加入节点池，新节点加入时将读取最新动态配置 ConfigMap 配置
   ```

3. **检查阿里云控制台**：
    - 登录阿里云控制台
    - 查看 ENI 的前缀分配情况
    - 确认 API 调用是否成功
