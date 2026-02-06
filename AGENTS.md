# Terway Project Development Guide

## Code Generation & CRDs

- **Trigger**: When modifying Kubernetes API definitions, struct fields, or deepcopy logic.
- **Action**:
  - Run `make generate` to update `DeepCopy` methods.
  - Run `make manifests` to update CustomResourceDefinitions (CRDs).

## Testing Strategy

### 1. Interface Mocking

- **Scope**: Interfaces defined within `github.com/AliyunContainerService/terway`.
- **Tool**: Use `mockery` to generate mock implementations.

### 2. Low-Level Stubbing

- **Scope**: Hard-to-mock system interactions such as `os` calls, `netlink`, `grpc`, or file system operations.
- **Tool**: Use `gomonkey` to patch functions or methods inline.

### 3. Controller & K8s Interaction

- **Primary Framework**: Use `sigs.k8s.io/controller-runtime/pkg/envtest` to spin up a real API server environment.
  - **Why**: To ensure accurate behavior validation that `client/fake` cannot provide.
- **Discouraged**: Avoid `sigs.k8s.io/controller-runtime/pkg/client/fake` unless the logic is trivial and stateless.
- **Constraints (Crucial)**:
  - **Do NOT manually set** server-managed fields: `UID`, `ResourceVersion`, or `DeletionTimestamp`.
  - To set `DeletionTimestamp`, you must perform a client `Delete` operation on the object.

## Verification & Quality Assurance

- **Linting**:
  - Strict adherence to `.golangci.yml`. Run `make lint-fix` for auto-fixes.
  - Run `make vet` to verify build tags.
- **Test Execution**:
  - Run `make test-quick` for the standard suite (outputs test report and coverage to `coverage.txt`).

## Dependency Management

- **Trigger**: When adding/removing imports.
- **Action**: Run `go mod tidy && go mod vendor`. CI requires vendor files to be in sync.

## Pull Request Standards

- **Naming**: `[terway] <Title>` or `[component] <Title>`.
- **Checklist**: Format (`make fmt`) -> Lint (`make lint`) -> Test (`make quick-test`).
