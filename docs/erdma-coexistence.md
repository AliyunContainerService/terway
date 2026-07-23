# ERDMA ENI 与 terway 的零配置共存

## 背景

ECS 节点上可能同时存在两类 ENI：

- terway 管理的普通弹性网卡（用于 Pod IP）；
- ERDMA 高性能网卡（`NetworkInterfaceTrafficMode == "HighPerformance"`），通常由 [alibabacloud-erdma-controller](https://github.com/AliyunContainerService/alibabacloud-erdma-controller) 管理（也可能由 ECS 控制台预先绑定到节点）。

历史上需要客户在 `eni-config` 里手工配置 `eni_tag_filter` 白名单，让 terway 只管自己创建的 ENI。客户经常忘记配置，事后补配又无法让 terway 释放已经纳管的 ERDMA ENI，必须重新加入节点。本设计的目标是让两类 ENI **零配置共存**。

## 设计概览

两层保护：

1. **ENI 黑名单 tag 机制**：命中特定 tag 的 ENI **保留在 CR 里、标注 `Unschedulable`,但 terway 只展示不管理**(不分配、不回收)。硬编码默认黑名单开箱即用；预留 ConfigMap 字段供扩展。
2. **HighPerformance ENI 永远不给非 RDMA Pod 用**：兜底——即使 race 窗口内某张 HP ENI 已经被 terway 看见，也不会被用于普通 Pod 的 IP 分配。

erdma-controller 配合落地：每张被它管理的 ERDMA ENI 都打 `creator=alibabacloud-erdma-controller` + `terway.alibabacloud.com/excluded=true` 两个 tag。

> **适用范围：本特性仅作用于 multi-ip controller（集中式 IPAM）模式。** 非中心化的
> daemon/legacy 模式（`pkg/factory/aliyun`、`daemon/builder.go`）**刻意保持原样、行为
> 不变**——它在 factory 层拿不到"已纳管"信号,任何按 tag 的过滤都会在升级/重启时把存量
> 卡连同其 Pod IP 一起丢掉,得不偿失。daemon 模式的 ERDMA 卡请继续用 `eni_tag_filter`
> 白名单规避,或从 ECS 控制台解绑。

## 改动一：ENI 黑名单 tag 机制

### 1. 默认硬编码黑名单

`pkg/aliyun/client/eni_filter.go` 中定义：

```go
var DefaultENITagBlockList = []ENITagBlockListItem{
    {Key: "creator", Value: "alibabacloud-erdma-controller"},
    {Key: "terway.alibabacloud.com/excluded", Value: "true"},
}
```

任何 ENI 上携带这两条 (key, value) 中任一个，terway 都会把它标为 `Unschedulable`——保留在 CR 供查看，但不分配 IP、不回收其 IP。新装的 terway 不需要用户任何配置就能识别 erdma-controller 管理的 ENI。

### 2. ConfigMap 可补充

`eni-config` 新增 `eni_tag_block_list` 字段，与默认黑名单合并：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config
data:
  eni_conf: |
    {
      ...
      "eni_tag_block_list": [
        {"key": "managed-by", "value": "custom-rdma-operator"}
      ]
    }
```

合并语义：默认黑名单 + 用户自定义 = 完整黑名单，去重。空 key 项被忽略。逻辑实现在 `types/daemon/config.go` 的 `MergeENITagBlockList`。

> 配置链路：daemon 侧 `pkg/eni/node_reconcile.go` 把合并后的列表写入 Node CR 的 `Spec.ENISpec.TagBlockList`（这是 daemon 唯一参与的部分——仅传递配置，不做 ENI 过滤）；controller 的 `syncWithAPI` 读取该字段，Node CR 未携带时回退到 `DefaultENITagBlockList`，保证 erdma-controller 契约始终生效。

### 3. 标注模型：保留在 CR、打 `Unschedulable`、分配时跳过

**核心:命中黑名单的 ENI 不从 CR 里丢掉,而是保留在 `node.Status.NetworkInterfaces`(连其 IP 一起导入,方便运维直接 `kubectl get -o yaml` 看到),打上 `Nic.Unschedulable = true`。terway 对它「只展示、不管理」:不分配新 IP、不扩 prefix、不回收/删除其 IP。** 这样效果与「丢弃」一致(普通 Pod 永不落到它),但更直观,且容量天然正确——它在 `node.Status` 里占位,`getEniOptions` 的 `total - len(sorted)` 自动把这张卡占的槽位算进去,无需任何额外计数字段。

打标点(`pkg/controller/multi-ip/node/pool.go` `syncWithAPI`):`DescribeNetworkInterfaceV2` 返回后,先滤掉 primary/LENI primary,其余 ENI **全部照常同步进 `node.Status`**;随后一个 marking loop 对每张 ENI 计算 `Unschedulable`,命中以下任一即标记:

- **命中黑名单**:`IsENIBlocked(item.Tags, blockList)`(`blockList` 优先取 Node CR 的 `Spec.ENISpec.TagBlockList`,缺省回退 `DefaultENITagBlockList`);
- **未被 terway 管理的 HighPerformance(ERDMA)卡**:traffic mode 为 HighPerformance **且** `Spec.ENISpec.EnableERDMA == false`。这样即使一张 HP 卡没打 tag(如误开 HP、或 erdma-controller 打 tag 前的竞态窗口),也能获得与 tag-blocked 卡**一致的全路径保护**(不分配、不回收);当 `EnableERDMA == true` 时 HP 卡是 terway 自己的 RDMA 卡,不标记,RDMA Pod 照常落到它上面。

每次同步重算,所以撤掉规则 / 关掉 HP 即自动恢复可调度。

daemon/legacy 模式不做黑名单相关处理(见开头「适用范围」)。

### 4. `Unschedulable` 在所有管理路径上被跳过

因为选择「连 IP 一起导入」,必须确保 terway 对 blocked ENI **既不分配、也不回收**,否则会误动 erdma-controller 的 IP。跳过点:

| 路径 | 位置 | 行为 |
|------|------|------|
| 分配(非 prefix) | `assignIPFromLocalPool` | 跳过 `Unschedulable`(及 HighPerformance)ENI 的空闲 IP(v4/v6) |
| 配额/创建 | `validateENI` | `eniRef.Unschedulable` 直接返回 false,`addIP` 不给它加新 IP |
| **回收(命门)** | `releaseUnUsedIP`(`eni.go`) | 函数首行 `if eni.Unschedulable { return 0 }`——**绝不释放其 IP、绝不删这张卡** |
| **GC 执行队列** | `handleStatus` | 首行 `if eni.Unschedulable { continue }`——**绝不 Detach/Delete ENI、绝不 UnAssign IP/Prefix**。配合 marking 时 `clearPendingDelete` 清掉先前排队的 `Deleting` 标记,防止升级前/上次 reconcile 留下的删除动作在本次执行 |
| 水位计算 | `countTotalIdleIPs` / `calculateToDel` | 两者都跳过 `Unschedulable`——其空闲 IP 不计入水位,否则 MinPoolSize 会误判已满足、或删除额度落到正常 ENI 上 |
| 节点状态 | `updateNodeCondition` | 跳过 `Unschedulable` option——避免"只有 blocked ENI 有空闲 IP"时把节点误报为 `SufficientIP=True` |
| prefix 模式 | `assignEniPrefixWithOptions` / `syncPrefixAllocation` | 容量统计与 prefix 追加都跳过 `Unschedulable`(既不计其容量,也不给它扩 prefix) |
| 非中心化 daemon(CRD prefix) | `LocalDelegate.tryAllocateLocal` + `syncNodeCR` 的 `SetUnschedulable` | 新 Pod 选卡跳过 `Unschedulable` IPAM;存量 Pod 经 `RestorePod` 保留 |
| 删除 | `syncWithAPI` del-loop | blocked ENI 是 attached → 在 `eniIDMap` 里 → 永不被 terway 删除 |

存量 Pod 的 IP 与数据面完全不动,blocked ENI 随存量 Pod 退出**自然排空**。

> 无需重建节点:blocked / 未管理的 HP 卡始终以 `Unschedulable` 形态留在 CR,terway 从不占用也从不回收它,它一直干净地留给 erdma-controller。撤掉规则(或该卡不再是 HP)后,下次同步即自动恢复可调度。
>
> 未覆盖的风险:`enable_erdma=true`(terway 自己也管 erdma)**且** ENI 未打 tag 时,`rdmaKey` 扩容会与 erdma-controller 争抢这张卡。属于「两个 manager 同时管 erdma」的误配置——典型部署下 terway 不开 `enable_erdma`,且一旦 ENI 被打 tag 即被标 `Unschedulable`。

## 改动二：HighPerformance ENI 永远不给非 RDMA Pod 用

历史代码 `assignIPFromLocalPool`：

```go
// 旧逻辑：只有 enableEDRMA=true 时才跳过 HP
if !info.RequireERDMA &&
    enableEDRMA &&
    v.NetworkInterface.NetworkInterfaceTrafficMode == HighPerformance {
    continue
}
```

`enableEDRMA` 是 terway daemon 启动参数 `enable_erdma`，默认 false。客户用 erdma-controller 的典型部署里 **terway 不开 enable_erdma**——它根本不应该管 ERDMA 卡。这意味着 HP ENI 一旦被 terway 看到（race 窗口、客户没装 erdma-controller、老版本 erdma-controller 没补 tag），就会被普通 Pod 占用。

新逻辑：

```go
// 无条件跳过 HP（对非 RDMA Pod）。HP IP 被用满了，addIP() 会建新的 standard ENI，
// 不会回退到 HP，保持 HP 卡的 IP 干净。
if !info.RequireERDMA &&
    v.NetworkInterface.NetworkInterfaceTrafficMode == HighPerformance {
    continue
}
```

修改位置：`pkg/controller/multi-ip/node/pool.go` `assignIPFromLocalPool` 的 IPv4 和 IPv6 分支各一处。RDMA Pod 走 HP ENI 的现有逻辑（`info.RequireERDMA && ... != HighPerformance { continue }`）完全保留。删除了原来多余的 `enableEDRMA` 判断参数（现在无条件 skip，不再依赖该开关）。

> daemon/legacy 模式（`daemon/builder.go`）**不改动**：其 HP ENI 归属逻辑保持原样(见开头「适用范围」)。本兜底仅在 controller 模式生效。

### `Unschedulable` ENI 的全链路排空(prefix 模式补充）

被标记 `Unschedulable` 的 ENI(命中黑名单、或未被 terway 管理的 HP 卡;完整跳过路径见「改动一·第 4 节」)要在**所有分配路径**上停止接收新 Pod,同时不动存量 Pod。这里补充说明两种 IPAM 模式下的落点:

- **非 Prefix 模式**：
  - `assignIPFromLocalPool` 跳过 `Unschedulable` ENI 的空闲 IP(v4/v6);
  - `validateENI` 对 `eniRef.Unschedulable` 直接返回 `false`,使 `addIP → assignEniWithOptions` **不再向这张卡追加新 IP**——否则会出现「Pod 一直 Pending、blocked 卡却被不断填充、反复扩容/回收抖动」。
- **Prefix 模式**（`syncPods` 在此模式提前返回、不走 `assignIPFromLocalPool`；真正的 Pod↔IP 分配在 daemon 的 `LocalDelegate`）：
  - `pkg/eni/eni_local_ipam.go` 的 `ENILocalIPAM` 记录 `unschedulable`(来自 `Nic.Unschedulable`);
  - `LocalDelegate.tryAllocateLocal` 为新 Pod 选卡时跳过 `IsUnschedulable()` 的 IPAM;存量 Pod 在 `doInit` 经 `RestorePod` 恢复,不受影响;
  - `syncNodeCR` 在 CR 更新时对已存在的 IPAM 调 `SetUnschedulable`,使「先纳管、后被 block-list」的卡也能及时停止分配。

这样 `Unschedulable` 卡在两种模式下都**自然排空**,不会 orphan 存量 Pod。

### 为什么默认"直接 skip"而不是"降级 fallback"

考虑过"HP 排到候选末尾、实在没别的才用"的 fallback 写法。默认行为最终选 skip：

- map 顺序无意义，要做"排到末尾"必须先收集再排序，diff 大；
- terway 池满会自动 `addIP()` 新建 standard ENI，**根本不会**走到"实在没普通 ENI 才用 HP" 的 fallback；
- skip 让代码意图清晰：HP 卡是 RDMA 流量专属，不会跟普通 Pod 抢资源。

行为等价、实现更简、读起来更直接,默认选 skip。

> **误开 HP 又没装 erdma-controller 怎么办**：这张 HP 卡会被 terway 一直 skip、其 IP 不可用。terway 不提供「收回」开关——想让 terway 用上这部分容量,直接从 **ECS 控制台把这张 ENI 从节点解绑**即可(它本就没有属主)。不做自动纳管是为了避免与 erdma-controller 的竞态:节点刚加入时无法可靠区分「未打 tag 的 HP 卡」是「无主可收回」还是「controller 即将接管」,自动纳管会导致 controller 接管后 Pod IP 悬空。

## 测试

新增/更新:

- `pkg/aliyun/client/eni_filter_test.go`:`IsENIBlocked` 各种匹配/不匹配场景、空 list 行为。
- `types/daemon/config_test.go` `TestMergeENITagBlockList_*`:默认始终生效;用户列表去重;空 key 丢弃。
- `pkg/controller/multi-ip/node/pool_test.go`:
  - `Test_assignIPFromLocalPool`:非 RDMA Pod 跳过 HighPerformance ENI(即使 `enable_erdma=false`)、跳过被标 `Unschedulable` 的 ENI(v4+v6)、RDMA Pod 落到 HP ENI;
  - `validateENI`:`Unschedulable` ENI 直接不通过,不给它追加新 IP;
  - `Test_releaseUnUsedIP_SkipsUnschedulable`:`Unschedulable` ENI 的空闲 IP **不被释放、整卡不被删**;
  - `Test_clearPendingDelete`:标 `Unschedulable` 时把 ENI/IP/Prefix 的 `Deleting` 标记重置回存活态;
  - `Test_countTotalIdleIPs_SkipsUnschedulable` 与 `Test_calculateToDel`(新用例):`Unschedulable` ENI 的空闲 IP 不计入水位/删除额度;
  - `updateNodeCondition`(新用例):只有 `Unschedulable` ENI 有空闲 IP 时节点报 `InsufficientIP`;
  - `syncWithAPI`:命中黑名单的 ENI(新的与已纳管的)都**保留在 CR 且标 `Unschedulable`**,untagged HP 卡在 `EnableERDMA=false` 时也被标,primary 始终被排除。
- `pkg/eni/local_delegate_test.go` `TestLocalDelegate_Allocate_SkipsUnschedulable`:Prefix 模式下新 Pod 跳过 `Unschedulable` ENI,落到可调度 ENI。

## 兼容性

- **旧版 terway + 新版 erdma-controller**：旧版 terway 不识别 tag，HP ENI 仍会被纳管——退化到现状，**不比旧版差**。客户仍可手动配 `eni_tag_filter`。
- **新版 terway + 旧版 erdma-controller**：旧版 erdma-controller 不打 `terway.alibabacloud.com/excluded` tag，但通常会打 `creator=alibabacloud-erdma-controller`——黑名单第一条仍能命中。ECS 控制台预绑定的 ENI（连 creator tag 都没有）则依赖"改动二"兜底（controller 模式在 `assignIPFromLocalPool` skip HP ENI）。
- **新版 terway + 新版 erdma-controller**：controller 模式完全零配置共存。
- **误开 HP + 无 erdma-controller**（尤其小机型）：controller 模式下这张卡被 skip、其 IP 不可用;想收回容量,直接从 **ECS 控制台把该 ENI 从节点解绑**。
- **存量已被老版 terway 纳管的 ERI（升级场景）**：controller 模式下升级后被标 `Unschedulable`——**存量 Pod 不受影响**、继续可用,新 Pod 不再落到它上面,随存量 Pod 退出自然排空(见第 4 节),无需重建节点。
- **daemon/legacy 模式**：行为与本特性发布前完全一致,不受影响;ERDMA 卡仍靠 `eni_tag_filter` 白名单或 ECS 控制台解绑规避。

## 与 erdma-controller 侧的协议

| erdma-controller 责任 | terway 责任 |
|----------------------|------------|
| 创建/接管 ERDMA ENI 时打 `creator=alibabacloud-erdma-controller` | 默认黑名单识别此 (key, value) |
| 同时打 `terway.alibabacloud.com/excluded=true` | 默认黑名单识别此 (key, value) |
| ENI 上有 `NetworkInterfaceTrafficMode=HighPerformance` | controller 模式 `assignIPFromLocalPool` 默认 skip，不给非 RDMA Pod 用 |

两侧任一改动单独发版都不引入回归；同时升级即可零配置共存。
