# Terway Wiki

Welcome to the Terway CNI Plugin documentation wiki! This directory contains comprehensive documentation for users, developers, and operators of Terway.

## 📖 How to Use This Wiki

### For New Users
Start with these documents:
1. **[Getting Started](Getting-Started.md)** - Introduction to Terway
2. **[Installation Guide](Installation-Guide.md)** - How to install Terway
3. **[Quick Start Tutorial](Quick-Start-Tutorial.md)** - Hands-on tutorial

### For Experienced Users
- **[Network Modes](user-guide/Network-Modes.md)** - Choose the right network mode
- **[Configuration](user-guide/Configuration.md)** - Detailed configuration options
- **[Troubleshooting](troubleshooting/Common-Issues.md)** - Common issues and solutions

### For Developers
- **[Contributing](developer-guide/Contributing.md)** - How to contribute
- **[Development Setup](developer-guide/Development-Setup.md)** - Development environment setup

## 🗂️ Documentation Structure

```
wiki/
├── Home.md                          # Main wiki homepage
├── Getting-Started.md               # Getting started guide
├── Installation-Guide.md            # Installation instructions
├── Quick-Start-Tutorial.md          # Hands-on tutorial
├── _Sidebar.md                     # Navigation sidebar
│
├── user-guide/                     # User documentation
│   ├── Network-Modes.md            # Network modes guide
│   ├── Configuration.md            # Configuration reference
│   ├── Security-Policies.md        # Security policies (planned)
│   ├── IPv6-Support.md             # IPv6 configuration (planned)
│   ├── eBPF-Acceleration.md        # eBPF features (planned)
│   └── QoS-Traffic-Control.md      # QoS configuration (planned)
│
├── developer-guide/                # Developer documentation
│   ├── Contributing.md             # Contribution guidelines
│   ├── Development-Setup.md        # Development environment
│   ├── Architecture.md             # Architecture docs (planned)
│   └── Building-from-Source.md     # Build instructions (planned)
│
├── reference/                      # Reference documentation
│   ├── Terway-CLI.md              # CLI reference
│   ├── CNI-Configuration.md        # CNI config reference (planned)
│   ├── Dynamic-Configuration.md    # Dynamic config (planned)
│   ├── Environment-Variables.md    # Environment variables (planned)
│   ├── gRPC-API.md                 # API documentation (planned)
│   └── Metrics.md                  # Metrics reference (planned)
│
├── troubleshooting/               # Troubleshooting guides
│   ├── FAQ.md                     # Frequently asked questions
│   ├── Common-Issues.md           # Common issues and solutions
│   ├── Debugging-Guide.md         # Debugging procedures (planned)
│   └── Log-Analysis.md            # Log analysis guide (planned)
│
├── advanced/                      # Advanced topics
│   ├── Centralized-IPAM.md       # Centralized IPAM
│   ├── Cilium-Integration.md      # Cilium integration (planned)
│   ├── Hubble-Integration.md      # Hubble integration (planned)
│   ├── ECS-Nodes.md               # ECS node documentation (planned)
│   └── Lingjun-Support.md         # Lingjun support (planned)
│
└── zh-cn/                         # Chinese translations
    ├── Home.md                    # Chinese homepage
    ├── 入门指南.md                 # Chinese getting started
    └── ...                       # Additional Chinese docs (planned)
```

## 🌟 Key Features of This Wiki

### Comprehensive Coverage
- **Complete user journey**: From installation to advanced configuration
- **Multiple audiences**: Users, developers, and operators
- **Practical examples**: Real-world scenarios and code samples
- **Troubleshooting**: Solutions to common problems

### Easy Navigation
- **Sidebar navigation**: Quick access to all sections
- **Cross-references**: Links between related topics
- **Search-friendly**: Well-structured for easy searching
- **Progressive disclosure**: Start simple, dive deeper as needed

### Up-to-Date Information
- **Current versions**: Documentation matches latest Terway versions
- **Best practices**: Recommended approaches and configurations
- **Security focus**: Security considerations throughout
- **Performance tips**: Optimization guidance

## 🚀 Quick Start Paths

### Path 1: New to Terway
```
Getting Started → Installation Guide → Quick Start Tutorial
```

### Path 2: Experienced Kubernetes User
```
Network Modes → Configuration → Security Policies
```

### Path 3: Developer
```
Architecture → Development Setup → Contributing
```

### Path 4: Operations/SRE
```
Installation Guide → Configuration → Troubleshooting → Monitoring
```

## 🔍 Finding Information

### By Topic
- **Installation**: Installation Guide, Configuration Reference
- **Networking**: Network Modes, IPv6 Support, Security Policies
- **Performance**: eBPF Acceleration, QoS, Advanced Topics
- **Development**: Development Setup, Contributing, Architecture
- **Operations**: Troubleshooting, CLI Reference, Monitoring

### By Skill Level
- **Beginner**: Getting Started, Installation Guide, Quick Start
- **Intermediate**: Network Modes, Configuration, Security Policies
- **Advanced**: Advanced Topics, Architecture, Development

### By Use Case
- **First-time setup**: Installation Guide → Quick Start Tutorial
- **Performance optimization**: eBPF Acceleration → QoS Configuration
- **Security hardening**: Security Policies → Advanced Topics
- **Troubleshooting**: Common Issues → Debugging Guide → CLI Reference

## 🌐 Language Support

### Available Languages
- **English**: Complete documentation (this wiki)
- **简体中文**: Core documentation (zh-cn/ directory)

### Contributing Translations
We welcome translations to additional languages! See our [Contributing Guide](developer-guide/Contributing.md#translation) for guidelines.

## 📝 Contributing to Documentation

### How to Contribute
1. **Find areas to improve**: Missing topics, unclear explanations, outdated info
2. **Follow the structure**: Use existing document templates and organization
3. **Write clearly**: Use simple language, provide examples
4. **Test your changes**: Verify links and code examples work
5. **Submit a PR**: Follow the contribution guidelines

### Documentation Standards
- **Markdown format**: Use standard Markdown syntax
- **Code examples**: Include working, tested examples
- **Screenshots**: For UI changes, include before/after screenshots
- **Links**: Use relative links for internal documentation
- **Structure**: Follow the established directory structure

### What We Need
- **More examples**: Real-world configuration examples
- **Video tutorials**: Visual learning content
- **Use case studies**: How organizations use Terway
- **Performance data**: Benchmarks and optimization tips
- **Advanced topics**: Deep-dive technical content

## 🆘 Getting Help

### Community Support
- **DingTalk Group**: Join using ID `35924643`
- **GitHub Discussions**: For community questions and discussions
- **GitHub Issues**: For bug reports and feature requests

### Documentation Issues
If you find issues with the documentation:
1. **Check existing issues**: Search GitHub issues first
2. **Create detailed reports**: Include page URL, issue description, suggested fix
3. **Contribute fixes**: Submit a PR with improvements

## 📊 Documentation Metrics

This wiki aims to:
- **Reduce support burden**: Answer common questions proactively
- **Improve user experience**: Make Terway easier to use and understand
- **Accelerate adoption**: Help users get started quickly
- **Enable contribution**: Make it easy for others to contribute

## 🎯 Roadmap

### Completed ✅
- Core documentation structure
- Getting started guides
- Installation documentation
- CLI reference
- Basic troubleshooting

### In Progress 🚧
- Additional user guides
- Advanced configuration topics
- Chinese translations
- Code examples and tutorials

### Planned 📋
- Video tutorials
- Interactive examples
- Performance benchmarks
- Migration guides
- Best practices collection

---

**Need something that's not here?** [Open an issue](https://github.com/AliyunContainerService/terway/issues/new) or [contribute it yourself](developer-guide/Contributing.md)!

**Happy networking with Terway!** 🚀