#!/bin/bash
#
# Terway 升级兼容性测试编排脚本
#
# 用法:
#   ./run-upgrade-test.sh --path 1.9.18 1.17.6 --config B
#   ./run-upgrade-test.sh --path 1.7.4 1.9.18 1.17.6 --config A
#   ./run-upgrade-test.sh --all
#
# 前提: 已创建 ACK BYO 集群 (make ack-cluster-byo-ipv4) 且 KUBECONFIG 已设置
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TEMPLATE_DIR="${SCRIPT_DIR}/templates"
ACK_DIR="${PROJECT_ROOT}/hack/terraform/ack"

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
log_step()  { echo -e "${BLUE}[STEP]${NC}  $*"; }

# Extract minor version (e.g. "1.7.4" -> "1.7") so template folders use the
# minor-version convention (v1.7, v1.9, v1.17) while templates inside still
# carry the full patch version.
minor_version() {
    local v="$1"
    echo "${v%.*}"
}

# 默认值
REGION="cn-hangzhou"
DRY_RUN=false
WORKDIR=""

# 集群参数 (从 terraform state 提取或通过 flag 指定)
POD_VSWITCH_ID=""
CLUSTER_ID=""
SERVICE_CIDR=""
SECURITY_GROUP_ID=""
VPC_ID=""

# 解析参数
PATH_VERSIONS=()
CONFIG=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --path)
            shift
            while [[ $# -gt 0 && ! "$1" =~ ^-- ]]; do
                PATH_VERSIONS+=("$1")
                shift
            done
            ;;
        --config)
            CONFIG="$2"; shift 2 ;;
        --all)
            ALL=true; shift ;;
        --region)
            REGION="$2"; shift 2 ;;
        --workdir)
            WORKDIR="$2"; shift 2 ;;
        --pod-vswitch-id)
            POD_VSWITCH_ID="$2"; shift 2 ;;
        --cluster-id)
            CLUSTER_ID="$2"; shift 2 ;;
        --service-cidr)
            SERVICE_CIDR="$2"; shift 2 ;;
        --security-group-id)
            SECURITY_GROUP_ID="$2"; shift 2 ;;
        --vpc-id)
            VPC_ID="$2"; shift 2 ;;
        --dry-run)
            DRY_RUN=true; shift ;;
        -h|--help)
            sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"; exit 1 ;;
    esac
done

# 配置模式映射
get_config_flags() {
    local cfg="$1"
    case "$cfg" in
        A) echo "--datapath --disable-np false" ;;
        B) echo "--disable-np false" ;;
        C) echo "--disable-np true" ;;
        *) log_error "Invalid config: $cfg (use A, B, or C)"; exit 1 ;;
    esac
}

# 从 terraform state 提取集群参数
extract_cluster_params() {
    # Resolve KUBECONFIG to absolute path (script may cd to different directories)
    if [[ -n "${KUBECONFIG}" && ! "${KUBECONFIG}" = /* ]]; then
        export KUBECONFIG="${PWD}/${KUBECONFIG}"
    fi

    log_step "Extracting cluster params..."

    if [[ -z "${WORKDIR}" ]]; then
        WORKDIR=$(ls -dt "${ACK_DIR}"/runs/*/ 2>/dev/null | head -1 || true)
        if [[ -z "${WORKDIR}" ]]; then
            # 尝试主目录
            WORKDIR="${ACK_DIR}"
        fi
    fi

    local tfstate="${WORKDIR}/terraform.tfstate"

    if [[ -f "${tfstate}" ]]; then
        log_info "Reading from terraform state: ${tfstate}"

        if [[ -z "${CLUSTER_ID}" ]]; then
            CLUSTER_ID=$(jq -r '.resources[] | select(.type=="alicloud_cs_managed_kubernetes" and .name=="default") | .instances[0].attributes.id' "${tfstate}" 2>/dev/null || echo "")
        fi
        if [[ -z "${SECURITY_GROUP_ID}" ]]; then
            SECURITY_GROUP_ID=$(jq -r '.resources[] | select(.type=="alicloud_cs_managed_kubernetes" and .name=="default") | .instances[0].attributes.security_group_id' "${tfstate}" 2>/dev/null || echo "")
        fi
        if [[ -z "${VPC_ID}" ]]; then
            VPC_ID=$(jq -r '.resources[] | select(.type=="alicloud_vpc" and .name=="default") | .instances[0].attributes.id' "${tfstate}" 2>/dev/null || echo "")
        fi
        if [[ -z "${SERVICE_CIDR}" ]]; then
            SERVICE_CIDR=$(terraform -chdir="${WORKDIR}" output -raw service_cidr 2>/dev/null || echo "192.168.0.0/16")
        fi
        if [[ -z "${POD_VSWITCH_ID}" ]]; then
            # vswitches must be a map of zone→[vswitchIDs], not a flat array
            POD_VSWITCH_ID=$(jq -c -r -s 'add' < <(jq -c '.resources[] | select(.type=="alicloud_vswitch" and .name=="terway_vswitches") | .instances[] | {(.attributes.zone_id // .attributes.availability_zone): [.attributes.id]}' "${tfstate}" 2>/dev/null) 2>/dev/null || echo "")
        fi
    fi

    # 检查必要参数
    local missing=()
    [[ -z "${CLUSTER_ID}" ]] && missing+=("--cluster-id")
    [[ -z "${SECURITY_GROUP_ID}" ]] && missing+=("--security-group-id")
    [[ -z "${POD_VSWITCH_ID}" ]] && missing+=("--pod-vswitch-id")
    [[ -z "${VPC_ID}" ]] && missing+=("--vpc-id")

    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required params: ${missing[*]}"
        log_error "Provide via flags or ensure terraform state exists at: ${tfstate}"
        exit 1
    fi

    [[ -z "${SERVICE_CIDR}" ]] && SERVICE_CIDR="192.168.0.0/16"

    log_info "Cluster ID: ${CLUSTER_ID}"
    log_info "Security Group: ${SECURITY_GROUP_ID}"
    log_info "VPC ID: ${VPC_ID}"
    log_info "Service CIDR: ${SERVICE_CIDR}"
    log_info "Region: ${REGION}"
    log_info "Pod VSwitch IDs: ${POD_VSWITCH_ID}"
}

# 清理 terway 资源
cleanup_terway() {
    log_step "Cleaning up previous terway deployment..."

    if [[ "${DRY_RUN}" == "true" ]]; then
        log_info "[DRY-RUN] Would remove the previous Terway Kubernetes resources"
        return
    fi

    # Delete test workloads first while Terway can still release their ENIs.
    if kubectl get namespace terway-upgrade-test >/dev/null 2>&1; then
        kubectl delete pod,service --all -n terway-upgrade-test --ignore-not-found=true --timeout=3m || return 1
        kubectl wait --for=delete pod --all -n terway-upgrade-test --timeout=3m || return 1
        kubectl delete namespace terway-upgrade-test --wait=false || return 1
    fi

    # Uninstall helm release if one was left by cluster provisioning.
    if helm status terway -n kube-system >/dev/null 2>&1; then
        helm uninstall terway -n kube-system
    fi

    kubectl delete daemonset terway-eniip -n kube-system --ignore-not-found=true --timeout=3m || return 1
    kubectl delete deployment terway-controlplane -n kube-system --ignore-not-found=true --timeout=3m || return 1
    kubectl delete configmap eni-config -n kube-system --ignore-not-found=true || return 1
    kubectl delete configmap terway-controlplane -n kube-system --ignore-not-found=true || return 1
    kubectl delete job terway-preflight -n kube-system --ignore-not-found=true || return 1
    kubectl delete serviceaccount terway -n kube-system --ignore-not-found=true || return 1
    kubectl delete clusterrolebinding terway-binding --ignore-not-found=true || return 1
    kubectl delete clusterrole terway-pod-reader --ignore-not-found=true || return 1
    kubectl delete rolebinding terway-binding -n kube-system --ignore-not-found=true || return 1
    kubectl delete role terway -n kube-system --ignore-not-found=true || return 1

    # 删除 Calico CRDs
    local crds=(
        felixconfigurations.crd.projectcalico.org
        bgpconfigurations.crd.projectcalico.org
        ippools.crd.projectcalico.org
        hostendpoints.crd.projectcalico.org
        clusterinformations.crd.projectcalico.org
        globalnetworkpolicies.crd.projectcalico.org
        globalnetworksets.crd.projectcalico.org
        networkpolicies.crd.projectcalico.org
    )
    for crd in "${crds[@]}"; do
        kubectl delete crd "${crd}" --ignore-not-found=true || return 1
    done

    # 删除 Cilium CRDs
    local cilium_crds=(
        ciliumclusterwidenetworkpolicies.cilium.io
        ciliumendpoints.cilium.io
        ciliumendpointslices.cilium.io
        ciliumexternalworkloads.cilium.io
        ciliumidentities.cilium.io
        ciliumnetworkpolicies.cilium.io
        ciliumnodes.cilium.io
        ciliumpodippools.cilium.io
        ciliumloadbalancerippools.cilium.io
        ciliuml2announcementpolicies.cilium.io
        ciliumcidrgroups.cilium.io
    )
    for crd in "${cilium_crds[@]}"; do
        kubectl delete crd "${crd}" --ignore-not-found=true || return 1
    done

    # 删除 controlplane credential secret
    kubectl delete secret terway-controlplane-credential -n kube-system --ignore-not-found=true || return 1

    # Once the controller has stopped, it can no longer re-add PodENI
    # finalizers. Remove only finalizers left in the deleting test namespace.
    if kubectl get namespace terway-upgrade-test >/dev/null 2>&1; then
        local podeni
        while IFS= read -r podeni; do
            [[ -z "${podeni}" ]] && continue
            kubectl patch "${podeni}" -n terway-upgrade-test --type=merge \
                -p '{"metadata":{"finalizers":[]}}' || return 1
        done < <(kubectl get podeni.network.alibabacloud.com -n terway-upgrade-test -o name 2>/dev/null || true)
        kubectl wait --for=delete namespace/terway-upgrade-test --timeout=3m || return 1
    fi

    # 等待 terway pods 消失
    kubectl wait --for=delete pod -l app=terway-eniip -n kube-system --timeout=3m 2>/dev/null || \
        [[ -z "$(kubectl get pod -n kube-system -l app=terway-eniip -o name)" ]]

    log_info "Cleanup complete"
}

# 重启 worker 节点
reboot_nodes() {
    log_step "Rebooting worker nodes..."

    # 获取 worker 节点列表 (排除 master)
    local nodes
    nodes=$(kubectl get nodes -l node-role.kubernetes.io/control-plane!=true -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

    if [[ -z "${nodes}" ]]; then
        # 如果没有 control-plane label, 尝试获取所有节点
        nodes=$(kubectl get nodes -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
    fi

    if [[ -z "${nodes}" ]]; then
        log_warn "No nodes found, skipping reboot"
        return
    fi

    log_info "Nodes to reboot: ${nodes}"

    if [[ "${DRY_RUN}" == "true" ]]; then
        log_info "[DRY-RUN] Would reboot: ${nodes}"
        return
    fi

    command -v aliyun >/dev/null 2>&1 || {
        log_error "aliyun CLI is required for a real node reboot"
        return 1
    }

    # Reboot sequentially and require both a changed boot ID and Ready=True.
    for node in ${nodes}; do
        local provider_id instance_id old_boot_id deadline new_boot_id ready
        provider_id=$(kubectl get node "${node}" -o jsonpath='{.spec.providerID}')
        instance_id=$(echo "${provider_id}" | grep -oE 'i-[a-z0-9]+')
        old_boot_id=$(kubectl get node "${node}" -o jsonpath='{.status.nodeInfo.bootID}')
        if [[ -z "${instance_id}" || -z "${old_boot_id}" ]]; then
            log_error "Cannot resolve instance or boot ID for ${node}"
            return 1
        fi

        log_info "Rebooting ${node} (instance: ${instance_id}, bootID: ${old_boot_id})"
        aliyun ecs RebootInstance --InstanceId "${instance_id}" --region "${REGION}" >/dev/null
        deadline=$((SECONDS + 600))
        while (( SECONDS < deadline )); do
            new_boot_id=$(kubectl get node "${node}" -o jsonpath='{.status.nodeInfo.bootID}' 2>/dev/null || true)
            ready=$(kubectl get node "${node}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
            if [[ -n "${new_boot_id}" && "${new_boot_id}" != "${old_boot_id}" && "${ready}" == "True" ]]; then
                log_info "Node ${node} reboot verified (bootID: ${new_boot_id})"
                break
            fi
            sleep 5
        done
        if [[ "${new_boot_id:-}" == "${old_boot_id}" || "${ready:-}" != "True" ]]; then
            log_error "Node ${node} did not complete a verified reboot within 10m"
            return 1
        fi
    done

    log_info "All nodes ready"
}

validate_version_template() {
    local version="$1"
    local version_dir="$2"
    local manifest="${version_dir}/terway-eniip.yaml"

    [[ -f "${manifest}" ]] || {
        log_error "Missing version manifest: ${manifest}"
        return 1
    }
    local unexpected
    unexpected=$(grep -E 'image: .*/(terway|terway-controlplane):' "${manifest}" | grep -v ":v${version}$" || true)
    if [[ -n "${unexpected}" ]]; then
        log_error "Version manifest contains a mismatched Terway image tag:"
        echo "${unexpected}" >&2
        return 1
    fi
}

# 部署初始版本
deploy_version() {
    local version="$1"
    local config="$2"

    log_step "Deploying terway v${version} (config: ${config})..."

    local config_flags
    config_flags=$(get_config_flags "${config}")
    local version_dir
    version_dir="${TEMPLATE_DIR}/v$(minor_version "${version}")"
    validate_version_template "${version}" "${version_dir}"

    if [[ "${DRY_RUN}" == "true" ]]; then
        log_info "[DRY-RUN] Would render and apply configmap + terway-eniip.yaml for v${version}"
        return
    fi

    # 1. 渲染并 apply ConfigMap
    log_info "Rendering ConfigMap..."
    go run "${SCRIPT_DIR}/render-configmap.go" \
        --template "${version_dir}/configmap.yaml.tmpl" \
        ${config_flags} \
        --pod-vswitch-id "${POD_VSWITCH_ID}" \
        --cluster-id "${CLUSTER_ID}" \
        --service-cidr "${SERVICE_CIDR}" \
        --security-group-id "${SECURITY_GROUP_ID}" \
        --ip-stack ipv4 \
        --region-id "${REGION}" \
        --vpc-id "${VPC_ID}" | kubectl apply -f -

    # 2. Apply 静态 YAML (SA, RBAC, DS, Job, CRDs)
    log_info "Applying terway-eniip.yaml..."
    kubectl apply -f "${version_dir}/terway-eniip.yaml"

    # 3. 等待 terway pods ready
    log_info "Waiting for terway pods to be ready..."
    kubectl wait pod -l app=terway-eniip --for=condition=ready -n kube-system --timeout=5m 2>/dev/null || {
        log_error "Terway pods not ready after 5m"
        kubectl get pods -n kube-system -l app=terway-eniip -o wide
        return 1
    }

    kubectl rollout status deployment/terway-controlplane -n kube-system --timeout=5m || {
        log_error "Terway controlplane rollout failed"
        kubectl get pods -n kube-system -l k8s-app=terway-controlplane -o wide
        return 1
    }
    kubectl wait pod -l k8s-app=terway-controlplane --for=condition=ready -n kube-system --timeout=3m || {
        log_error "Terway controlplane pods not ready after 3m"
        kubectl get pods -n kube-system -l k8s-app=terway-controlplane -o wide
        return 1
    }

    log_info "Terway v${version} deployed successfully"
}

# 升级版本 (仅 apply terway-eniip.yaml, 不碰 ConfigMap)
upgrade_version() {
    local version="$1"
    local version_dir
    version_dir="${TEMPLATE_DIR}/v$(minor_version "${version}")"
    validate_version_template "${version}" "${version_dir}"

    log_step "Upgrading to terway v${version} (ConfigMap unchanged)..."

    if [[ "${DRY_RUN}" == "true" ]]; then
        log_info "[DRY-RUN] Would apply terway-eniip.yaml for v${version}"
        return
    fi

    # 只 apply 目标版本的 terway-eniip.yaml (不碰 ConfigMap!)
    kubectl apply -f "${version_dir}/terway-eniip.yaml"

    # 等待滚动更新完成
    log_info "Waiting for DaemonSet rollout..."
    kubectl rollout status daemonset/terway-eniip -n kube-system --timeout=10m || {
        log_error "DaemonSet rollout failed"
        kubectl get pods -n kube-system -l app=terway-eniip -o wide
        return 1
    }

    # 等待 pods ready
    kubectl wait pod -l app=terway-eniip --for=condition=ready -n kube-system --timeout=5m

    # 等待 controlplane Deployment 就绪 (必须等 controlplane ready 才能跑 PostUpgrade)
    log_info "Waiting for controlplane Deployment rollout..."
    kubectl rollout status deployment/terway-controlplane -n kube-system --timeout=5m

    # 等待 controlplane pods ready
    kubectl wait pod -l k8s-app=terway-controlplane --for=condition=ready -n kube-system --timeout=3m

    log_info "Upgrade to v${version} complete"
}

# 运行 e2e 测试
run_e2e() {
    local phase="$1"

    log_step "Running e2e test (phase: ${phase})..."

    local test_name
    if [[ "${phase}" == "pre" ]]; then
        test_name="TestUpgrade_PreUpgrade"
    else
        test_name="TestUpgrade_PostUpgrade"
    fi

    # Config B/C intentionally leave eniip_virtual_type unset (legacy veth).
    # Config A differs by release (IPVlan vs datapathv2), so its live value is
    # protected by the pre/post ConfigMap snapshot instead of hard-coding one.
    if [[ "${DRY_RUN}" == "true" ]]; then
        log_info "[DRY-RUN] Would run: go test -tags e2e -run ${test_name} -upgrade-phase ${phase}"
        return
    fi

    cd "${PROJECT_ROOT}"
    if [[ "${CONFIG}" == "B" || "${CONFIG}" == "C" ]]; then
        go test -v -count=1 -timeout 30m -tags e2e ./tests \
            -run "${test_name}" \
            -upgrade-phase "${phase}" \
            -upgrade-expected-datapath unset \
            -region-id "${REGION}"
    else
        go test -v -count=1 -timeout 30m -tags e2e ./tests \
            -run "${test_name}" \
            -upgrade-phase "${phase}" \
            -region-id "${REGION}"
    fi

    return $?
}

# 运行单个场景
run_scenario() {
    local -a versions=("${@:1:$(($#-1))}")
    local config="${!#}"
    local scenario_name

    # 构建场景名称
    local path_str
    path_str=$(IFS='→'; echo "${versions[*]}")
    scenario_name="${path_str} | ${config}"

    local start_time
    start_time=$(date +%s)

    echo ""
    echo "════════════════════════════════════════════════════════════════"
    log_info "Scenario: ${scenario_name}"
    echo "════════════════════════════════════════════════════════════════"

    local result="PASS"
    if [[ "${DRY_RUN}" == "true" ]]; then
        result="DRY-RUN"
    fi

    # Step 1: 清理 (非第一个场景时)
    if ! cleanup_terway; then
        log_error "Cleanup failed; refusing to continue with a partial environment"
        return 1
    fi
    log_info "Cleanup OK"

    # Step 2: 部署初始版本
    if ! deploy_version "${versions[0]}" "${config}"; then
        log_error "Deploy failed"
        result="FAIL"
        echo ""
        echo "${scenario_name} | ${result} | $(($(date +%s) - start_time))s"
        return 1
    fi

    # Step 3: 部署旧版本后重启节点，确保节点 CNI 和数据面完整切换。
    if ! reboot_nodes; then
        log_error "Node reboot failed"
        result="FAIL"
        echo ""
        echo "${scenario_name} | ${result} | $(($(date +%s) - start_time))s"
        return 1
    fi

    # Step 4: 预升级 e2e
    if ! run_e2e "pre"; then
        log_error "PreUpgrade e2e failed"
        result="FAIL"
        echo ""
        echo "${scenario_name} | ${result} | $(($(date +%s) - start_time))s"
        return 1
    fi

    # Step 5+: 升级 (每个后续版本)
    for ((i=1; i<${#versions[@]}; i++)); do
        local hop_num=$i
        local total_hops=$((${#versions[@]} - 1))

        log_info "Upgrade hop ${hop_num}/${total_hops}: v${versions[$((i-1))]} → v${versions[i]}"

        if ! upgrade_version "${versions[i]}"; then
            log_error "Upgrade to v${versions[i]} failed"
            result="FAIL"
            break
        fi

        # 每跳后运行 PostUpgrade e2e
        if ! run_e2e "post"; then
            log_error "PostUpgrade e2e failed after hop ${hop_num}"
            result="FAIL"
            break
        fi

        log_info "Hop ${hop_num}/${total_hops} completed successfully"
    done

    local duration=$(($(date +%s) - start_time))

    echo ""
    echo "────────────────────────────────────────────────────────────────"
    echo "Scenario: ${scenario_name}"
    echo "Result:   ${result}"
    echo "Duration: ${duration}s"
    echo "────────────────────────────────────────────────────────────────"

    [[ "${result}" != "FAIL" ]]
}

# 主函数
main() {
    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║        Terway 升级兼容性测试                                       ║"
    echo "╚══════════════════════════════════════════════════════════════════╝"

    # 提取集群参数
    extract_cluster_params

    # 结果汇总
    declare -a RESULTS=()
    local overall_status=0

    if [[ "${ALL:-false}" == "true" ]]; then
        # 运行全部 6 个场景
        SCENARIOS=(
            "1.9.18 1.17.6 A"
            "1.9.18 1.17.6 B"
            "1.9.18 1.17.6 C"
            "1.7.4 1.9.18 1.17.6 A"
            "1.7.4 1.9.18 1.17.6 B"
            "1.7.4 1.9.18 1.17.6 C"
        )

        for scenario in "${SCENARIOS[@]}"; do
            read -ra parts <<< "${scenario}"
            local last_idx=$((${#parts[@]}-1))
            local scenario_status=0
            run_scenario "${parts[@]:0:${last_idx}}" "${parts[${last_idx}]}" || scenario_status=$?
            if [[ "${DRY_RUN}" == "true" ]]; then
                RESULTS+=("${scenario} | DRY-RUN")
            elif [[ ${scenario_status} -eq 0 ]]; then
                RESULTS+=("${scenario} | PASS")
            else
                RESULTS+=("${scenario} | FAIL")
                overall_status=1
            fi
        done
    else
        # 运行指定场景
        if [[ ${#PATH_VERSIONS[@]} -lt 2 ]]; then
            log_error "--path requires at least 2 versions (from and to)"
            exit 1
        fi
        if [[ -z "${CONFIG}" ]]; then
            log_error "--config is required (A, B, or C)"
            exit 1
        fi

        local scenario_status=0
        run_scenario "${PATH_VERSIONS[@]}" "${CONFIG}" || scenario_status=$?
        if [[ "${DRY_RUN}" == "true" ]]; then
            RESULTS+=("${PATH_VERSIONS[*]} | ${CONFIG} | DRY-RUN")
        elif [[ ${scenario_status} -eq 0 ]]; then
            RESULTS+=("${PATH_VERSIONS[*]} | ${CONFIG} | PASS")
        else
            RESULTS+=("${PATH_VERSIONS[*]} | ${CONFIG} | FAIL")
            overall_status=1
        fi
    fi

    # 打印汇总
    echo ""
    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║                        测试结果汇总                                ║"
    echo "╠══════════════════════════════════════════════════════════════════╣"
    for r in "${RESULTS[@]}"; do
        echo "  ${r}"
    done
    echo "╚══════════════════════════════════════════════════════════════════╝"

    return "${overall_status}"
}

main "$@"
