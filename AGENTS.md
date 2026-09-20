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

## Policy Image Workflow

- **Trigger**: Changes to `deploy/images/policy/Dockerfile`, `policy/cilium/`, `policy/felix/`, or other policy image build inputs.
- **First commit — build inputs**: Commit the policy Dockerfile, patches, and any required build changes together. Keep unrelated changes out and preserve the existing patch format and ordering. Append a separately maintained fix as a new numbered patch instead of folding it into an earlier patch.
- **Build and publish**: Build from a clean checkout of that exact commit on a Linux build host. Use `make build-push-policy REGISTRY=<registry/namespace> BUILD_PLATFORMS=linux/amd64,linux/arm64` with the intended registry explicitly set. The image tag is `policy-<first-commit-short-sha>`; do not amend or rebase that commit after publishing without rebuilding and publishing under the new SHA.
- **Verify**: Confirm the published manifest includes both architectures and check the binaries' versions for each architecture. Run the required Linux tests and relevant datapath regression for runtime changes; distinguish emulated binary checks from tests on actual target nodes.
- **Second commit — consume the image**: After publishing and any required registry synchronization, update `TERWAY_POLICY_IMAGE` in both `deploy/images/terway/Dockerfile` and `deploy/images/terway-controlplane/Dockerfile`. Keep the tag and pin the multi-platform index digest read from the destination registry. Synchronization can change the index digest; verify the architecture-specific manifests still match the built image. Keep this commit limited to the image references and separate from the first commit.
- **Public repository hygiene**: Keep credentials, kubeconfigs, account/cluster/node identifiers, private repository URLs, personal registry addresses, SSH details, local paths, and raw diagnostic output out of tracked files, patch descriptions, and commit messages. Use placeholders in workflow examples and only public, project-approved image references in committed Dockerfiles. Review staged content before committing; retain private build and diagnostic artifacts outside the repository.

## Commit Message Standards

- Follow recent history for the affected component. For new commits, use `<type>(<scope>): <summary>` when a scope helps, or `<type>: <summary>` otherwise. Common types are `fix`, `feat`, `chore`, `docs`, `test`, and `build`; use concise scopes such as `policy` or `datapath`.
- Write a concise English imperative summary describing the actual change. Use `fix` for a behavior correction, `feat` for a new capability, and `chore` for an image reference update. Avoid vague subjects such as "update code" or explanations of the conversation.
- Policy examples: `fix(policy): avoid forced inlining in IPv6 socket LB` for the first commit and `chore: update Terway policy image to policy-<sha>` for the second. Documentation-only changes use `docs: ...`.
- Add a body when needed to explain the problem, resulting behavior, and relevant validation or limitations. Apply the public repository hygiene rules above to the entire message.
- The bracketed component prefix below is a PR title convention, not a required commit prefix. Do not rewrite existing commits solely to normalize style, especially commits already referenced by published image tags.

## Pull Request Standards

- **PR title**: `[terway] <Title>` or `[component] <Title>`.
- **Checklist**: Format (`make fmt`) -> Lint (`make lint`) -> Test (`make test` or explicitly scoped `make test-quick`).
