# IPv6 Only 开发验证记录

2026-09-07：集群维度 IPv6 Only 已实现；default、CRD 两种 IPAM 均通过新建 ACK BYO 双栈集群上的 CNI 验收。没有执行协议栈迁移。

## 环境与构建

- 基线：`aad92ade`，首次实云验收时叠加工作区修改；实现随后提交为 `f5d6e0c7`，review 修正为 `d394aef4`。
- ACK：`1.35.7-aliyun.1`，杭州 J/K 两个可用区，每集群两个 `ecs.g7nex.2xlarge` 普通 Linux 工作节点。
- ACK、节点和 Service CIDR 保持双栈；Terway 从首次安装即配置 `ipv6`。
- 每 ENI IPv6 配额 15；验收扩容到每节点 16 个测试 Pod，共 32 个。
- daemon/CNI 镜像：`registry.cn-hangzhou.aliyuncs.com/l1b0k/terway:ipv6-only-20260907-aad92ade`。
- controlplane 镜像：同仓库命名空间的 `terway-controlplane:ipv6-only-20260907-aad92ade`。
- 测试容器：同仓库 `terway:ipv6-only-python-20260907`，Python 3.12。

| IPAM | ACK 集群 ID | Terraform 工作目录 |
| --- | --- | --- |
| default | `c9397b2b65acd426e9af831cb03eb680c` | `hack/terraform/ack/runs/byo-dual-cni-ipv6-default-20260907` |
| CRD | `caafeb464fd2a455d97b5e8d528098931` | `hack/terraform/ack/runs/byo-dual-cni-ipv6-crd-20260907` |

集群保留供复查，验收创建的命名空间和测试 Pod 已删除。工作目录含凭据和 Terraform 状态，保持在 gitignore 内。

## 通过的检查

- Linux `dev`：完整 `make test` 成功，包含 kind 默认、datapath v2、legacy 验证，以及 privileged、race、envtest 测试。
- datapath 全包额外连续运行三次成功；新增 IPv6-only IPvlan、policy-route 配置与幂等删除测试通过。
- `make fmt`、Linux 目标 `make lint`（0 issues）、`make vet` 通过。`go mod tidy && go mod vendor` 后依赖无变化。
- Terraform validate、fmt 和部署脚本语法、Helm 两种 IPAM 渲染及协议栈不一致拒绝检查通过。
- 两种 IPAM 均通过：每 Pod 只有一个 IPv6、非 loopback 接口没有 IPv4、没有 IPv4 路由、IPv6 不重复；同节点及跨节点双向 TCP/UDP；IPv6 Service 和 EndpointSlice；超过单 ENI 配额扩容；daemon 重启后地址保持及互通；CRD controller 重启恢复；删除重建和缩容。

初次完整测试暴露 policy-route 测试夹具缺少就绪的宿主机 IPv6，以及 MultiNetwork 测试错误地使用宿主机 ENI 索引查询容器路由表。已补齐真实宿主机地址夹具并修正索引，随后完整测试通过；没有修改生产数据路径来绕过测试。

## ECS 地址与回收证据

两种 IPAM 在扩容阶段均达到每节点两张 Pod ENI。所有这些 ENI 的 IPv4 集合都只有一个 Primary IP。CRD 的 Primary IPv4 保留在 ENI 元数据中，不参与 Pod 绑定。

缩容和删除测试命名空间后，观察到 `UnassignIpv6Addresses`、空 ENI 删除成功；最终每节点只剩一张 Pod ENI：

| IPAM | 节点 | 剩余 IPv6 | 系统 Pod 使用 | 空闲 IPv6 | IPv4 |
| --- | --- | ---: | ---: | ---: | ---: |
| default | `172.16.3.117` | 8 | 3 | 5 | 1 Primary |
| default | `172.16.4.149` | 6 | 1 | 5 | 1 Primary |
| CRD | `172.16.2.255` | 6 | 1 | 5 | 1 Primary |
| CRD | `172.16.5.236` | 8 | 3 | 5 | 1 Primary |

空闲数量与 `max_pool_size=5` 一致。创建请求不传额外 IPv4 数量、部分 IPv6 返回后的补充分配、缺失 IPv6 的失败处理，另有单元测试覆盖。

## 范围与证据位置

本次没有测试 ACK API/DNS 的纯 IPv6 访问；其默认 Service 仍为 IPv4，部分普通系统组件因此无法正常访问 API。这不影响本次直接验证 CNI 的测试结果，也不代表已验证整个 ACK 集群的纯 IPv6 工作负载兼容性。`hack/ipv6-only-e2e.py --check-cluster-services` 可在准备好 IPv6 DNS/API 前端后执行额外检查。

没有在真实云资源上注入 vSwitch IPv4 耗尽、云 API IPv6 分配失败。相关边界通过单元测试验证，云端故障注入场景保留在[开发与测试方案](ipv6-only.md)中，不计为已完成的实云验收。其他功能以及已有集群迁移不在本次范围。

每个 Terraform 工作目录的 `artifacts/` 保存 `e2e.log`、Pod/节点/事件快照、`cloud-expanded.json`、`cloud-reclaimed.json` 和组件日志。Linux 完整日志位于 `hack/terraform/ack/runs/ipv6-only-linux-20260907/make-test-final.log`；dev 原始记录位于 `/root/terway-ipv6-only-20260907/artifacts/`。

创建过程中发现原模板 Kubernetes 版本已不可用，仅新 IPv6 Only 工作目录使用实测版本，共享默认值和已有工作目录保持原值。CRD 集群的一个零节点扩展池首次并发创建失败，已仅重建该空池恢复 Terraform apply；普通工作节点未受影响。
