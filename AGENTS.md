# Terway Project Development Guide

## Code Generation & CRDs

- **Trigger**: When modifying Kubernetes API definitions, struct fields, or deepcopy logic.
- **Action**:
  - Run `make generate` to update `DeepCopy` methods.
  - Run `make manifests` to update CustomResourceDefinitions (CRDs).

## Testing Strategy

### 0. Repository Test Entry Points

- **Default verification**: Run `make test` on a Linux dev host for end-to-end validation.
  - `make test` includes datapath tests and `test-quick`; do not replace it with a hand-picked `go test` package list when final verification is required.
  - Local macOS runs are useful only for narrow feedback. Linux-only code, netlink/CNI paths, `gomonkey`, and envtest assets can behave differently on macOS.
- **Targeted feedback**: It is fine to run focused `go test ./path -run TestName` while iterating, but finish with the project Makefile target.

### 1. Interface Mocking

- **Scope**: Interfaces defined within `github.com/AliyunContainerService/terway`.
- **Tool**: Use `mockery` to generate mock implementations.

### 2. Low-Level Stubbing

- **Scope**: Hard-to-mock system interactions such as `os` calls, `netlink`, `grpc`, or file system operations.
- **Tool**: Use `gomonkey` to patch functions or methods inline.
- **Caveats**:
  - Keep gomonkey patches tightly scoped and verify the replacement function signature matches the original method/function exactly.
  - Avoid patching gRPC internals or constructing zero-value `grpc.ClientConn`/`grpc.Server`; prefer real in-memory servers such as `bufconn` or a temporary Unix socket.

### 3. Controller & K8s Interaction

- **Primary Framework**: Use `sigs.k8s.io/controller-runtime/pkg/envtest` to spin up a real API server environment.
  - **Why**: To ensure accurate behavior validation that `client/fake` cannot provide.
- **Discouraged**: Avoid `sigs.k8s.io/controller-runtime/pkg/client/fake` unless the logic is trivial and stateless.
- **Constraints (Crucial)**:
  - **Do NOT manually set** server-managed fields: `UID`, `ResourceVersion`, or `DeletionTimestamp`.
  - To set `DeletionTimestamp`, you must perform a client `Delete` operation on the object.

### 4. Test Migration From Internal Branches

- **Scope filtering**: When importing tests from internal branches, exclude scenarios for code that is not public in this repository, such as HDENI-only paths.
- **Behavior alignment**: Treat imported assertions as hypotheses. Reconcile them against the current open-source implementation before keeping them.
- **Determinism**: Do not write table cases that depend on Go map iteration order. Use one pod/resource per focused case, or make the expected outcome independent of iteration order.

## Verification & Quality Assurance

- **Linting**:
  - Strict adherence to `.golangci.yml`. Run `make lint-fix` for auto-fixes.
  - Run `make vet` to verify build tags.
- **Test Execution**:
  - Run `make test` for final validation on Linux.
  - Run `make test-quick` when datapath/kind validation is intentionally out of scope (outputs coverage to `coverage.txt`).

## Dependency Management

- **Trigger**: When adding/removing imports.
- **Action**: Run `go mod tidy && go mod vendor`. CI requires vendor files to be in sync.

## Pull Request Standards

- **Naming**: `[terway] <Title>` or `[component] <Title>`.
- **Checklist**: Format (`make fmt`) -> Lint (`make lint`) -> Test (`make test` or explicitly scoped `make test-quick`).
