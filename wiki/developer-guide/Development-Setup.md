# Development Setup

This guide helps you set up a development environment for contributing to Terway.

## Prerequisites

### System Requirements

- **Operating System**: Linux (Ubuntu 20.04+ or CentOS 7+ recommended)
- **Architecture**: x86_64 or arm64
- **Memory**: 8GB+ RAM
- **Disk**: 20GB+ available space

### Required Tools

#### Go Development
- **Go**: 1.19+ (1.20+ recommended)
- **Git**: 2.20+
- **Make**: 4.0+

#### Container Tools
- **Docker**: 20.10+ or **Podman**: 3.0+
- **Docker Compose**: 1.29+ (optional)

#### Kubernetes Tools
- **kubectl**: 1.20+
- **kind** or **minikube**: For local cluster testing

## Environment Setup

### 1. Install Go

```bash
# Download and install Go 1.20
wget https://go.dev/dl/go1.20.6.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.20.6.linux-amd64.tar.gz

# Add to PATH
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
echo 'export GOPATH=$HOME/go' >> ~/.bashrc
echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc
source ~/.bashrc

# Verify installation
go version
```

### 2. Install Development Tools

```bash
# Install make
sudo apt-get update && sudo apt-get install -y make

# Install Docker
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
# Log out and back in for group changes to take effect

# Install kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl

# Install kind for local testing
go install sigs.k8s.io/kind@latest
```

### 3. Set Up Go Tools

```bash
# Install Go development tools
go install golang.org/x/tools/gopls@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/securecodewarrior/sast-scan@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
```

## Project Setup

### 1. Fork and Clone

```bash
# Fork the repository on GitHub first, then:
git clone https://github.com/YOUR_USERNAME/terway.git
cd terway

# Add upstream remote
git remote add upstream https://github.com/AliyunContainerService/terway.git

# Verify remotes
git remote -v
```

### 2. Build the Project

```bash
# Build all components
make build

# Build specific components
make build-terway      # Build terway binary
make build-daemon      # Build terway daemon
make build-cli         # Build terway CLI
```

### 3. Run Tests

```bash
# Run unit tests
make test

# Run unit tests with coverage
make test-coverage

# Run specific package tests
go test -v ./pkg/daemon/...
```

### 4. Linting and Formatting

```bash
# Run linter
make lint

# Format code
make fmt

# Run all checks
make check
```

## Development Workflow

### 1. Create Feature Branch

```bash
# Sync with upstream
git fetch upstream
git checkout main
git merge upstream/main

# Create feature branch
git checkout -b feature/your-feature-name
```

### 2. Make Changes

```bash
# Edit files with your preferred editor
vim pkg/daemon/daemon.go

# Build and test changes
make build
make test
make lint
```

### 3. Test Changes

```bash
# Run unit tests for your changes
go test -v ./pkg/daemon/...

# Build container image for testing
make docker-build

# Tag for local testing
docker tag registry.cn-hangzhou.aliyuncs.com/acs/terway:latest terway:dev
```

## Local Testing

### 1. Set Up Test Cluster

#### Using kind

```bash
# Create kind cluster configuration
cat << EOF > kind-config.yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
networking:
  disableDefaultCNI: true  # Disable default CNI for Terway testing
EOF

# Create cluster
kind create cluster --config kind-config.yaml --name terway-dev

# Verify cluster
kubectl cluster-info --context kind-terway-dev
```

#### Using minikube

```bash
# Start minikube without CNI
minikube start --cni=false --driver=docker

# Verify cluster
kubectl get nodes
```

### 2. Deploy Development Terway

```bash
# Build development image
make docker-build

# Load image into kind cluster
kind load docker-image terway:dev --name terway-dev

# Create test configuration
cat << EOF > terway-dev-config.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config
  namespace: kube-system
data:
  eni_conf: |
    {
      "version": "1",
      "access_key": "test-key",
      "access_secret": "test-secret",
      "security_group": "sg-test",
      "service_cidr": "10.96.0.0/12",
      "vswitches": {
        "test-zone": ["vsw-test"]
      }
    }
  10-terway.conf: |
    {
      "cniVersion": "0.4.0",
      "name": "terway",
      "type": "terway",
      "eniip_virtual_type": "IPVlan"
    }
EOF

# Apply configuration
kubectl apply -f terway-dev-config.yaml

# Deploy Terway with development image
sed 's|registry.cn-hangzhou.aliyuncs.com/acs/terway:latest|terway:dev|' terway.yml | kubectl apply -f -
```

### 3. Test Your Changes

```bash
# Check Terway pods
kubectl get pods -n kube-system | grep terway

# Check logs
kubectl logs -n kube-system -l app=terway -c terway

# Test pod creation
kubectl run test-pod --image=nginx
kubectl get pods -o wide
```

## Debugging

### 1. Debug Logs

```bash
# Enable debug logging
kubectl set env daemonset/terway -n kube-system LOG_LEVEL=debug

# Follow logs
kubectl logs -n kube-system -l app=terway -c terway -f
```

### 2. Use Delve Debugger

```bash
# Install delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Debug specific package
dlv test ./pkg/daemon/

# Debug running process (if running locally)
dlv attach $(pgrep terway)
```

### 3. Profile Performance

```bash
# Build with profiling enabled
go build -tags=profile -o terway-profile cmd/terway/terway.go

# Run with profiling
./terway-profile --enable-pprof=true --pprof-port=6060

# Access profiling data
go tool pprof http://localhost:6060/debug/pprof/profile
```

## IDE Setup

### Visual Studio Code

Install recommended extensions:

```json
{
  "recommendations": [
    "golang.go",
    "ms-vscode.vscode-go",
    "redhat.vscode-yaml",
    "ms-kubernetes-tools.vscode-kubernetes-tools"
  ]
}
```

Configure settings:

```json
{
  "go.lintTool": "golangci-lint",
  "go.lintFlags": ["--fast"],
  "go.useLanguageServer": true,
  "go.formatTool": "goimports",
  "go.testFlags": ["-v"],
  "go.buildFlags": ["-tags=debug"]
}
```

### GoLand/IntelliJ

1. Import project as Go module
2. Configure Go SDK path
3. Enable Go modules support
4. Configure linter: golangci-lint

## Common Development Tasks

### Adding New Features

```bash
# 1. Create feature branch
git checkout -b feature/new-feature

# 2. Add tests first (TDD approach)
echo 'func TestNewFeature(t *testing.T) { /* test code */ }' >> pkg/daemon/feature_test.go

# 3. Implement feature
vim pkg/daemon/feature.go

# 4. Run tests
go test -v ./pkg/daemon/

# 5. Build and test
make build test lint
```

### Fixing Bugs

```bash
# 1. Create bug fix branch
git checkout -b fix/bug-description

# 2. Add regression test
echo 'func TestBugFix(t *testing.T) { /* test code */ }' >> pkg/daemon/bug_test.go

# 3. Fix the bug
vim pkg/daemon/buggy_file.go

# 4. Verify fix
go test -v ./pkg/daemon/

# 5. Ensure all tests pass
make test
```

### Updating Dependencies

```bash
# Update specific dependency
go get -u github.com/example/dependency

# Update all dependencies
go get -u ./...

# Tidy modules
go mod tidy

# Verify changes
make test
```

## Performance Testing

### Benchmark Tests

```bash
# Run benchmarks
go test -bench=. ./pkg/daemon/

# Run specific benchmark
go test -bench=BenchmarkSpecificFunction ./pkg/daemon/

# Generate CPU profile
go test -bench=. -cpuprofile=cpu.prof ./pkg/daemon/

# Analyze profile
go tool pprof cpu.prof
```

### Memory Profiling

```bash
# Generate memory profile
go test -bench=. -memprofile=mem.prof ./pkg/daemon/

# Analyze memory usage
go tool pprof mem.prof
```

## Troubleshooting

### Common Issues

#### Build Failures
```bash
# Clean build cache
go clean -cache

# Update dependencies
go mod tidy

# Rebuild
make clean build
```

#### Test Failures
```bash
# Run tests with verbose output
go test -v ./...

# Run specific failing test
go test -v -run TestFailingTest ./pkg/daemon/

# Check for race conditions
go test -race ./...
```

#### Container Issues
```bash
# Rebuild container
make docker-build

# Check container logs
docker logs $(docker ps -q --filter ancestor=terway:dev)

# Debug container
docker run -it --entrypoint=/bin/bash terway:dev
```

## Contributing Guidelines

### Before Submitting PR

```bash
# 1. Ensure all tests pass
make test

# 2. Run linter
make lint

# 3. Format code
make fmt

# 4. Build successfully
make build

# 5. Update documentation if needed
# Edit relevant wiki pages

# 6. Commit with descriptive message
git commit -s -m "component: description of change

Longer explanation if needed.

Fixes #123"
```

### Code Review Process

1. **Self-review**: Review your own changes first
2. **Tests**: Ensure adequate test coverage
3. **Documentation**: Update docs for user-facing changes
4. **Backwards compatibility**: Avoid breaking changes
5. **Performance**: Consider performance implications

## Getting Help

### Development Questions

- 💬 **DingTalk Group**: `35924643`
- 📖 **GitHub Discussions**: For technical discussions
- 🐛 **GitHub Issues**: For bugs and feature requests

### Resources

- **Go Documentation**: https://golang.org/doc/
- **Kubernetes Development**: https://kubernetes.io/docs/contribute/
- **CNI Specification**: https://github.com/containernetworking/cni

## Next Steps

After setting up your development environment:

1. **Read the code**: Start with `cmd/terway/main.go`
2. **Understand architecture**: Review `pkg/daemon/` and `pkg/`
3. **Run tests**: Make sure everything works
4. **Pick an issue**: Look for `good first issue` labels
5. **Ask questions**: Don't hesitate to ask for help

Happy coding! 🚀