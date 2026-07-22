# ERDMA ENI 与 terway 的零配置共存

## 背景

ECS 节点上可能同时存在两类 ENI：

- terway 管理的普通弹性网卡（用于 Pod IP）；
- ERDMA 高性能网卡（`NetworkInterfaceTrafficMode == "HighPerformance"`），通常由 [alibabacloud-erdma-controller](https://github.com/AliyunContainerService/alibabacloud-erdma-controller) 管理（也可能由 ECS 控制台预先绑定到节点）。

历史上需要客户在 `eni-config` 里手工配置 `eni_tag_filter` 白名单，让 terway 只管自己创建的 ENI。客户经常忘记配置，事后补配又无法让 terway 释放已经纳管的 ERDMA ENI，必须重新加入节点。本设计的目标是让两类 ENI **零配置共存**。

## 设计概览

两层保护：

1. **ENI 黑名单 tag 机制**：终态——带特定 tag 的 ENI 完全不被 terway 纳管。硬编码默认黑名单开箱即用；预留 ConfigMap 字段供未来扩展。
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

任何 ENI 上携带这两条 (key, value) 中任一个，terway 都会跳过——既不纳入管理池，也不分配 IP。新装的 terway 不需要用户任何配置就能识别 erdma-controller 管理的 ENI。

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

### 3. 过滤生效点

| 入口 | 修改文件 | 行为 |
|------|---------|------|
| multi-ip controller 同步 | `pkg/controller/multi-ip/node/pool.go` `syncWithAPI` | `DescribeNetworkInterfaceV2` 返回后调用 `filterBlockedForAdoption(enis, blockList, node.Status.NetworkInterfaces)`：**只过滤还没被纳管的新 ENI，已在 `node.Status.NetworkInterfaces` 里的一律保留**（见第 4 节)。`blockList` 优先取 Node CR 的 `Spec.ENISpec.TagBlockList`，缺省回退 `DefaultENITagBlockList` |

daemon/legacy 模式不做黑名单过滤（见开头「适用范围」）。

被过滤的**新** ENI 不会进入 `node.Status.NetworkInterfaces`，因此后续的 IP 分配、回收、ENI 删除等流程都不会触及它们。

### 4. 纳管是单向的：黑名单只在"采纳"那一刻生效

**核心原则：一旦一张 ENI 被 terway 纳管，就一直管到底，黑名单不会再驱逐它。** 因为纳管后 terway 已经给这张卡配了路由和 IP rule（数据面),把它从 CR 里删掉既不清理这些状态、也不能让它干净地还给 erdma-controller 用。所以黑名单只是一道**采纳门**：它决定「要不要接管一张还没被管的 ENI」，绝不碰已经在 `node.Status.NetworkInterfaces` 里的 ENI。

`filterBlockedForAdoption` 的语义：

- ENI 已在 `node.Status.NetworkInterfaces` → **一律保留**，无论有没有 tag；
- ENI 不在 status（新出现）→ 命中黑名单则不采纳。

由此,`syncWithAPI` 里那段"把不在 instance 上的 ENI `delete` 出 CR"的逻辑不会再被黑名单触发（已管的卡不会被过滤掉，就不会进入删除分支）。**这也顺带解决了「存量已接管的 ERI 升级后被不 graceful 丢弃」的问题**——它不再被丢弃。

#### 竞态与自然排空

terway 与 erdma-controller 完全异步,对预绑定 ERI 二者同一时刻看到这张卡,terway 可能在 erdma-controller 打 tag 之前就把这张未打 tag 的 HP ENI 采纳进 status。之后无论 erdma-controller 是否补 tag：

- 存量已纳管的 HP/tagged ENI **不再被驱逐**,存量 Pod 走 take-over 继续可用;
- **改动二** 保证新 Pod 永不落到 HP ENI（`assignIPFromLocalPool` skip）；
- **addIP 也不会给它扩 IP**：HP ENI 归 `rdmaKey`,普通 Pod 扩容只挑 `secondaryKey`/`trunkKey`,而 RDMA 扩容需求 `len(rdmaPods)` 在 `enable_erdma=false` 时恒为 0。

于是这张卡**自然排空**：存量 Pod 逐个退出后其上 IP 空闲、不再被复用（HP 卡不进新 Pod）,也不会被 `releaseUnUsedIP` 删除（只删 Standard）。**无需额外的 drain/活跃 IP 检查代码。**

#### 想彻底甩掉一张已纳管的问题 ENI → 重建节点

重建节点会得到一个全新的 Node CR（`node.Status` 为空），那张 ERI 就变成"新的" → 被采纳门（黑名单）挡在门外,从头就不接管,干净地留给 erdma-controller。这是官方 remediation。

> 未覆盖的风险：`enable_erdma=true`（terway 自己也管 erdma）**且** ENI 未打 tag 时,`rdmaKey` 扩容会与 erdma-controller 争抢这张卡。属于「两个 manager 同时管 erdma」的误配置——典型部署下 terway 不开 `enable_erdma`,且一旦 ENI 被打 tag,新卡即被采纳门挡住。

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

### 已纳管黑名单 ENI(`Unschedulable`)的全链路排空

被标记 `Unschedulable` 的已纳管 ENI(见「改动一·第 4 节」)要在**所有分配路径**上停止接收新 Pod,同时不动存量 Pod:

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
  - `Test_assignIPFromLocalPool`:非 RDMA Pod 跳过 HighPerformance ENI(即使 `enable_erdma=false`)、以及跳过已纳管但命中黑名单被标记 `Unschedulable` 的 ENI(v4+v6);
  - `Test_filterBlockedForAdoption`:新的 blocked ENI 被过滤、**已纳管的 blocked ENI 被保留**、混合场景、nil 元素、空 blockList 等分支;
  - `Test_getEniOptions`:`ExcludedENICount` 扣减新建配额;
  - `validateENI`:`Unschedulable` ENI 直接不通过,不再追加新 IP;
  - `syncWithAPI`:已纳管黑名单 ENI 被标记 `Unschedulable`、新黑名单 ENI 计入 `ExcludedENICount`、命中 block-list 的 primary 不被重复计数。
- `pkg/eni/local_delegate_test.go` `TestLocalDelegate_Allocate_SkipsUnschedulable`:Prefix 模式下新 Pod 跳过 `Unschedulable` ENI,落到可调度 ENI。

## 兼容性

- **旧版 terway + 新版 erdma-controller**：旧版 terway 不识别 tag，HP ENI 仍会被纳管——退化到现状，**不比旧版差**。客户仍可手动配 `eni_tag_filter`。
- **新版 terway + 旧版 erdma-controller**：旧版 erdma-controller 不打 `terway.alibabacloud.com/excluded` tag，但通常会打 `creator=alibabacloud-erdma-controller`——黑名单第一条仍能命中。ECS 控制台预绑定的 ENI（连 creator tag 都没有）则依赖"改动二"兜底（controller 模式在 `assignIPFromLocalPool` skip HP ENI）。
- **新版 terway + 新版 erdma-controller**：controller 模式完全零配置共存。
- **误开 HP + 无 erdma-controller**（尤其小机型）：controller 模式下这张卡被 skip、其 IP 不可用;想收回容量,直接从 **ECS 控制台把该 ENI 从节点解绑**。
- **存量已被老版 terway 纳管的 ERI（升级场景）**：controller 模式下**不会被驱逐**,存量 Pod 继续可用、自然排空(见第 4 节);要彻底把这张卡还给 erdma-controller,**重建节点**。
- **daemon/legacy 模式**：行为与本特性发布前完全一致,不受影响;ERDMA 卡仍靠 `eni_tag_filter` 白名单或 ECS 控制台解绑规避。

## 与 erdma-controller 侧的协议

| erdma-controller 责任 | terway 责任 |
|----------------------|------------|
| 创建/接管 ERDMA ENI 时打 `creator=alibabacloud-erdma-controller` | 默认黑名单识别此 (key, value) |
| 同时打 `terway.alibabacloud.com/excluded=true` | 默认黑名单识别此 (key, value) |
| ENI 上有 `NetworkInterfaceTrafficMode=HighPerformance` | controller 模式 `assignIPFromLocalPool` 默认 skip，不给非 RDMA Pod 用 |

两侧任一改动单独发版都不引入回归；同时升级即可零配置共存。
