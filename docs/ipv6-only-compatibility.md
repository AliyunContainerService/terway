# IPv6 Only review 与存量影响检查

最终结论：发现并修正了对存量路径的潜在影响；最终 Linux 完整回归通过，已检查范围内未发现新增 IPv4/双栈回归。Windows CNI 的基线构建问题仍存在，不作全功能零影响承诺。

检查基线为 `aad92ade`。初始实现提交为 `f5d6e0c7`，review 修正提交为 `d394aef4`。

## Review 发现与修正

| 发现 | 可能影响 | 修正 |
| --- | --- | --- |
| ENI attach 后预热累计改用双栈地址数的 `min` | IPv4/IPv6 返回数量不一致时改变既有预热行为 | IPv4/双栈继续使用原来的 `max`；仅 IPv6 Only 按 IPv6 计数 |
| 节点 SufficientIP 条件在双栈下额外检查 IPv6 | 既有节点可能改变 condition，影响依赖该条件的组件 | IPv4/双栈保持原有 IPv4 判断；IPv6 Only 才检查 IPv6 |
| 创建 ENI 返回空 IPv6 时无条件提前失败 | 双栈原有 attach/响应处理被替换成失败与回收路径 | 提前失败仅对 IPv6 Only 生效 |
| 创建请求队列改用实际返回数量，以及统一过滤禁用地址族 | 扩大了本次对既有创建响应处理的修改范围 | IPv4/双栈保持原有请求计数和返回值；仅 IPv6 Only 保留部分响应补分配与 Primary IPv4 隔离 |
| IPv6 Only 空响应在 attach 前返回失败 | 清理流程先 detach，尚未挂载的 ENI 可能无法进入删除步骤 | 将检查移到 attach 成功后；新增 attach → 失败 → detach/delete 顺序测试 |
| 共享 Terraform 默认 Kubernetes 版本被升级 | 存量 Terraform 工作目录后续 plan 可能出现版本变更 | 恢复共享默认值，仅新建 IPv6 Only 工作目录使用实测版本；已有 tfvars 不覆盖 |

ECS 的 detach 前置状态要求依据[官方 API 文档](https://www.alibabacloud.com/help/id/ecs/developer-reference/api-ecs-2014-05-26-detachnetworkinterface)。新增检查不会把空 IPv6 响应作为 Pod 分配成功返回。

修正测试包括：实际双栈 ENI attach 的不等量预热响应、IPv4/双栈节点条件、双栈创建缺少 IPv6 的原有响应处理；原 IPv6 Only 缺失/部分响应测试继续保留。

## 存量路径检查

| 范围 | 结论与证据 |
| --- | --- |
| IPv4 / dual 默认配置 | 未改变默认栈；原有 IPv4/dual 节点级配置覆盖继续允许。新增一致性限制只涉及 IPv6 Only |
| default IPAM | IPv4/dual 的创建数量、请求队列计数、返回地址族不变；IPv6 Only 分支单独处理 |
| CRD IPAM | IPv4/dual 的容量基准、预热、节点条件、vSwitch IPv4 余量检查及回收规则保持基线语义 |
| Primary IPv4 | 存量 IPv4/dual 仍可按原语义使用；仅新模式将它隔离在 ENI 元数据中 |
| trunk / ERDMA / 独占 ENI / Prefix | 对存量 IPv4/dual 配置保留既有分支；不宣称新增 IPv6 Only 对这些功能的支持 |
| CNI 数据路径、NetworkPolicy | 生产 datapath、driver、CNI 代码相对基线无修改；完整 Linux 测试验证原有路径 |
| Helm | IPv4/dual × 两种 IPAM × 默认数据路径/datapath v2/启用策略，共 12 组渲染结果与基线逐字节相同 |
| ACK 创建脚本 | 原四种 profile 保持协议栈、CRD IPAM 和扩展节点池数量；原 tfvars 内容不变，已有工作目录配置不覆盖 |
| 持久化和接口 | CRD 定义及生成物、RPC、依赖及 vendor 无变化；未加入存量数据转换或迁移流程 |

`getAllocatable` 增加了 nil 防护；有效记录的筛选条件未变。

## 验证与边界

- 最终代码在 dev 上完整 `make test` 通过，包含三种 kind 配置、privileged/race/envtest；日志为 `ipv6-regression-final.log`。已核对所有 21 个变更 Go 文件与 dev 测试目录的 SHA-256 一致。
- Linux 针对 `pkg/controller/multi-ip/node`、`pkg/factory/aliyun`、`pkg/eni` 的 race 测试通过；lint 为 0 issues。
- Windows 下 `daemon`、`pkg/eni`、`pkg/factory/aliyun` 交叉编译通过。
- Windows CNI 完整构建失败；已对 `aad92ade` 建立独立工作树复现，编译错误逐字节一致，涉及 `datapath.PrioMap`、`utils.Hook`、`logger` 等已有缺失符号。没有将这项检查报告为通过，也没有在本次扩大范围修复。
- 先前两套 ACK CNI 验收对应初始实现，详见[实云报告](ipv6-only-validation.md)。本轮以存量行为回归为重点，没有对已有线上集群执行部署或协议栈变更，也未进行 Windows、ERDMA 等专项实云验证。

本轮证据保存在 `hack/terraform/ack/runs/ipv6-only-linux-20260907/`。Linux 完整回归从 `make test` 执行；因 GitHub 下载超时，bootstrap 复用了 dev 已验证版本的 kind v0.26.0、kubectl v1.30.8 和 Helm v3.21.4，仅通过临时 curl 缓存包装器提供依赖，没有删减或替换测试目标。
