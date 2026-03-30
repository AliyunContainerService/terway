#!/bin/bash
#
# Terway 部署脚本
# 该脚本从 Terraform 状态读取配置，并使用 Helm 部署 Terway 组件
#
# 用法:
#   ./deploy-terway.sh [选项]
#
# 选项:
#   -t, --tag <tag>              镜像 tag (默认: 从 git 获取 short commit)
#   -r, --registry <registry>    镜像仓库地址 (默认: registry-vpc.cn-hangzhou.aliyuncs.com/l1b0k)
#   -p, --pull-policy <policy>   镜像拉取策略 (默认: IfNotPresent)
#   -d, --daemon-mode <mode>     Daemon 模式: ENIMultiIP|ENIDirectIP (默认: ENIMultiIP)
#   --enable-dp-v2               启用 Datapath V2
#   --enable-network-policy      启用网络策略
#   --ip-stack <stack>           IP 栈: ipv4|ipv6|dual (默认: ipv4)
#   -f, --values <file>          指定 values 文件路径 (默认: 自动生成)
#   --dry-run                    仅生成 values 文件，不执行部署
#   -h, --help                   显示帮助信息
#
# 环境变量:
#   TERWAY_IMAGE_TAG            镜像 tag
#   TERWAY_IMAGE_REGISTRY       镜像仓库
#   TERWAY_IMAGE_PULL_POLICY    镜像拉取策略
#   TERWAY_DAEMON_MODE          Daemon 模式
#   TERWAY_ENABLE_DP_V2         是否启用 Datapath V2 (true/false)
#   TERWAY_ENABLE_NETWORK_POLICY 是否启用网络策略 (true/false)
#   TERWAY_IP_STACK             IP 栈类型
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TERRAFORM_STATE_FILE="${SCRIPT_DIR}/terraform.tfstate"
VALUES_OUTPUT_FILE="${SCRIPT_DIR}/terway-values.yaml"

# 动态获取项目根目录和 Chart 路径
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
CHART_PATH="${PROJECT_ROOT}/charts/terway"

# 默认配置
DEFAULT_REGISTRY="registry-vpc.cn-hangzhou.aliyuncs.com/l1b0k"
DEFAULT_PULL_POLICY="IfNotPresent"
DEFAULT_DAEMON_MODE="ENIMultiIP"
DEFAULT_IP_STACK="ipv4"

# 用户配置（优先从命令行，其次从环境变量，最后使用默认值）
TERWAY_TAG="${TERWAY_IMAGE_TAG:-}"
TERWAY_REGISTRY="${TERWAY_IMAGE_REGISTRY:-$DEFAULT_REGISTRY}"
TERWAY_PULL_POLICY="${TERWAY_IMAGE_PULL_POLICY:-$DEFAULT_PULL_POLICY}"
TERWAY_DAEMON_MODE="${TERWAY_DAEMON_MODE:-$DEFAULT_DAEMON_MODE}"
TERWAY_ENABLE_DP_V2="${TERWAY_ENABLE_DP_V2:-false}"
TERWAY_ENABLE_NETWORK_POLICY="${TERWAY_ENABLE_NETWORK_POLICY:-false}"
TERWAY_IP_STACK="${TERWAY_IP_STACK:-$DEFAULT_IP_STACK}"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_debug() {
    echo -e "${BLUE}[DEBUG]${NC} $1"
}

# 显示帮助信息
show_help() {
    sed -n '/^#/!b;:a;N;/\n#/!ba;s/^#//;s/^ //;s/^#//;p' "$0" | head -n 35
}

# 解析命令行参数
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -t|--tag)
                TERWAY_TAG="$2"
                shift 2
                ;;
            -r|--registry)
                TERWAY_REGISTRY="$2"
                shift 2
                ;;
            -p|--pull-policy)
                TERWAY_PULL_POLICY="$2"
                shift 2
                ;;
            -d|--daemon-mode)
                TERWAY_DAEMON_MODE="$2"
                shift 2
                ;;
            --enable-dp-v2)
                TERWAY_ENABLE_DP_V2="true"
                shift
                ;;
            --enable-network-policy)
                TERWAY_ENABLE_NETWORK_POLICY="true"
                shift
                ;;
            --ip-stack)
                TERWAY_IP_STACK="$2"
                shift 2
                ;;
            -f|--values)
                VALUES_OUTPUT_FILE="$2"
                shift 2
                ;;
            --dry-run)
                DRY_RUN=true
                shift
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                show_help
                exit 1
                ;;
        esac
    done
}

# 获取默认 tag（从 git commit）
get_default_tag() {
    cd "${PROJECT_ROOT}"
    local tag
    tag=$(git rev-parse --short=8 HEAD 2>/dev/null || echo "latest")
    echo "$tag"
}

# 检查依赖
check_dependencies() {
    log_info "Checking dependencies..."

    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is not installed"
        exit 1
    fi

    if ! command -v helm &> /dev/null; then
        log_error "helm is not installed"
        exit 1
    fi

    if ! command -v jq &> /dev/null; then
        log_error "jq is not installed"
        exit 1
    fi

    log_info "All dependencies are available"
}

# 检查 kubeconfig 文件
check_kubeconfig() {
    # 如果 KUBECONFIG_FILE 未设置，尝试从 terraform 读取
    if [[ -z "${KUBECONFIG_FILE}" ]] || [[ ! -f "${KUBECONFIG_FILE}" ]]; then
        # 尝试查找最新的 kubeconfig 文件
        # shellcheck disable=SC2012
        KUBECONFIG_FILE=$(ls -t "${SCRIPT_DIR}"/kubeconfig-* 2>/dev/null | head -1)
    fi

    if [[ -z "${KUBECONFIG_FILE}" ]] || [[ ! -f "${KUBECONFIG_FILE}" ]]; then
        log_error "Kubeconfig file not found"
        log_info "Please run 'terraform apply' first to generate the kubeconfig"
        exit 1
    fi

    log_info "Kubeconfig file found: ${KUBECONFIG_FILE}"
}

# 从 Terraform 状态读取配置
read_terraform_config() {
    log_info "Reading Terraform configuration..."

    if [[ ! -f "${TERRAFORM_STATE_FILE}" ]]; then
        log_error "Terraform state file not found: ${TERRAFORM_STATE_FILE}"
        log_info "Please run 'terraform apply' first"
        exit 1
    fi

    # 从 terraform.tfstate 读取配置
    CLUSTER_ID=$(jq -r '.resources[] | select(.type=="alicloud_cs_managed_kubernetes" and .name=="default") | .instances[0].attributes.id' "${TERRAFORM_STATE_FILE}")
    REGION=$(jq -r '.variables.region_id.value // "cn-hangzhou"' "${TERRAFORM_STATE_FILE}")
    SERVICE_CIDR=$(jq -r '.variables.service_cidr.value // "192.168.0.0/16"' "${TERRAFORM_STATE_FILE}")

    # 获取 VPC ID
    VPC_ID=$(jq -r '.resources[] | select(.type=="alicloud_vpc" and .name=="default") | .instances[0].attributes.id' "${TERRAFORM_STATE_FILE}")

    # 获取节点交换机 ID
    VSWITCH_IDS=$(jq -r '.resources[] | select(.type=="alicloud_vswitch" and .name=="vswitches") | .instances[].attributes.id' "${TERRAFORM_STATE_FILE}" | tr '\n' ',' | sed 's/,$//')

    # 获取 Terway 交换机 ID
    TERWAY_VSWITCH_IDS=$(jq -r '.resources[] | select(.type=="alicloud_vswitch" and .name=="terway_vswitches") | .instances[].attributes.id' "${TERRAFORM_STATE_FILE}" | tr '\n' ',' | sed 's/,$//')

    # 获取安全组 ID (从集群信息)
    SECURITY_GROUP_ID=$(jq -r '.resources[] | select(.type=="alicloud_cs_managed_kubernetes" and .name=="default") | .instances[0].attributes.security_group_id' "${TERRAFORM_STATE_FILE}")

    # 获取 kubeconfig 文件名
    KUBECONFIG_NAME=$(jq -r '.variables.k8s_name_prefix.value // "tf-ack-hangzhou-terway"' "${TERRAFORM_STATE_FILE}")
    KUBECONFIG_SUFFIX=$(jq -r '.resources[] | select(.type=="random_string" and .name=="cluster_suffix") | .instances[0].attributes.result' "${TERRAFORM_STATE_FILE}")
    KUBECONFIG_FILE="${SCRIPT_DIR}/kubeconfig-${KUBECONFIG_NAME}-${KUBECONFIG_SUFFIX}"

    log_info "Cluster ID: ${CLUSTER_ID}"
    log_info "Region: ${REGION}"
    log_info "VPC ID: ${VPC_ID}"
    log_info "Service CIDR: ${SERVICE_CIDR}"
    log_info "Node VSwitch IDs: ${VSWITCH_IDS}"
    log_info "Terway VSwitch IDs: ${TERWAY_VSWITCH_IDS}"
    log_info "Security Group ID: ${SECURITY_GROUP_ID}"
    log_info "Kubeconfig file: ${KUBECONFIG_FILE}"
}

# 打印配置摘要
print_config_summary() {
    log_info "Deployment Configuration:"
    echo "  Image Registry: ${TERWAY_REGISTRY}"
    echo "  Image Tag: ${TERWAY_TAG}"
    echo "  Pull Policy: ${TERWAY_PULL_POLICY}"
    echo "  Daemon Mode: ${TERWAY_DAEMON_MODE}"
    echo "  Enable Datapath V2: ${TERWAY_ENABLE_DP_V2}"
    echo "  Enable Network Policy: ${TERWAY_ENABLE_NETWORK_POLICY}"
    echo "  IP Stack: ${TERWAY_IP_STACK}"
    echo "  Values File: ${VALUES_OUTPUT_FILE}"
}

# 生成 Helm values 文件
generate_values_file() {
    log_info "Generating Helm values file..."

    # 构建 vSwitch IDs 按可用区分组 (生成 YAML 格式)
    VSWITCH_YAML=$(jq -r -n --argjson state "$(cat "${TERRAFORM_STATE_FILE}")" '
        $state.resources
        | map(select(.type == "alicloud_vswitch" and .name == "terway_vswitches"))
        | map(.instances[])
        | group_by(.attributes.zone_id // .attributes.availability_zone)
        | map({
            zone: (.[0].attributes.zone_id // .attributes.availability_zone),
            ids: [.[].attributes.id]
          })
        | map("    " + .zone + ":\n" + (.ids | map("      - " + .) | join("\n")))
        | join("\n")
    ')

    # 转换 boolean 为 YAML 格式
    local dp_v2_yaml="false"
    [[ "${TERWAY_ENABLE_DP_V2}" == "true" ]] && dp_v2_yaml="true"

    local network_policy_yaml="false"
    [[ "${TERWAY_ENABLE_NETWORK_POLICY}" == "true" ]] && network_policy_yaml="true"

    # 创建 values 文件
    cat > "${VALUES_OUTPUT_FILE}" << EOF
# Terway Helm Values - Generated by deploy-terway.sh
# Generated at: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
#
# Configuration:
#   Registry: ${TERWAY_REGISTRY}
#   Tag: ${TERWAY_TAG}
#   Daemon Mode: ${TERWAY_DAEMON_MODE}
#   Datapath V2: ${TERWAY_ENABLE_DP_V2}
#   Network Policy: ${TERWAY_ENABLE_NETWORK_POLICY}
#   IP Stack: ${TERWAY_IP_STACK}

centralizedIPAM: true

terway:
  image:
    repository: ${TERWAY_REGISTRY}/terway
    pullPolicy: ${TERWAY_PULL_POLICY}
    tag: "${TERWAY_TAG}"

  daemonMode: ${TERWAY_DAEMON_MODE}
  enableDatapathV2: ${dp_v2_yaml}
  networkPolicyProvider: ebpf
  enableNetworkPolicy: ${network_policy_yaml}

  ipStack: ${TERWAY_IP_STACK}

  # 安全组配置
  securityGroupIDs:
    - "${SECURITY_GROUP_ID}"

  # 交换机配置 (按可用区)
  vSwitchIDs:
${VSWITCH_YAML}

  # Service CIDR
  serviceCIDR: "${SERVICE_CIDR}"

terwayControlplane:
  replicaCount: 1

  image:
    repository: ${TERWAY_REGISTRY}/terway-controlplane
    pullPolicy: ${TERWAY_PULL_POLICY}
    tag: "${TERWAY_TAG}"

  # 集群配置
  regionID: "${REGION}"
  clusterID: "${CLUSTER_ID}"
  vpcID: "${VPC_ID}"

  # 控制器配置
  controllers:
    - pod-eni
    - pod
    - pod-networking
    - node
    - multi-ip-node
    - multi-ip-pod

  # Credential path for addon token
  credentialPath: "/var/addon/token-config"
EOF

    log_info "Values file generated: ${VALUES_OUTPUT_FILE}"
}

# 部署 Terway
deploy_terway() {
    log_info "Deploying Terway using Helm..."

    # 检查 chart 路径
    if [[ ! -d "${CHART_PATH}" ]]; then
        log_error "Chart path not found: ${CHART_PATH}"
        exit 1
    fi

    # 使用 Helm 部署
    helm upgrade --install terway "${CHART_PATH}" \
        --kubeconfig "${KUBECONFIG_FILE}" \
        --namespace kube-system \
        --values "${VALUES_OUTPUT_FILE}" \
        --create-namespace \
        --timeout 10m0s \
        --wait --wait-for-jobs

    log_info "Terway deployment completed"
}

# 验证部署
verify_deployment() {
    log_info "Verifying Terway deployment..."

    # 等待 pods 就绪
    kubectl --kubeconfig "${KUBECONFIG_FILE}" wait --for=condition=available deployment/terway-controlplane -n kube-system --timeout=300s || true
    kubectl --kubeconfig "${KUBECONFIG_FILE}" wait --for=condition=ready pod -l k8s-app=terway -n kube-system --timeout=300s || true

    # 显示 pods 状态
    log_info "Terway pods status:"
    kubectl --kubeconfig "${KUBECONFIG_FILE}" get pods -n kube-system -l k8s-app=terway

    log_info "Terway-controlplane pods status:"
    kubectl --kubeconfig "${KUBECONFIG_FILE}" get pods -n kube-system -l app=terway-controlplane
}

# 主函数
main() {
    # 解析命令行参数
    parse_args "$@"

    # 如果没有指定 tag，使用 git commit
    if [[ -z "${TERWAY_TAG}" ]]; then
        TERWAY_TAG=$(get_default_tag)
        log_info "Using git commit as image tag: ${TERWAY_TAG}"
    fi

    log_info "Starting Terway deployment..."

    # 显示配置
    print_config_summary

    check_dependencies
    check_kubeconfig
    read_terraform_config
    generate_values_file

    if [[ "${DRY_RUN:-}" == "true" ]]; then
        log_info "Dry run mode - skipping deployment"
        log_info "Values file generated: ${VALUES_OUTPUT_FILE}"
        echo ""
        log_info "To deploy, run:"
        echo "  ./deploy-terway.sh --tag ${TERWAY_TAG} --registry ${TERWAY_REGISTRY}"
        exit 0
    fi

    deploy_terway
    verify_deployment

    log_info "Terway deployment completed successfully!"
    echo ""
    log_info "Image used:"
    echo "  ${TERWAY_REGISTRY}/terway:${TERWAY_TAG}"
    echo "  ${TERWAY_REGISTRY}/terway-controlplane:${TERWAY_TAG}"
}

main "$@"
