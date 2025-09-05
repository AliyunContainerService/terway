# Contributing to Terway

Thank you for your interest in contributing to Terway! We welcome contributions from the community and are pleased to have you join us.

## 🤝 Ways to Contribute

There are many ways to contribute to Terway:

- **Report bugs** and request features
- **Improve documentation** and fix typos
- **Submit code patches** and new features
- **Review pull requests** from other contributors
- **Help with testing** and validation
- **Share your use cases** and experiences

## 🚀 Getting Started

### 1. Fork and Clone

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/terway.git
   cd terway
   ```
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/AliyunContainerService/terway.git
   ```

### 2. Set Up Development Environment

See our [Development Setup Guide](Development-Setup.md) for detailed instructions on:
- Installing dependencies
- Setting up your build environment
- Running tests locally

## 📝 Contribution Guidelines

### Code Style

- **Go Code**: Follow standard Go formatting with `gofmt`
- **Comments**: Write clear, concise comments for public APIs
- **Error Handling**: Always handle errors appropriately
- **Testing**: Include tests for new functionality

### Commit Messages

Use clear and descriptive commit messages:

```
component: brief description of the change

Longer explanation of what changed and why, if necessary.
Include any breaking changes or special notes.

Fixes #123
```

Examples:
```bash
git commit -m "daemon: fix memory leak in ENI pool management

The ENI pool was not properly releasing memory when ENIs were
removed from the pool. This change ensures proper cleanup.

Fixes #456"
```

### Pull Request Process

1. **Create a branch** for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes** following our guidelines

3. **Test your changes**:
   ```bash
   make test
   make lint
   ```

4. **Commit and push**:
   ```bash
   git commit -m "your commit message"
   git push origin feature/your-feature-name
   ```

5. **Create a Pull Request** on GitHub

### Pull Request Template

When creating a PR, please include:

```markdown
## Description
Brief description of the changes made.

## Type of Change
- [ ] Bug fix (non-breaking change which fixes an issue)
- [ ] New feature (non-breaking change which adds functionality)
- [ ] Breaking change (fix or feature that would cause existing functionality to not work as expected)
- [ ] Documentation update

## Testing
- [ ] Unit tests pass
- [ ] Integration tests pass
- [ ] Manual testing completed

## Checklist
- [ ] My code follows the style guidelines
- [ ] I have performed a self-review of my code
- [ ] I have commented my code, particularly in hard-to-understand areas
- [ ] I have made corresponding changes to the documentation
- [ ] My changes generate no new warnings
```

## 🧪 Testing

### Running Tests

```bash
# Run unit tests
make test

# Run integration tests
make test-integration

# Run specific test
go test -v ./pkg/daemon/...
```

### Writing Tests

- **Unit tests**: Test individual functions and components
- **Integration tests**: Test component interactions
- **E2E tests**: Test full scenarios

Example test structure:
```go
func TestENIAllocation(t *testing.T) {
    // Setup
    pool := NewENIPool(config)
    
    // Test
    eni, err := pool.Allocate()
    
    // Assertions
    assert.NoError(t, err)
    assert.NotNil(t, eni)
    assert.Equal(t, "eni-12345", eni.ID)
}
```

## 🐛 Reporting Bugs

When reporting bugs, please include:

### Bug Report Template

```markdown
**Describe the bug**
A clear and concise description of what the bug is.

**To Reproduce**
Steps to reproduce the behavior:
1. Go to '...'
2. Click on '....'
3. Scroll down to '....'
4. See error

**Expected behavior**
A clear description of what you expected to happen.

**Environment:**
- Terway version: [e.g. v1.0.10]
- Kubernetes version: [e.g. 1.20.4]
- OS: [e.g. Ubuntu 20.04]
- Kernel version: [e.g. 5.4.0]
- Instance type: [e.g. ecs.c6.large]

**Logs**
```
Paste relevant logs here
```

**Additional context**
Add any other context about the problem here.
```

## 💡 Feature Requests

We welcome feature requests! Please include:

- **Use case**: Describe your scenario and why this feature would be helpful
- **Proposed solution**: If you have ideas on how to implement it
- **Alternatives**: Any alternative solutions you've considered
- **Additional context**: Any other relevant information

## 📚 Documentation

Documentation improvements are always welcome:

- **Fix typos** and grammatical errors
- **Improve clarity** of existing docs
- **Add examples** and use cases
- **Create new guides** for advanced topics
- **Translate content** to other languages

### Documentation Structure

```
wiki/
├── Home.md                 # Main wiki homepage
├── Getting-Started.md      # Getting started guide
├── Installation-Guide.md   # Installation instructions
├── user-guide/            # User documentation
├── developer-guide/       # Developer documentation
├── reference/             # Reference documentation
├── troubleshooting/       # Troubleshooting guides
├── advanced/              # Advanced topics
└── zh-cn/                 # Chinese translations
```

## 🌐 Translation

Help us make Terway accessible to more users by contributing translations:

- **Chinese (Simplified)**: We maintain Chinese versions of key documents
- **Other languages**: We welcome translations to other languages

Translation guidelines:
- Keep technical terms consistent
- Maintain the same structure as English docs
- Update translations when English docs change

## 🔒 Security

### Reporting Security Issues

**Do NOT** report security vulnerabilities through public GitHub issues.

Instead, email us at: [kubernetes-security@service.aliyun.com](mailto:kubernetes-security@service.aliyun.com)

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Any suggested fixes

### Security Best Practices

When contributing code:
- **Validate inputs** properly
- **Handle secrets** securely
- **Follow least privilege** principle
- **Keep dependencies** up to date

## 👥 Community

### Communication Channels

- **GitHub Discussions**: For general questions and discussions
- **GitHub Issues**: For bug reports and feature requests
- **DingTalk Group**: Join using ID `35924643`
- **Email**: [kubernetes-security@service.aliyun.com](mailto:kubernetes-security@service.aliyun.com) for security issues

### Code of Conduct

We follow the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md). Please be respectful and inclusive in all interactions.

## 📄 Legal

### License

By contributing to Terway, you agree that your contributions will be licensed under the [Apache License 2.0](https://github.com/AliyunContainerService/terway/blob/main/LICENSE).

### Developer Certificate of Origin (DCO)

All commits must be signed off to indicate you agree to the [Developer Certificate of Origin](https://developercertificate.org/):

```bash
git commit -s -m "your commit message"
```

## 🎯 Roadmap and Planning

### Current Priorities

1. **Performance improvements**: eBPF enhancements
2. **IPv6 support**: Full dual-stack implementation  
3. **Observability**: Better monitoring and debugging
4. **Security**: Enhanced network policies
5. **Documentation**: Comprehensive guides and examples

### Getting Involved

- **Check issues** labeled `good first issue` for beginner-friendly tasks
- **Join discussions** about roadmap planning
- **Propose features** that align with project goals
- **Help with testing** of new features

## 🆘 Getting Help

If you need help with contributing:

- **Read the docs**: Start with our [Development Setup](Development-Setup.md)
- **Ask questions**: Use GitHub Discussions or DingTalk
- **Join the community**: Participate in reviews and discussions
- **Be patient**: Maintainers will respond as soon as possible

## 🙏 Recognition

We appreciate all contributions! Contributors are recognized through:

- **GitHub contributor graph**
- **Release notes** mention significant contributions
- **Special thanks** in documentation
- **Community recognition** in DingTalk group

---

Thank you for making Terway better! Every contribution, no matter how small, makes a difference. 🚀