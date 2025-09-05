# Terway CNI Plugin

CNI plugin for Alibaba Cloud VPC/ENI

[![Go Report Card](https://goreportcard.com/badge/github.com/AliyunContainerService/terway)](https://goreportcard.com/report/github.com/AliyunContainerService/terway)
[![codecov](https://codecov.io/gh/AliyunContainerService/terway/branch/main/graph/badge.svg)](https://codecov.io/gh/AliyunContainerService/terway)
[![Linter](https://github.com/AliyunContainerService/terway/workflows/check/badge.svg)](https://github.com/marketplace/actions/super-linter)

English | [简体中文](./README-zh_CN.md)

## Introduction

Terway is a self-developed CNI (Container Network Interface) plugin for ACK (Alibaba Cloud Kubernetes), built on Alibaba Cloud's Elastic Network Interface (ENI) technology. It optimizes cloud resource usage and enhances network performance. Terway supports eBPF for traffic acceleration, reducing latency, and adheres to Kubernetes Network Policy standards for container-to-container access control.

In Terway, each Pod has its own network stack and IP address. Pods on the same ECS (Elastic Compute Service) instance communicate directly, while cross-ECS Pod communication transits directly through VPC ENIs, avoiding encapsulation with technologies like VxLAN for higher communication performance.

## 📖 Documentation

For comprehensive documentation, tutorials, and guides, visit our **[Terway Wiki](wiki/Home.md)**:

- **[Getting Started](wiki/Getting-Started.md)** - New to Terway? Start here!
- **[Installation Guide](wiki/Installation-Guide.md)** - Step-by-step installation
- **[User Guide](wiki/Home.md#-user-guide)** - Configuration and usage
- **[Developer Guide](wiki/Home.md#-developer-guide)** - Contributing and development
- **[Troubleshooting](wiki/troubleshooting/FAQ.md)** - FAQ and common issues
- **[Reference](wiki/Home.md#-reference)** - API and configuration reference

## Features

- **ENI Network Mode**: Allocates ENIs to Pods for optimized resource utilization and network performance.
- **Trunking Feature**: Allows Pods to have independent ENIs for flexible security group and switch configurations.
- **Node Pool Network Mode Configuration**: Supports configuring node pools for exclusive ENI usage.
- **Security Policies**: Supports NetworkPolicy and traditional security groups for multi-dimensional network security control.
- **High Performance**: Utilizes eBPF for protocol stack acceleration, ensuring low latency and high throughput.
- **IPv6 Support**: Dual-stack support for both IPv4 and IPv6.
- **Intelligent Computing Lingjun**：Linjun support.

### Deprecated Features

- **VPC Network Mode**: Direct communication to VPC resources using VPC routing.

- **Exclusive ENI Mode**: Direct ENI attachment to Pods for maximum performance.（Replace with configuring the network mode through node pool dimension as a dedicated ENI.)

## Version Differences

ACK-provided versions are identical to the open-source version, except the Trunking feature is not available in self-hosted clusters.

## Contributions

We warmly welcome community contributions! Whether it's bug fixes, new features, documentation improvements, or code enhancements, your help is appreciated.

[Report Issues](https://github.com/AliyunContainerService/terway/issues/new)
[Submit Pull Request](https://github.com/AliyunContainerService/terway/compare)

## Security

If you discover a security vulnerability in the code, please contact [kubernetes-security@service.aliyun.com](mailto:kubernetes-security@service.aliyun.com). Refer to [SECURITY.md](SECURITY.md) for details.

## Community

### DingTalk

Join `DingTalk` group by `DingTalkGroup` id "35924643".
