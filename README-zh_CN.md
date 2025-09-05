# Terway 网络插件  

CNI plugin for alibaba cloud VPC/ENI

[![Go Report Card](https://goreportcard.com/badge/github.com/AliyunContainerService/terway)](https://goreportcard.com/report/github.com/AliyunContainerService/terway)
[![codecov](https://codecov.io/gh/AliyunContainerService/terway/branch/main/graph/badge.svg)](https://codecov.io/gh/AliyunContainerService/terway)
[![Linter](https://github.com/AliyunContainerService/terway/workflows/check/badge.svg)](https://github.com/marketplace/actions/super-linter)

[English](./README.md) | 简体中文

## 简介

Terway网络插件是ACK自研的容器网络接口（CNI）插件，基于阿里云的弹性网卡（ENI）构建网络，可以充分利用云上资源。Terway支持eBPF对网络流量进行加速，降低延迟，支持基于Kubernetes标准的网络策略（Network Policy）来定义容器间的访问策略。

在Terway网络插件中，每个Pod都拥有自己的网络栈和IP地址。同一台ECS内的Pod之间通信，直接通过机器内部的转发，跨ECS的Pod通信、报文通过VPC的弹性网卡直接转发。由于不需要使用VxLAN等的隧道技术封装报文，因此Terway模式网络具有较高的通信性能。

## 📖 文档

查看完整的文档、教程和指南，请访问我们的 **[Terway Wiki](wiki/zh-cn/Home.md)**：

- **[入门指南](wiki/zh-cn/入门指南.md)** - 初次使用 Terway？从这里开始！
- **[安装指南](wiki/zh-cn/安装指南.md)** - 详细的安装步骤说明
- **[用户指南](wiki/zh-cn/Home.md#-用户指南)** - 配置和使用说明
- **[开发者指南](wiki/zh-cn/Home.md#-开发者指南)** - 贡献和开发指南
- **[故障排查](wiki/zh-cn/Home.md#-故障排查)** - 常见问题解答
- **[参考文档](wiki/zh-cn/Home.md#-参考文档)** - API 和配置参考

## 特性

- ENI网络模式：分配 Elastic Network Interfaces (ENIs) 给Pod，优化资源利用率和网络性能。
- Trunking功能：允许Pod配置独立的ENI，支持灵活安全组、交换机配置。
- 节点池维度网络模式配置：支持节点池配置为独占ENI。
- 安全策略：支持NetworkPolicy和传统的安全组，提供多维度的网络安全控制。
- 高性能：使用eBPF加速协议栈，确保低延迟和高吞吐量。
- IPv6: 支持IPv4/IPv6双栈。
- 灵骏: 支持智能计算灵骏。

### 以下功能已经废弃

- VPC网络模式：利用VPC路由，实现容器与VPC内其他资源的直接通信。
- 独占ENI模式：将ENI直通进Pod，最大化网络性能。（替换为通过节点池维度网络模式配置为独占ENI）

## 版本差异

ACK 提供的版本和开源一致。仅Trunking功能无法在自建集群使用。

## 贡献

我们非常欢迎社区的贡献！无论是修复bug、新增功能、改进文档，或者仅仅是对现有代码的改进，你的帮助都将被我们珍视。

[报告问题](https://github.com/AliyunContainerService/terway/issues/new)
[提交Pull Request](https://github.com/AliyunContainerService/terway/compare)

## 安全

如果您发现了代码中的安全漏洞，请联系[kubernetes-security@service.aliyun.com](mailto:kubernetes-security@service.aliyun.com)。详见 [SECURITY.md](SECURITY.md)

## 社区

### 钉钉群

通过钉钉群号 "35924643" 加入`钉钉`群组。
