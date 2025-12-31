# Terway Project Development Guide

## Dev environment tips

- Use `make help` to see all available make targets and their descriptions
- Run `make generate` to generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations
- Use `make manifests` to generate CustomResourceDefinition objects
- Run `make fmt` to format Go code with `go fmt`
- Use `make vet` to run `go vet` against code with build tags

## Testing instructions

- Find the CI plan in the `.github/workflows` folder (check.yml, build.yml)
- Run `make quick-test` to run all tests
- Run `make lint` to run golangci-lint with the configuration in `.golangci.yml`
- Run `make lint-fix` to run golangci-lint and perform automatic fixes
- Fix any test or lint errors until the whole suite passes
- After moving files or changing imports, run `make fmt` and `make vet` to ensure code quality
- Add or update tests for the code you change, even if nobody asked

## PR instructions

- Title format: `[terway] <Title>` or `[component] <Title>`
- Always run `make lint` and `make test` before committing
- Ensure `go mod tidy` and `go mod vendor` are run before submitting (checked in CI)
- Make sure all build tags are properly set (privileged, default_build)
- Verify that your changes pass the GitHub Actions workflows:
  - Code formatting and linting (golangci-lint)
  - Unit tests with coverage
  - Module vendoring checks
  - Super-linter for markdown and bash files
