# Terway 网络插件 Wiki

欢迎来到 Terway 网络插件文档 Wiki！Terway 是专为阿里云 VPC 和弹性网卡（ENI）技术设计的高性能容器网络接口（CNI）插件。

## 🚀 快速开始

- **[入门指南](入门指南.md)** - 初次使用 Terway？从这里开始！
- **[安装指南](安装指南.md)** - 详细的安装步骤说明
- **[快速入门教程](快速入门教程.md)** - 几分钟内快速上手

## 📖 用户指南

### 基础使用
- **[网络模式](user-guide/网络模式.md)** - 了解不同的网络模式
- **[配置说明](user-guide/配置说明.md)** - 根据需求配置 Terway
- **[安全策略](user-guide/安全策略.md)** - 网络策略和安全组

### 高级特性
- **[Trunking](user-guide/Trunking.md)** - ENI trunking 实现灵活网络
- **[IPv6 支持](user-guide/IPv6-支持.md)** - 双栈 IPv4/IPv6 配置
- **[eBPF 加速](user-guide/eBPF-加速.md)** - 使用 eBPF 实现高性能网络
- **[QoS 和流量控制](user-guide/QoS-流量控制.md)** - 带宽管理

## 🔧 开发者指南

- **[架构设计](developer-guide/架构设计.md)** - Terway 的设计和架构
- **[开发环境搭建](developer-guide/开发环境搭建.md)** - 搭建开发环境
- **[贡献指南](developer-guide/贡献指南.md)** - 如何为 Terway 做贡献
- **[源码构建](developer-guide/源码构建.md)** - 自行构建 Terway

## 📚 参考文档

### 配置参考
- **[CNI 配置](reference/CNI-配置.md)** - 完整的 CNI 配置参考
- **[动态配置](reference/动态配置.md)** - 运行时配置变更
- **[环境变量](reference/环境变量.md)** - 所有环境变量说明

### CLI 和工具
- **[Terway CLI](reference/Terway-CLI.md)** - 命令行接口参考
- **[调试工具](reference/调试工具.md)** - 故障排查工具

### API 参考
- **[gRPC API](reference/gRPC-API.md)** - 内部 gRPC API 文档
- **[监控指标](reference/监控指标.md)** - 监控和指标

## 🐛 故障排查

- **[常见问题](troubleshooting/常见问题.md)** - 经常遇到的问题
- **[FAQ](troubleshooting/FAQ.md)** - 常见问题解答
- **[调试指南](troubleshooting/调试指南.md)** - 如何调试网络问题
- **[日志分析](troubleshooting/日志分析.md)** - 理解 Terway 日志

## 🏗️ 高级主题

- **[中心化 IPAM](advanced/中心化-IPAM.md)** - 中心化 IP 地址管理
- **[Cilium 集成](advanced/Cilium-集成.md)** - 与 Cilium 配合使用
- **[Hubble 集成](advanced/Hubble-集成.md)** - 使用 Hubble 进行网络观测
- **[ECS 节点](advanced/ECS-节点.md)** - 使用 ECS 实例
- **[灵骏支持](advanced/灵骏支持.md)** - 智能计算灵骏

## 🌐 多语言支持

本 Wiki 支持多种语言：
- **[English](../Home.md)** - 英文文档
- **简体中文** (当前)

## 🤝 社区和支持

- **[问题反馈](https://github.com/AliyunContainerService/terway/issues/new)** - Bug 报告和功能请求
- **[讨论论坛](https://github.com/AliyunContainerService/terway/discussions)** - 社区讨论
- **[安全](https://github.com/AliyunContainerService/terway/blob/main/SECURITY.md)** - 安全漏洞报告
- **钉钉群**: 使用群号 `35924643` 加入

## 📄 许可证

Terway 使用 [Apache License 2.0](https://github.com/AliyunContainerService/terway/blob/main/LICENSE) 许可证。

---

💡 **提示**: 使用导航栏快速跳转到任何章节。如果您是 Terway 的新用户，我们建议从[入门指南](入门指南.md)开始。