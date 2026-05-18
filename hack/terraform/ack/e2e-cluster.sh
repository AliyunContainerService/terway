#!/bin/bash
#
# ACK E2E cluster orchestration script
#
# Wraps `terraform apply/destroy` for the ACK E2E cluster and chains the
# `deploy-terway.sh` helper for profiles that include Terway.
#
# Usage:
#   ./e2e-cluster.sh create <profile> [extra args passed to deploy-terway.sh]
#   ./e2e-cluster.sh destroy
#   ./e2e-cluster.sh kubeconfig
#
# Profiles:
#   byo-ipv4      single stack ipv4, no terway (BYO CNI)
#   terway-ipv4   single stack ipv4 + terway-eniip + terway-controlplane
#   terway-dual   dual stack + terway-eniip + terway-controlplane
#
# The combination dual-stack + BYO is unsupported and will be rejected.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

show_help() {
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
    cat <<'EOF'

Examples:
  ./e2e-cluster.sh create byo-ipv4
  ./e2e-cluster.sh create terway-ipv4
  ./e2e-cluster.sh create terway-dual --tag mytag --registry my.registry/me
  ./e2e-cluster.sh destroy
  export KUBECONFIG=$(./e2e-cluster.sh kubeconfig)
EOF
}

# Map profile -> (ip_stack, deploy_terway). Rejects byo-dual explicitly.
profile_to_vars() {
    local profile="$1"
    case "$profile" in
        byo-ipv4)
            IP_STACK="ipv4"
            DEPLOY_TERWAY="false"
            ;;
        terway-ipv4)
            IP_STACK="ipv4"
            DEPLOY_TERWAY="true"
            ;;
        terway-dual)
            IP_STACK="dual"
            DEPLOY_TERWAY="true"
            ;;
        byo-dual)
            log_error "Profile 'byo-dual' is not supported: dual-stack + BYO CNI is an invalid combination."
            log_error "Use one of: byo-ipv4, terway-ipv4, terway-dual."
            exit 2
            ;;
        *)
            log_error "Unknown profile: '$profile'"
            log_error "Valid profiles: byo-ipv4, terway-ipv4, terway-dual"
            exit 2
            ;;
    esac
}

cmd_create() {
    if [[ $# -lt 1 ]]; then
        log_error "create requires a profile argument"
        log_error "Valid profiles: byo-ipv4, terway-ipv4, terway-dual"
        exit 2
    fi
    local profile="$1"
    shift

    profile_to_vars "$profile"

    log_info "Profile: ${profile} (ip_stack=${IP_STACK}, deploy_terway=${DEPLOY_TERWAY})"

    cd "${SCRIPT_DIR}"

    log_info "Running 'terraform init'..."
    terraform init

    log_info "Running 'terraform apply' for profile ${profile}..."
    terraform apply -auto-approve \
        -var="ip_stack=${IP_STACK}" \
        -var="deploy_terway=${DEPLOY_TERWAY}"

    if [[ "${DEPLOY_TERWAY}" == "true" ]]; then
        log_info "Profile installs Terway; invoking deploy-terway.sh --ip-stack ${IP_STACK} $*"
        "${SCRIPT_DIR}/deploy-terway.sh" --ip-stack "${IP_STACK}" "$@"
    else
        log_info "Profile is BYO; skipping Terway deployment."
        if [[ $# -gt 0 ]]; then
            log_warn "Ignoring extra args (BYO profile does not invoke deploy-terway.sh): $*"
        fi
    fi

    log_info "Cluster ready. Run './e2e-cluster.sh kubeconfig' to print the kubeconfig path."
}

cmd_destroy() {
    cd "${SCRIPT_DIR}"
    log_info "Running 'terraform destroy'..."
    terraform destroy -auto-approve
    log_info "Destroy completed."
}

cmd_kubeconfig() {
    cd "${SCRIPT_DIR}"
    local kc
    # shellcheck disable=SC2012
    kc=$(ls -t "${SCRIPT_DIR}"/kubeconfig-* 2>/dev/null | head -1 || true)
    if [[ -z "${kc}" ]]; then
        log_error "No kubeconfig-* file found in ${SCRIPT_DIR}"
        exit 1
    fi
    echo "${kc}"
}

main() {
    if [[ $# -eq 0 ]]; then
        show_help
        exit 0
    fi

    case "$1" in
        -h|--help|help)
            show_help
            exit 0
            ;;
        create)
            shift
            cmd_create "$@"
            ;;
        destroy)
            shift
            cmd_destroy "$@"
            ;;
        kubeconfig)
            shift
            cmd_kubeconfig "$@"
            ;;
        *)
            log_error "Unknown subcommand: '$1'"
            show_help
            exit 2
            ;;
    esac
}

main "$@"
