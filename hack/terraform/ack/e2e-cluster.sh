#!/bin/bash
#
# ACK E2E cluster orchestration script
#
# Creates one or more isolated ACK clusters from the same Terraform module by
# materializing a per-run workdir under ./runs/. Each workdir has its own
# .terraform/, tfstate, kubeconfig and terway-values.yaml, so multiple
# concurrent clusters never conflict.
#
# Usage:
#   ./e2e-cluster.sh create <profile> [--workdir <path>] [extra args -> deploy-terway.sh]
#   ./e2e-cluster.sh destroy <workdir>
#   ./e2e-cluster.sh kubeconfig [<workdir>]   # default: most recent run
#   ./e2e-cluster.sh list
#
# Profiles:
#   byo-ipv4      single stack ipv4, no terway addon; deploy terway via helm chart afterwards (BYO CNI)
#   byo-dual      dual stack, no terway addon; deploy terway via helm chart afterwards (BYO CNI)
#   ack-ipv4      single stack ipv4 + terway-eniip addon installed by ACK
#   ack-dual      dual stack + terway-eniip addon installed by ACK
#                 (terway-controlplane is hosted by Aliyun in managed clusters; only a placeholder
#                  service/terway-controlplane is visible to the user)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNS_DIR="${SCRIPT_DIR}/runs"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

show_help() {
    sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
    cat <<'EOF'

Examples:
  ./e2e-cluster.sh create byo-ipv4 --tag mytag --registry my.registry/me
  ./e2e-cluster.sh create ack-ipv4
  ./e2e-cluster.sh create ack-dual --workdir runs/ack-dual-pr1234
  ./e2e-cluster.sh list
  ./e2e-cluster.sh destroy runs/ack-ipv4-20260519-103015
  export KUBECONFIG=$(./e2e-cluster.sh kubeconfig)
EOF
}

# Map profile -> (ip_stack, cluster_mode, service_cidr). Rejects byo-dual explicitly.
profile_to_vars() {
    local profile="$1"
    case "$profile" in
        byo-ipv4)
            IP_STACK="ipv4"
            CLUSTER_MODE="byo"
            SERVICE_CIDR="192.168.0.0/16"
            ;;
        ack-ipv4)
            IP_STACK="ipv4"
            CLUSTER_MODE="ack"
            SERVICE_CIDR="192.168.0.0/16"
            ;;
        ack-dual)
            IP_STACK="dual"
            CLUSTER_MODE="ack"
            SERVICE_CIDR="192.168.0.0/16,fd00:1234::/112"
            ;;
        byo-dual)
            IP_STACK="dual"
            CLUSTER_MODE="byo"
            SERVICE_CIDR="192.168.0.0/16,fd00:1234::/112"
            ;;
        *)
            log_error "Unknown profile: '$profile'"
            log_error "Valid profiles: byo-ipv4, byo-dual, ack-ipv4, ack-dual"
            exit 2
            ;;
    esac
}

# Materialize a workdir: symlink .tf and helper scripts; copy tfvars (so
# operators can tweak per-run without affecting the source module).
materialize_workdir() {
    local workdir="$1"
    local profile="$2"

    mkdir -p "${workdir}"

    local f
    for f in "${SCRIPT_DIR}"/*.tf; do
        ln -sf "$f" "${workdir}/$(basename "$f")"
    done
    ln -sf "${SCRIPT_DIR}/deploy-terway.sh" "${workdir}/deploy-terway.sh"

    if [[ ! -f "${workdir}/terraform.tfvars" ]]; then
        cp "${SCRIPT_DIR}/terraform.tfvars" "${workdir}/terraform.tfvars"
    fi
    # Pin provider versions across workdirs (avoids drift; reuses cache).
    if [[ -f "${SCRIPT_DIR}/.terraform.lock.hcl" && ! -e "${workdir}/.terraform.lock.hcl" ]]; then
        cp "${SCRIPT_DIR}/.terraform.lock.hcl" "${workdir}/.terraform.lock.hcl"
    fi

    cat > "${workdir}/.run-meta" <<EOF
profile=${profile}
created=$(date -Iseconds 2>/dev/null || date)
script_dir=${SCRIPT_DIR}
EOF
}

cmd_create() {
    if [[ $# -lt 1 ]]; then
        log_error "create requires a profile argument"
        log_error "Valid profiles: byo-ipv4, byo-dual, ack-ipv4, ack-dual"
        exit 2
    fi
    local profile="$1"; shift

    local workdir=""
    local extra_args=()
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --workdir)
                workdir="$2"; shift 2 ;;
            --workdir=*)
                workdir="${1#--workdir=}"; shift ;;
            *)
                extra_args+=("$1"); shift ;;
        esac
    done

    profile_to_vars "$profile"

    if [[ -z "${workdir}" ]]; then
        local ts
        ts=$(date +%Y%m%d-%H%M%S)
        workdir="${RUNS_DIR}/${profile}-${ts}"
    fi
    # Resolve to absolute path
    mkdir -p "${workdir}"
    workdir="$(cd "${workdir}" && pwd)"

    log_info "Profile: ${profile} (ip_stack=${IP_STACK}, cluster_mode=${CLUSTER_MODE}, service_cidr=${SERVICE_CIDR})"
    log_info "Workdir: ${workdir}"

    materialize_workdir "${workdir}" "${profile}"

    # Export PROJECT_ROOT/CHART_PATH so deploy-terway.sh resolves the chart
    # against the real repo root, not against the workdir's parent path.
    export PROJECT_ROOT
    export CHART_PATH="${PROJECT_ROOT}/charts/terway"
    # Share provider downloads across workdirs to avoid repeated github fetches.
    export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-${HOME}/.terraform.d/plugin-cache}"
    mkdir -p "${TF_PLUGIN_CACHE_DIR}"

    cd "${workdir}"

    log_info "Running 'terraform init'..."
    terraform init

    # Use saved-plan workflow (plan -> apply tfplan) instead of -auto-approve
    # to satisfy CI / harness preview-then-apply requirements for shared cloud
    # infra. Behaviour is equivalent: the saved plan is applied without prompts.
    log_info "Running 'terraform plan' for profile ${profile}..."
    terraform plan -out=tfplan \
        -var="ip_stack=${IP_STACK}" \
        -var="cluster_mode=${CLUSTER_MODE}" \
        -var="service_cidr=${SERVICE_CIDR}"

    log_info "Running 'terraform apply' on saved plan..."
    terraform apply tfplan
    rm -f tfplan

    if [[ "${CLUSTER_MODE}" == "byo" ]]; then
        log_info "BYO profile; invoking deploy-terway.sh --ip-stack ${IP_STACK} ${extra_args[*]:-}"
        ./deploy-terway.sh --ip-stack "${IP_STACK}" "${extra_args[@]}"
    else
        log_info "ACK profile; terway-eniip is installed by ACK addon. Skipping helm deployment."
        if [[ ${#extra_args[@]} -gt 0 ]]; then
            log_warn "Ignoring extra args (ACK profile does not invoke deploy-terway.sh): ${extra_args[*]}"
        fi
    fi

    log_info "Cluster ready in ${workdir}"
    log_info "  export KUBECONFIG=\$(./e2e-cluster.sh kubeconfig ${workdir})"
}

cmd_destroy() {
    if [[ $# -lt 1 ]]; then
        log_error "destroy requires a workdir argument"
        log_error "Run './e2e-cluster.sh list' to see active workdirs."
        exit 2
    fi
    local workdir="$1"
    if [[ ! -d "${workdir}" ]]; then
        log_error "Workdir not found: ${workdir}"
        exit 1
    fi
    workdir="$(cd "${workdir}" && pwd)"

    log_info "Destroying ${workdir}..."
    cd "${workdir}"
    # Saved-plan destroy workflow (preview-then-apply pattern).
    terraform plan -destroy -out=tfdestroy
    terraform apply tfdestroy
    rm -f tfdestroy
    log_info "Destroy completed. Workdir kept at ${workdir} (remove manually if no longer needed)."
}

cmd_kubeconfig() {
    local workdir=""
    if [[ $# -ge 1 ]]; then
        workdir="$1"
    elif [[ -d "${RUNS_DIR}" ]]; then
        # default: pick most recently modified run
        # shellcheck disable=SC2012
        workdir=$(ls -dt "${RUNS_DIR}"/*/ 2>/dev/null | head -1 || true)
    fi
    if [[ -z "${workdir}" || ! -d "${workdir}" ]]; then
        log_error "No workdir specified or found under ${RUNS_DIR}"
        exit 1
    fi
    local kc
    # shellcheck disable=SC2012
    kc=$(ls -t "${workdir}"/kubeconfig-* 2>/dev/null | head -1 || true)
    if [[ -z "${kc}" ]]; then
        log_error "No kubeconfig-* file found in ${workdir}"
        exit 1
    fi
    echo "${kc}"
}

cmd_list() {
    if [[ ! -d "${RUNS_DIR}" ]]; then
        log_info "No runs yet (${RUNS_DIR} does not exist)."
        return
    fi
    local d
    local found=0
    printf "%-12s  %-25s  %s\n" "PROFILE" "CREATED" "WORKDIR"
    for d in "${RUNS_DIR}"/*/; do
        [[ -d "$d" ]] || continue
        found=1
        local p="?" c="?"
        if [[ -f "$d/.run-meta" ]]; then
            p=$(grep '^profile=' "$d/.run-meta" | cut -d= -f2-)
            c=$(grep '^created=' "$d/.run-meta" | cut -d= -f2-)
        fi
        printf "%-12s  %-25s  %s\n" "$p" "$c" "${d%/}"
    done
    if [[ "${found}" -eq 0 ]]; then
        log_info "No runs under ${RUNS_DIR}."
    fi
}

main() {
    if [[ $# -eq 0 ]]; then
        show_help
        exit 0
    fi

    case "$1" in
        -h|--help|help)
            show_help; exit 0 ;;
        create)
            shift; cmd_create "$@" ;;
        destroy)
            shift; cmd_destroy "$@" ;;
        kubeconfig)
            shift; cmd_kubeconfig "$@" ;;
        list)
            shift; cmd_list "$@" ;;
        *)
            log_error "Unknown subcommand: '$1'"
            show_help
            exit 2 ;;
    esac
}

main "$@"
