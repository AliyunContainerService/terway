#!/usr/bin/env bash
set -euo pipefail

# inspect-terway-cluster.sh
#
# Helper script to inspect Terway cluster-level configuration.
# Intended to be used from Terway troubleshooting SOP to gather:
# - Terway version from the DaemonSet image
# - Service CIDR from ack-cluster-profile
# - Kube-proxy mode and cluster CIDR from kube-proxy-worker ConfigMap
# - IPAM type (centralized or local) from eni-config
# - Other key eni-config settings (ip_stack, enable_eni_trunking, etc.)
#
# Usage:
#   scripts/inspect-terway-cluster.sh
#

echo "============================================================"
echo "Terway Cluster Configuration Inspection"
echo "============================================================"
echo

# 1. Terway version from DaemonSet image
echo "1. Terway Version"
echo "-----------------"

TERWAY_DS_NAME="terway-eniip"
TERWAY_NS="kube-system"

if ! kubectl get ds "$TERWAY_DS_NAME" -n "$TERWAY_NS" >/dev/null 2>&1; then
  echo "  * DaemonSet '$TERWAY_DS_NAME' not found in namespace '$TERWAY_NS'."
  echo "    (Terway may use a different DaemonSet name or namespace.)"
else
  terway_image=$(kubectl get ds "$TERWAY_DS_NAME" -n "$TERWAY_NS" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="terway")].image}' 2>/dev/null || true)
  
  if [[ -z "$terway_image" ]]; then
    echo "  * Could not retrieve Terway container image from DaemonSet."
  else
    echo "  * Terway image: $terway_image"
    
    # Extract version tag (everything after the last ':')
    if [[ "$terway_image" =~ :([^:]+)$ ]]; then
      terway_version="${BASH_REMATCH[1]}"
      echo "  * Terway version tag: $terway_version"
    else
      echo "  * Could not parse version tag from image."
    fi
  fi
fi

echo
echo "2. ACK Cluster Profile"
echo "----------------------"

ACK_CM_NAME="ack-cluster-profile"
ACK_CM_NS="kube-system"

if ! kubectl get cm "$ACK_CM_NAME" -n "$ACK_CM_NS" >/dev/null 2>&1; then
  echo "  * ConfigMap '$ACK_CM_NAME' not found in namespace '$ACK_CM_NS'."
  echo "    (This may not be an ACK cluster or the ConfigMap name differs.)"
else
  service_cidr=$(kubectl get cm "$ACK_CM_NAME" -n "$ACK_CM_NS" \
    -o jsonpath='{.data.serviceCIDR}' 2>/dev/null || true)
  ip_stack=$(kubectl get cm "$ACK_CM_NAME" -n "$ACK_CM_NS" \
    -o jsonpath='{.data.ipStack}' 2>/dev/null || true)
  vpc_id=$(kubectl get cm "$ACK_CM_NAME" -n "$ACK_CM_NS" \
    -o jsonpath='{.data.vpcid}' 2>/dev/null || true)

  
  echo "  * serviceCIDR: ${service_cidr:-<not set>}"
  echo "  * ipStack: ${ip_stack:-<not set>}"
  echo "  * vpcid: ${vpc_id:-<not set>}"

fi

echo
echo "3. Kube-Proxy Configuration"
echo "---------------------------"

KUBE_PROXY_CM_NAME="kube-proxy-worker"
KUBE_PROXY_CM_NS="kube-system"

if ! kubectl get cm "$KUBE_PROXY_CM_NAME" -n "$KUBE_PROXY_CM_NS" >/dev/null 2>&1; then
  echo "  * ConfigMap '$KUBE_PROXY_CM_NAME' not found in namespace '$KUBE_PROXY_CM_NS'."
  echo "    (Kube-proxy may be disabled or use a different ConfigMap name.)"
else
  kube_proxy_config=$(kubectl get cm "$KUBE_PROXY_CM_NAME" -n "$KUBE_PROXY_CM_NS" \
    -o jsonpath='{.data.config\.conf}' 2>/dev/null || true)
  
  if [[ -z "$kube_proxy_config" ]]; then
    echo "  * Could not retrieve kube-proxy config.conf from ConfigMap."
  else
    # Extract mode and clusterCIDR using grep/awk
    kube_proxy_mode=$(echo "$kube_proxy_config" | grep -E '^\s*mode:' | awk '{print $2}' || true)
    cluster_cidr=$(echo "$kube_proxy_config" | grep -E '^\s*clusterCIDR:' | awk '{print $2}' || true)
    
    echo "  * mode: ${kube_proxy_mode:-<not set>}"
    echo "  * clusterCIDR: ${cluster_cidr:-<not set>}"
  fi
fi

echo
echo "4. Terway ENI Config (eni-config)"
echo "---------------------------------"

ENI_CONFIG_CM_NAME="eni-config"
ENI_CONFIG_CM_NS="kube-system"

if ! kubectl get cm "$ENI_CONFIG_CM_NAME" -n "$ENI_CONFIG_CM_NS" >/dev/null 2>&1; then
  echo "  * ConfigMap '$ENI_CONFIG_CM_NAME' not found in namespace '$ENI_CONFIG_CM_NS'."
  echo "    (Terway may not be configured or uses a different ConfigMap.)"
else
  eni_conf_json=$(kubectl get cm "$ENI_CONFIG_CM_NAME" -n "$ENI_CONFIG_CM_NS" \
    -o jsonpath='{.data.eni_conf}' 2>/dev/null || true)
  
  disable_network_policy=$(kubectl get cm "$ENI_CONFIG_CM_NAME" -n "$ENI_CONFIG_CM_NS" \
    -o jsonpath='{.data.disable_network_policy}' 2>/dev/null || true)
  
  echo "  * disable_network_policy: ${disable_network_policy:-<not set>}"
  
  if [[ -z "$eni_conf_json" ]]; then
    echo "  * Could not retrieve eni_conf JSON from ConfigMap."
  else
    # Parse key fields from JSON using grep/sed (portable fallback if jq is not available)
    # Try using jq if available, otherwise fall back to grep
    if command -v jq >/dev/null 2>&1; then
      ipam_type=$(echo "$eni_conf_json" | jq -r '.ipam_type // empty' 2>/dev/null || true)
      ip_stack_eni=$(echo "$eni_conf_json" | jq -r '.ip_stack // empty' 2>/dev/null || true)
      enable_eni_trunking=$(echo "$eni_conf_json" | jq -r '.enable_eni_trunking // empty' 2>/dev/null || true)
      enable_erdma=$(echo "$eni_conf_json" | jq -r '.enable_erdma // empty' 2>/dev/null || true)
      service_cidr_eni=$(echo "$eni_conf_json" | jq -r '.service_cidr // empty' 2>/dev/null || true)
      vswitch_selection_policy=$(echo "$eni_conf_json" | jq -r '.vswitch_selection_policy // empty' 2>/dev/null || true)
      max_pool_size=$(echo "$eni_conf_json" | jq -r '.max_pool_size // empty' 2>/dev/null || true)
      min_pool_size=$(echo "$eni_conf_json" | jq -r '.min_pool_size // empty' 2>/dev/null || true)
    else
      # Fallback: use grep + sed for basic extraction
      ipam_type=$(echo "$eni_conf_json" | grep -o '"ipam_type":"[^"]*"' | sed 's/"ipam_type":"\(.*\)"/\1/' || true)
      ip_stack_eni=$(echo "$eni_conf_json" | grep -o '"ip_stack":"[^"]*"' | sed 's/"ip_stack":"\(.*\)"/\1/' || true)
      enable_eni_trunking=$(echo "$eni_conf_json" | grep -o '"enable_eni_trunking":[^,}]*' | sed 's/"enable_eni_trunking":\(.*\)/\1/' || true)
      enable_erdma=$(echo "$eni_conf_json" | grep -o '"enable_erdma":[^,}]*' | sed 's/"enable_erdma":\(.*\)/\1/' || true)
      service_cidr_eni=$(echo "$eni_conf_json" | grep -o '"service_cidr":"[^"]*"' | sed 's/"service_cidr":"\(.*\)"/\1/' || true)
      vswitch_selection_policy=$(echo "$eni_conf_json" | grep -o '"vswitch_selection_policy":"[^"]*"' | sed 's/"vswitch_selection_policy":"\(.*\)"/\1/' || true)
      max_pool_size=$(echo "$eni_conf_json" | grep -o '"max_pool_size":[^,}]*' | sed 's/"max_pool_size":\(.*\)/\1/' || true)
      min_pool_size=$(echo "$eni_conf_json" | grep -o '"min_pool_size":[^,}]*' | sed 's/"min_pool_size":\(.*\)/\1/' || true)
    fi
    
    echo "  * ipam_type: ${ipam_type:-<not set>}"
    
    # Interpret IPAM type
    if [[ "$ipam_type" == "crd" ]]; then
      echo "    -> Centralized IPAM is enabled (ipam_type=crd)."
    elif [[ "$ipam_type" == "default" ]]; then
      echo "    -> Non-centralized (local) IPAM is enabled (ipam_type=default)."
    elif [[ -n "$ipam_type" ]]; then
      echo "    -> Unknown IPAM type: $ipam_type"
    fi
    
    echo "  * ip_stack: ${ip_stack_eni:-<not set>}"
    echo "  * enable_eni_trunking: ${enable_eni_trunking:-<not set>}"
    echo "  * enable_erdma: ${enable_erdma:-<not set>}"
    echo "  * service_cidr: ${service_cidr_eni:-<not set>}"
    echo "  * vswitch_selection_policy: ${vswitch_selection_policy:-<not set>}"
    echo "  * max_pool_size: ${max_pool_size:-<not set>}"
    echo "  * min_pool_size: ${min_pool_size:-<not set>}"
  fi
fi

echo
echo "============================================================"
echo "Inspection complete."
echo "============================================================"
