#!/usr/bin/env bash
set -euo pipefail

# inspect-terway-pod.sh
#
# Helper script to inspect Terway-related configuration on a specific Pod.
# Intended to be used from Terway troubleshooting SOP as a quick summary of:
# - Whether the Pod uses hostNetwork (not handled by Terway CNI)
# - Whether PodENI (exclusive / trunk ENI) is enabled
# - Which annotation-based config source is used (pod-networks, pod-networks-request, or pod-networking)
#
# Usage:
#   scripts/inspect-terway-pod.sh <namespace> <pod-name>
#

if [[ ${#@} -ne 2 ]]; then
  echo "Usage: $0 <namespace> <pod-name>" >&2
  exit 1
fi

NS="$1"
POD="$2"

# Basic existence check
if ! kubectl get pod "$POD" -n "$NS" >/dev/null 2>&1; then
  echo "ERROR: Pod $NS/$POD not found by kubectl" >&2
  exit 1
fi

echo "============================================================"
echo "Pod: $NS/$POD"

echo "- Basic spec flags:"

host_network=$(kubectl get pod "$POD" -n "$NS" -o jsonpath='{.spec.hostNetwork}' 2>/dev/null || true)
if [[ "$host_network" == "true" ]]; then
  echo "  * hostNetwork: true  -> Pod is not handled by Terway CNI"
else
  echo "  * hostNetwork: ${host_network:-false}  -> Pod uses CNI networking"
fi

echo

echo "- Terway-related annotations:"

POD_ENI_KEY="k8s.aliyun.com/pod-eni"
POD_NETWORKS_KEY="k8s.aliyun.com/pod-networks"
POD_NETWORKS_REQUEST_KEY="k8s.aliyun.com/pod-networks-request"
POD_NETWORKING_KEY="k8s.aliyun.com/pod-networking"

pod_eni=$(kubectl get pod "$POD" -n "$NS" \
  -o jsonpath='{.metadata.annotations["'"$POD_ENI_KEY"'"]}' 2>/dev/null || true)
pod_networks=$(kubectl get pod "$POD" -n "$NS" \
  -o jsonpath='{.metadata.annotations["'"$POD_NETWORKS_KEY"'"]}' 2>/dev/null || true)
pod_networks_req=$(kubectl get pod "$POD" -n "$NS" \
  -o jsonpath='{.metadata.annotations["'"$POD_NETWORKS_REQUEST_KEY"'"]}' 2>/dev/null || true)
pod_networking=$(kubectl get pod "$POD" -n "$NS" \
  -o jsonpath='{.metadata.annotations["'"$POD_NETWORKING_KEY"'"]}' 2>/dev/null || true)

if [[ -n "$pod_eni" ]]; then
  echo "  * $POD_ENI_KEY: $pod_eni"
else
  echo "  * $POD_ENI_KEY: <not set>"
fi

if [[ -n "$pod_networks" ]]; then
  echo "  * $POD_NETWORKS_KEY: present (explicit pod-networks config)"
else
  echo "  * $POD_NETWORKS_KEY: <not set>"
fi

if [[ -n "$pod_networks_req" ]]; then
  echo "  * $POD_NETWORKS_REQUEST_KEY: present (pod-networks-request config)"
else
  echo "  * $POD_NETWORKS_REQUEST_KEY: <not set>"
fi

if [[ -n "$pod_networking" ]]; then
  echo "  * $POD_NETWORKING_KEY: $pod_networking (PodNetworking resource name)"
else
  echo "  * $POD_NETWORKING_KEY: <not set>"
fi

echo

echo "- Derived Terway behavior:"

if [[ "$host_network" == "true" ]]; then
  echo "  * Pod uses hostNetwork=true, Terway CNI will not process this Pod."
else
  # Check PodENI usage (exclusive/trunk ENI)
  pod_eni_normalized=$(printf '%s' "$pod_eni" | tr '[:upper:]' '[:lower:]')
  if [[ "$pod_eni_normalized" == "true" ]]; then
    echo "  * PodENI is enabled (pod-eni=true): Pod is expected to use PodENI / exclusive ENI configuration."
  elif [[ -n "$pod_eni" ]]; then
    echo "  * PodENI annotation is set but not 'true' (value=$pod_eni): may be ignored by Terway."
  else
    echo "  * PodENI annotation is not set: Pod does not explicitly request PodENI."
  fi

  # Determine config source priority as in mutating webhook:
  # 1. pod-networks
  # 2. pod-networks-request
  # 3. matched PodNetworking
  # 4. fallback to eni-config default on eth0
  if [[ -n "$pod_networks" ]]; then
    echo "  * Effective network config source: pod-networks annotation ($POD_NETWORKS_KEY)."
  elif [[ -n "$pod_networks_req" ]]; then
    echo "  * Effective network config source: pod-networks-request annotation ($POD_NETWORKS_REQUEST_KEY)."
  elif [[ -n "$pod_networking" ]]; then
    echo "  * Effective network config source: matched PodNetworking resource ($POD_NETWORKING_KEY=$pod_networking)."
  else
    echo "  * Effective network config source: none of the above annotations present; webhook falls back to eni-config default on eth0 (if Terway webhook and CRD mode are enabled)."
  fi

  # Warn if multiple config annotations are present (should be mutually exclusive).
  count_cfg=0
  [[ -n "$pod_networks" ]] && count_cfg=$((count_cfg+1))
  [[ -n "$pod_networks_req" ]] && count_cfg=$((count_cfg+1))
  [[ -n "$pod_networking" ]] && count_cfg=$((count_cfg+1))

  if [[ $count_cfg -gt 1 ]]; then
    echo "  * WARNING: Multiple network config annotations are set (pod-networks / pod-networks-request / pod-networking)."
    echo "            Per webhook logic they must be mutually exclusive; this may cause admission errors."
  fi
fi
