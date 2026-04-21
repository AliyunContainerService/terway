#!/bin/bash
#
# Terway E2E Testing Script
# Automates: cluster creation → build images → deploy terway → run e2e tests
#
# Usage:
#   ./terway-e2e.sh [command] [options]
#
# Commands:
#   build       - Build and push terway images (uses make build-push)
#   deploy      - Deploy terway to existing cluster
#   test        - Run e2e tests
#   full        - Full workflow (build + deploy + test)
#   clean       - Clean up resources
#   status      - Show cluster and deployment status
#
# Options:
#   -v, --version     Version tag (required)
#   -r, --registry    Registry URL (default: registry.cn-hangzhou.aliyuncs.com/acs)
#   --platform        Build platforms (default: linux/amd64)
#
# Examples:
#   ./terway-e2e.sh build -v v1.17.0
#   ./terway-e2e.sh build -v v1.17.0 -r myregistry.com/myrepo
#   ./terway-e2e.sh deploy -v v1.17.0
#   ./terway-e2e.sh test -v v1.17.0
#   ./terway-e2e.sh full -v v1.17.0
#

set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TERRAFORM_DIR="${SCRIPT_DIR}/terraform/ack"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Default values
VERSION=""
REGISTRY="registry.cn-hangzhou.aliyuncs.com/acs"
KUBECONFIG_FILE=""
GIT_COMMIT_SHORT=""
PULL_POLICY="Always"
TEST_TIMEOUT="60m"
SKIP_BUILD=false
SKIP_DEPLOY=false
BUILD_PLATFORMS="linux/amd64"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_debug() { echo -e "${BLUE}[DEBUG]${NC} $1"; }

# Show help
show_help() {
    head -30 "${BASH_SOURCE[0]}" | grep -E "^#|^# "
}

# Parse arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -v|--version)
                VERSION="$2"
                shift 2
                ;;
            -r|--registry)
                REGISTRY="$2"
                shift 2
                ;;
            -p|--pull-policy)
                PULL_POLICY="$2"
                shift 2
                ;;
            -k|--kubeconfig)
                KUBECONFIG_FILE="$2"
                shift 2
                ;;
            --skip-build)
                SKIP_BUILD=true
                shift
                ;;
            --skip-deploy)
                SKIP_DEPLOY=true
                shift
                ;;
            -t|--timeout)
                TEST_TIMEOUT="$2"
                shift 2
                ;;
            --platform)
                BUILD_PLATFORMS="$2"
                shift 2
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            -*)
                log_error "Unknown option: $1"
                show_help
                exit 1
                ;;
            *)
                COMMAND="$1"
                shift
                ;;
        esac
    done

    if [[ -z "$COMMAND" ]]; then
        log_error "No command specified"
        show_help
        exit 1
    fi

    if [[ -z "$VERSION" ]]; then
        log_error "Version (-v) is required"
        exit 1
    fi

    # Generate git commit short for image tag
    GIT_COMMIT_SHORT=$(cd "${PROJECT_ROOT}" && git rev-parse --short=8 HEAD 2>/dev/null || echo "local")

    log_info "Configuration:"
    echo "  Command: ${COMMAND}"
    echo "  Version: ${VERSION}"
    echo "  Git Commit: ${GIT_COMMIT_SHORT}"
    echo "  Image Tag: ${VERSION}-${GIT_COMMIT_SHORT}"
    echo "  Registry: ${REGISTRY}"
    echo "  Pull Policy: ${PULL_POLICY}"
    echo "  Build Platforms: ${BUILD_PLATFORMS}"
}

# Find kubeconfig
find_kubeconfig() {
    if [[ -n "${KUBECONFIG_FILE}" ]] && [[ -f "${KUBECONFIG_FILE}" ]]; then
        echo "${KUBECONFIG_FILE}"
        return 0
    fi

    # Try default locations
    local kubeconfig=""
    kubeconfig=$(find "${TERRAFORM_DIR}" -maxdepth 1 -name 'kubeconfig-*' -printf '%T@ %p\n' 2>/dev/null | sort -rn | head -1 | cut -d' ' -f2- || true)

    if [[ -z "${kubeconfig}" ]]; then
        kubeconfig="${HOME}/.kube/config"
        if [[ ! -f "${kubeconfig}" ]]; then
            log_error "Kubeconfig not found"
            return 1
        fi
    fi

    echo "${kubeconfig}"
}

# Check dependencies
check_dependencies() {
    log_info "Checking dependencies..."

    local missing=()

    for cmd in docker kubectl helm jq git go; do
        if ! command -v $cmd &> /dev/null; then
            missing+=("$cmd")
        fi
    done

    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing dependencies: ${missing[*]}"
        return 1
    fi

    log_info "All dependencies available"
}

# Build and push terway images using make
build_images() {
    if $SKIP_BUILD; then
        log_info "Skipping build (--skip-build)"
        return 0
    fi

    local image_tag="${VERSION}-${GIT_COMMIT_SHORT}"
    log_info "Building and pushing terway images..."
    log_info "Image tag: ${image_tag}"
    log_info "Registry: ${REGISTRY}"
    log_info "Platforms: ${BUILD_PLATFORMS}"

    cd "${PROJECT_ROOT}"

    # Use make build-push with custom variables
    # This reuses the Makefile's build logic which handles:
    # - docker buildx build with --push
    # - GIT_VERSION build arg
    # - multi-platform builds
    log_info "Running: make build-push REGISTRY=${REGISTRY} GIT_COMMIT_SHORT=${image_tag} BUILD_PLATFORMS=${BUILD_PLATFORMS}"
    make build-push \
        REGISTRY="${REGISTRY}" \
        GIT_COMMIT_SHORT="${image_tag}" \
        BUILD_PLATFORMS="${BUILD_PLATFORMS}"

    log_info "Images built and pushed successfully:"
    log_info "  - ${REGISTRY}/terway:${image_tag}"
    log_info "  - ${REGISTRY}/terway-controlplane:${image_tag}"
}

# Deploy terway
deploy_terway() {
    if $SKIP_DEPLOY; then
        log_info "Skipping deploy (--skip-deploy)"
        return 0
    fi

    log_info "Deploying terway..."

    KUBECONFIG_FILE=$(find_kubeconfig) || exit 1
    log_info "Using kubeconfig: ${KUBECONFIG_FILE}"

    cd "${TERRAFORM_DIR}"
    ./deploy-terway.sh \
        --tag "${VERSION}-${GIT_COMMIT_SHORT}" \
        --registry "${REGISTRY}" \
        --pull-policy "${PULL_POLICY}"

    # Wait for pods
    log_info "Waiting for terway pods to be ready..."
    local max_wait=300
    local waited=0

    while true; do
        # Count running terway pods (label is app=terway-eniip from helm chart)
        local ready
        ready=$(kubectl --kubeconfig "${KUBECONFIG_FILE}" get pods -n kube-system -l app=terway-eniip --no-headers 2>/dev/null | awk '/Running/ {count++} END {print count+0}')
        local total
        total=$(kubectl --kubeconfig "${KUBECONFIG_FILE}" get pods -n kube-system -l app=terway-eniip --no-headers 2>/dev/null | awk 'END {print NR+0}')

        if [[ "$ready" -ge "$total" ]] && [[ "$total" -gt 0 ]]; then
            log_info "All terway pods are running ($ready/$total)"
            break
        fi

        if [[ $waited -ge $max_wait ]]; then
            log_warn "Timeout waiting for pods, but continuing..."
            break
        fi

        echo -ne "\r  Ready: ${ready}/${total}, waited: ${waited}s   "
        sleep 5
        waited=$((waited + 5))
    done
    echo ""

    # Show status
    log_info "Terway deployment status:"
    kubectl --kubeconfig "${KUBECONFIG_FILE}" get pods -n kube-system | grep terway || true
}

# Run e2e tests
run_tests() {
    log_info "Running e2e tests..."
    log_info "Timeout: ${TEST_TIMEOUT}"

    KUBECONFIG_FILE=$(find_kubeconfig) || exit 1

    # Setup kubeconfig for tests
    mkdir -p "${HOME}/.kube"
    cp "${KUBECONFIG_FILE}" "${HOME}/.kube/config"

    cd "${PROJECT_ROOT}"

    # Run e2e tests
    go test -v -count=1 -timeout "${TEST_TIMEOUT}" -tags e2e ./tests -run 'Test[^UM].*' "$@"
}

# Show status
show_status() {
    log_info "=== Terway E2E Testing Status ==="

    KUBECONFIG_FILE=$(find_kubeconfig) || {
        log_warn "No cluster found"
        return 1
    }

    log_info "Using kubeconfig: ${KUBECONFIG_FILE}"

    echo ""
    log_info "Cluster nodes:"
    kubectl --kubeconfig "${KUBECONFIG_FILE}" get nodes -o wide 2>/dev/null || log_warn "Cannot get nodes"

    echo ""
    log_info "Terway pods:"
    kubectl --kubeconfig "${KUBECONFIG_FILE}" get pods -n kube-system 2>/dev/null | grep terway || log_warn "No terway pods found"

    echo ""
    log_info "Terway version:"
    kubectl --kubeconfig "${KUBECONFIG_FILE}" get ds terway-eniip -n kube-system -o jsonpath='{.spec.template.spec.initContainers[*].image}' 2>/dev/null || true

    echo ""
    log_info "Helm releases:"
    helm list -n kube-system 2>/dev/null | grep terway || true
}

# Clean up
cleanup() {
    log_warn "Cleanup is destructive! This will delete:"
    echo "  - Test namespaces"
    echo "  - Helm release (optional)"

    read -p "Continue? (y/N) " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Cancelled"
        return 0
    fi

    KUBECONFIG_FILE=$(find_kubeconfig) || exit 1

    # Delete test namespaces
    log_info "Deleting test namespaces..."
    kubectl --kubeconfig "${KUBECONFIG_FILE}" delete ns -l "k8s.aliyun.com/terway-e2e=true" 2>/dev/null || true

    # Optionally delete helm release
    read -p "Delete helm release 'terway'? (y/N) " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        log_info "Deleting helm release..."
        helm delete terway -n kube-system 2>/dev/null || true
    fi

    log_info "Cleanup completed"
}

# Main function
main() {
    parse_args "$@"

    check_dependencies || exit 1

    case "$COMMAND" in
        build)
            build_images
            ;;
        deploy)
            deploy_terway
            ;;
        test)
            run_tests "${@}"
            ;;
        full)
            log_info "=== Full Workflow: Build → Deploy → Test ==="
            build_images
            deploy_terway
            run_tests
            ;;
        clean)
            cleanup
            ;;
        status)
            show_status
            ;;
        *)
            log_error "Unknown command: $COMMAND"
            show_help
            exit 1
            ;;
    esac
}

main "$@"
