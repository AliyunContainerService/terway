#!/usr/bin/env bash
set -euo pipefail

# inspect-terway-node.sh
#
# Helper script to inspect Terway-related attributes of one or more nodes.
# Intended to be used from Terway troubleshooting SOP as a quick summary of
# node ENI mode, dynamic config, LingJun status, and ENO API type.
#
# Usage:
#   hack/inspect-terway-node.sh <node1> [node2 ...]
#   hack/inspect-terway-node.sh                 # inspect all nodes
#

EXCLUSIVE_LABEL_KEY="k8s.aliyun.com/exclusive-mode-eni-type"
DYNAMIC_CONFIG_LABEL_KEY="terway-config"
LINGJUN_LABEL_KEY="alibabacloud.com/lingjun-worker"
IGNORE_BY_TERWAY_LABEL_KEY="k8s.aliyun.com/ignore-by-terway"
NO_KUBE_PROXY_LABEL_KEY="k8s.aliyun.com/no-kube-proxy"
ENO_API_ANNOTATION_KEY="k8s.aliyun.com/eno-api"

usage() {
  cat <<EOF
Usage:
  $0 <node1> [node2 ...]   Inspect specific nodes
  $0                       Inspect all nodes in the cluster

The script requires a working kubectl context pointing at the target cluster.
EOF
}

if [[ "${1-}" == "-h" || "${1-}" == "--help" ]]; then
  usage
  exit 0
fi

nodes=("$@")
if [[ ${#nodes[@]} -eq 0 ]]; then
  # Get all node names
  nodes=()
  while IFS= read -r line; do
    nodes+=("$line")
  done < <(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
fi

if [[ ${#nodes[@]} -eq 0 ]]; then
  echo "No nodes found." >&2
  exit 1
fi

for node in "${nodes[@]}"; do
  if [[ -z "$node" ]]; then
    continue
  fi

  echo "============================================================"
  echo "Node: $node"

  # Basic presence check
  if ! kubectl get node "$node" >/dev/null 2>&1; then
    echo "  ERROR: Node $node not found by kubectl. Skipping."
    continue
  fi

  echo "- Kubernetes Node labels:"

  exclusive_mode_raw=$(kubectl get node "$node" \
    -o jsonpath='{.metadata.labels["'"$EXCLUSIVE_LABEL_KEY"'"]}' 2>/dev/null || true)
  exclusive_mode_lc=$(printf '%s' "${exclusive_mode_raw}" | tr '[:upper:]' '[:lower:]')

  if [[ -z "${exclusive_mode_raw}" ]]; then
    echo "  * Exclusive ENI mode label ($EXCLUSIVE_LABEL_KEY): <not set> (treated as shared/default)"
  else
    case "${exclusive_mode_lc}" in
      enionly)
        echo "  * Exclusive ENI mode label ($EXCLUSIVE_LABEL_KEY): ${exclusive_mode_raw}  -> exclusive ENI node"
        ;;
      *)
        echo "  * Exclusive ENI mode label ($EXCLUSIVE_LABEL_KEY): ${exclusive_mode_raw}  -> shared (default) node"
        ;;
    esac
  fi

  dynamic_cfg=$(kubectl get node "$node" \
    -o jsonpath='{.metadata.labels["'"$DYNAMIC_CONFIG_LABEL_KEY"'"]}' 2>/dev/null || true)
  if [[ -z "${dynamic_cfg}" ]]; then
    echo "  * Dynamic config label ($DYNAMIC_CONFIG_LABEL_KEY): <none> (cluster-level config only)"
  else
    echo "  * Dynamic config label ($DYNAMIC_CONFIG_LABEL_KEY): ${dynamic_cfg}"
  fi

  lingjun_label=$(kubectl get node "$node" \
    -o jsonpath='{.metadata.labels["'"$LINGJUN_LABEL_KEY"'"]}' 2>/dev/null || true)
  if [[ -n "${lingjun_label}" ]]; then
    echo "  * LingJun node label ($LINGJUN_LABEL_KEY): ${lingjun_label}  -> LingJun node"
  else
    echo "  * LingJun node label ($LINGJUN_LABEL_KEY): <not set>  -> non-LingJun node"
  fi

  ignore_label=$(kubectl get node "$node" \
    -o jsonpath='{.metadata.labels["'"$IGNORE_BY_TERWAY_LABEL_KEY"'"]}' 2>/dev/null || true)
  if [[ -n "${ignore_label}" ]]; then
    echo "  * Ignore-by-Terway label ($IGNORE_BY_TERWAY_LABEL_KEY): ${ignore_label}  -> Terway will NOT manage this node"
  else
    echo "  * Ignore-by-Terway label ($IGNORE_BY_TERWAY_LABEL_KEY): <not set>  -> node is eligible for Terway management"
  fi

  no_kube_proxy=$(kubectl get node "$node" \
    -o jsonpath='{.metadata.labels["'"$NO_KUBE_PROXY_LABEL_KEY"'"]}' 2>/dev/null || true)
  if [[ "${no_kube_proxy}" == "true" ]]; then
    echo "  * No-kube-proxy label ($NO_KUBE_PROXY_LABEL_KEY): true  -> kube-proxy is NOT expected to run on this node"
  elif [[ -n "${no_kube_proxy}" ]]; then
    echo "  * No-kube-proxy label ($NO_KUBE_PROXY_LABEL_KEY): ${no_kube_proxy}"
  else
    echo "  * No-kube-proxy label ($NO_KUBE_PROXY_LABEL_KEY): <not set>  -> kube-proxy is expected to be present"
  fi

  echo
  echo "- Terway Node CR (nodes.network.alibabacloud.com):"

  if ! kubectl get nodes.network.alibabacloud.com "$node" >/dev/null 2>&1; then
    echo "  * Node CR: not found (centralized IPAM may be disabled or controlplane not running)"
    echo
    continue
  fi

  # LingJun label on Node CR
  cr_lingjun_label=$(kubectl get nodes.network.alibabacloud.com "$node" \
    -o jsonpath='{.metadata.labels["'"$LINGJUN_LABEL_KEY"'"]}' 2>/dev/null || true)
  if [[ -n "${cr_lingjun_label}" ]]; then
    echo "  * CR LingJun label ($LINGJUN_LABEL_KEY): ${cr_lingjun_label}  -> LingJun node CR"
  else
    echo "  * CR LingJun label ($LINGJUN_LABEL_KEY): <not set>"
  fi

  # ENO API type from Node CR annotation
  eno_api=$(kubectl get nodes.network.alibabacloud.com "$node" \
    -o jsonpath='{.metadata.annotations["'"$ENO_API_ANNOTATION_KEY"'"]}' 2>/dev/null || true)

  if [[ -z "${eno_api}" ]]; then
    echo "  * ENO API annotation ($ENO_API_ANNOTATION_KEY): <not set>"
  else
    case "${eno_api}" in
      ecs)
        desc="ECS standard API"
        ;;
      ecs-hdeni)
        desc="ECS high-density ENI API"
        ;;
      hdeni)
        desc="ENO HDENI API"
        ;;
      *)
        desc="unknown type"
        ;;
    esac
    echo "  * ENO API annotation ($ENO_API_ANNOTATION_KEY): ${eno_api}  -> ${desc}"
  fi

  echo

done
