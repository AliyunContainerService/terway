# Getting Started with Terway

Welcome to Terway! This guide will help you understand what Terway is and how to get started with it.

## What is Terway?

Terway is a Container Network Interface (CNI) plugin specifically designed for Alibaba Cloud's Virtual Private Cloud (VPC) and Elastic Network Interface (ENI) technology. It provides high-performance, secure, and scalable networking for Kubernetes containers running on Alibaba Cloud.

## Key Benefits

### 🚀 **High Performance**
- **eBPF Acceleration**: Utilizes eBPF technology for protocol stack acceleration
- **Direct ENI Communication**: Pods communicate directly through VPC ENIs without encapsulation
- **Low Latency**: Optimized for minimal network latency

### 🔒 **Security**
- **Network Policies**: Full support for Kubernetes NetworkPolicy
- **Security Groups**: Integration with Alibaba Cloud security groups
- **Isolation**: Strong network isolation between workloads

### 🛠️ **Flexibility**
- **Multiple Network Modes**: Support for various networking configurations
- **IPv6 Support**: Dual-stack IPv4/IPv6 capabilities
- **Trunking**: Advanced ENI trunking for complex scenarios

## Prerequisites

Before getting started with Terway, ensure you have:

1. **Alibaba Cloud Account** with appropriate permissions
2. **ACK Cluster** or self-managed Kubernetes cluster on Alibaba Cloud
3. **VPC and VSwitches** configured in your region
4. **RAM Permissions** for ENI operations (see [Installation Guide](Installation-Guide.md))

## Network Modes Overview

Terway supports several network modes to meet different requirements:

### ENI Network Mode (Recommended)
- Each Pod gets an IP from the ENI subnet
- High performance with direct VPC communication
- Supports security groups and network policies

### Trunking Mode
- Advanced ENI trunking capability
- Pods can have independent ENIs
- Flexible security group and vSwitch configuration per Pod

### Deprecated Modes
- **VPC Mode**: Direct VPC routing (deprecated)
- **Exclusive ENI Mode**: Direct ENI attachment (replaced by node pool configuration)

## Quick Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                       │
├─────────────────────────────────────────────────────────────┤
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │   Pod    │  │   Pod    │  │   Pod    │  │   Pod    │   │
│  │  (ENI)   │  │  (ENI)   │  │  (ENI)   │  │  (ENI)   │   │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘   │
├─────────────────────────────────────────────────────────────┤
│                      Terway CNI                            │
├─────────────────────────────────────────────────────────────┤
│                  Alibaba Cloud VPC                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │   vSwitch   │  │   vSwitch   │  │   vSwitch   │        │
│  │   Zone A    │  │   Zone B    │  │   Zone C    │        │
│  └─────────────┘  └─────────────┘  └─────────────┘        │
└─────────────────────────────────────────────────────────────┘
```

## Next Steps

Now that you understand the basics, here's what to do next:

1. **[Installation Guide](Installation-Guide.md)** - Install Terway in your cluster
2. **[Quick Start Tutorial](Quick-Start-Tutorial.md)** - Deploy your first application
3. **[Network Modes](user-guide/Network-Modes.md)** - Choose the right network mode
4. **[Configuration](user-guide/Configuration.md)** - Configure Terway for your needs

## Getting Help

If you need help:

- 📖 Check the [FAQ](troubleshooting/FAQ.md) for common questions
- 🐛 Report issues on [GitHub](https://github.com/AliyunContainerService/terway/issues)
- 💬 Join our DingTalk group: `35924643`
- 📧 Security issues: [kubernetes-security@service.aliyun.com](mailto:kubernetes-security@service.aliyun.com)

## What's Next?

Ready to install Terway? Head over to the [Installation Guide](Installation-Guide.md) to get started!