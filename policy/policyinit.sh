#!/bin/bash

mount -o remount rw /proc/sys

export DATASTORE_TYPE=kubernetes

masq_eni_only() {
  if ! "$1" -t nat -L terway-masq; then
    # Create a new chain in nat table.
    "$1" -t nat -N terway-masq
  fi

  if ! "$1" -t nat -L POSTROUTING | grep -q terway-masq; then
    # Append that chain to POSTROUTING table.
    "$1" -t nat -A POSTROUTING -m comment --comment "terway:masq-outgoing" ! -o lo -j terway-masq
  fi

  if ! "$1" -t nat -L terway-masq | grep -q MASQUERADE; then
    "$1" -t nat -A terway-masq -j MASQUERADE
  fi
}

terway_config_val() {
  config_key="$1"
  if [ ! -f /etc/cni/net.d/10-terway.conflist ]; then return; fi
  jq -r ".plugins[] | select(.type == \"terway\") | .${config_key}" /etc/cni/net.d/10-terway.conflist | sed 's/^null$//'
}

virtyal_type=$(terway_config_val 'eniip_virtual_type' | tr '[:upper:]' '[:lower:]')
network_policy_provider=$(terway_config_val 'network_policy_provider')

KERNEL_MAJOR_VERSION=$(uname -r | awk -F . '{print $1}')
KERNEL_MINOR_VERSION=$(uname -r | awk -F . '{print $2}')

node_capabilities=/var-run-eni/node_capabilities
datapath_mode=ipvlan

if [ ! -f "$node_capabilities" ]; then
  echo "Init node capabilities not finished, exiting"
  exit 1
fi

if grep -q "cni_exclusive_eni *= *eniOnly" "$node_capabilities"; then
  # write terway snat rule

  masq_eni_only iptables

  if grep -q "cni_ipv6_stack *= *true" "$node_capabilities"; then
    masq_eni_only ip6tables
  fi

  # for health check
  if [ "$FELIX_HEALTHPORT" != "" ]; then
    # shellcheck disable=SC2016
    exec socat TCP-LISTEN:"$FELIX_HEALTHPORT",bind=127.0.0.1,fork,reuseaddr system:'sleep 2;kill -9 $SOCAT_PID 2>/dev/null'
  else
    # shellcheck disable=SC2016
    exec socat TCP-LISTEN:9099,bind=127.0.0.1,fork,reuseaddr system:'sleep 2;kill -9 $SOCAT_PID 2>/dev/null'
  fi
fi

if grep -q "datapath *= *datapathv2" "$node_capabilities"; then
  datapath_mode=veth
fi

# kernel version has already checked in initContainer, so just determine whether plugin chaining exists
if [ "$virtyal_type" = "ipvlan" ] || [ "$virtyal_type" = "datapathv2" ]; then
  # check kernel version & enable cilium

  # kernel version equal and above 4.19
  if { [ "$KERNEL_MAJOR_VERSION" -eq 4 ] && [ "$KERNEL_MINOR_VERSION" -ge 19 ]; } ||
    [ "$KERNEL_MAJOR_VERSION" -gt 4 ]; then

    extra_args=$(terway_config_val 'cilium_args')
    if [ -z "$DISABLE_POLICY" ] || [ "$DISABLE_POLICY" = "false" ] || [ "$DISABLE_POLICY" = "0" ]; then
      ENABLE_POLICY="default"
    else
      ENABLE_POLICY="never"
      extra_args="${extra_args} --labels=k8s:io\\.kubernetes\\.pod\\.namespace "
    fi

    if [[ $extra_args != *"bpf-map-dynamic-size-ratio"* ]]; then
      extra_args="${extra_args} --bpf-map-dynamic-size-ratio=0.0025"
    fi

    if [ "$(terway_config_val 'cilium_enable_hubble' | tr '[:upper:]' '[:lower:]')" = "true" ]; then
      cilium_hubble_metrics=$(terway_config_val 'cilium_hubble_metrics')
      cilium_hubble_metrics=${cilium_hubble_metrics:="drop"}
      cilium_hubble_listen_address=$(terway_config_val 'cilium_hubble_listen_address')
      cilium_hubble_listen_address=${cilium_hubble_listen_address:=":4244"}
      cilium_hubble_metrics_server=$(terway_config_val 'cilium_hubble_metrics_server')
      cilium_hubble_metrics_server=${cilium_hubble_metrics_server:=":9091"}
      extra_args="${extra_args} --enable-hubble=true --hubble-disable-tls=true --hubble-metrics=${cilium_hubble_metrics}"
      extra_args="${extra_args} --hubble-listen-address=${cilium_hubble_listen_address} --hubble-metrics-server=${cilium_hubble_metrics_server}"
      echo "turning up hubble, passing args \"${extra_args}\""
    fi

    if [ "$IN_CLUSTER_LOADBALANCE" = "true" ]; then
      extra_args="${extra_args} --enable-in-cluster-loadbalance=true "
      echo "turning up in cluster loadbalance, passing args \"${extra_args}\""
    fi

    if bpftool -j feature probe | grep bpf_skb_ecn_set_ce; then
      extra_args="${extra_args} --enable-bandwidth-manager=true "
    fi

    echo "using cilium as network routing & policy"

    # shellcheck disable=SC2086
    exec cilium-agent --tunnel=disabled --enable-ipv4-masquerade=false --enable-ipv6-masquerade=false \
      --enable-policy=$ENABLE_POLICY \
      --agent-health-port=9099 --disable-envoy-version-check=true \
      --enable-local-node-route=false --ipv4-range=169.254.10.0/30 --ipv6-range=fe80:2400:3200:baba::/30 --enable-endpoint-health-checking=false \
      --enable-health-checking=false --enable-service-topology=true --disable-cnp-status-updates=true --k8s-heartbeat-timeout=0 --enable-session-affinity=true \
      --install-iptables-rules=false --enable-l7-proxy=false \
      --ipam=cluster-pool --datapath-mode=${datapath_mode} --enable-runtime-device-detection=true ${extra_args}
  fi
fi
# shellcheck disable=SC1091
source uninstall_policy.sh

# check kernel version

export FELIX_IPTABLESBACKEND=Auto
if (uname -r | grep -E "el7|an7" && [ "${KERNEL_MAJOR_VERSION}" -eq 3 ]) || (uname -r | grep -E "al7" && [ "${KERNEL_MAJOR_VERSION}" -eq 4 ]); then
  export FELIX_IPTABLESBACKEND=Legacy
elif (uname -r | grep -E "el8|an8" && [ "${KERNEL_MAJOR_VERSION}" -ge 4 ]) || (uname -r | grep -E "al8|lifsea8" && [ "${KERNEL_MAJOR_VERSION}" -ge 5 ]); then
  export FELIX_IPTABLESBACKEND=NFT

  # clean legacy rules if exist
  cleanup_legacy
fi

# default for veth
export FELIX_LOGSEVERITYSYS=none
export FELIX_LOGSEVERITYSCREEN=info
export CALICO_NETWORKING_BACKEND=none
export CLUSTER_TYPE=k8s,aliyun
export CALICO_DISABLE_FILE_LOGGING=true
# shellcheck disable=SC2154
export CALICO_IPV4POOL_CIDR="${Network}"
export FELIX_IPTABLESREFRESHINTERVAL="${IPTABLESREFRESHINTERVAL:-60}"
export FELIX_IPV6SUPPORT=true
export WAIT_FOR_DATASTORE=true
export IP=""
export NO_DEFAULT_POOLS=true
export FELIX_DEFAULTENDPOINTTOHOSTACTION=ACCEPT
export FELIX_HEALTHENABLED=true
export FELIX_LOGFILEPATH=/dev/null
export FELIX_BPFENABLED=false
export FELIX_XDPENABLED=false
export FELIX_BPFCONNECTTIMELOADBALANCINGENABLED=false
export FELIX_BPFKUBEPROXYIPTABLESCLEANUPENABLED=false
exec 2>&1
if [ -n "$NODENAME" ]; then
  export FELIX_FELIXHOSTNAME="$NODENAME"
fi
if [ -n "$DATASTORE_TYPE" ]; then
  export FELIX_DATASTORETYPE="$DATASTORE_TYPE"
fi

if [ "$network_policy_provider" = "ebpf" ]; then
  cleanup_felix
  # kernel version equal and above 4.19
  if { [ "$KERNEL_MAJOR_VERSION" -eq 4 ] && [ "$KERNEL_MINOR_VERSION" -ge 19 ]; } ||
    [ "$KERNEL_MAJOR_VERSION" -gt 4 ]; then

    extra_args=$(terway_config_val 'cilium_args')

    if [ -z "$DISABLE_POLICY" ] || [ "$DISABLE_POLICY" = "false" ] || [ "$DISABLE_POLICY" = "0" ]; then
      ENABLE_POLICY="default"
    else
      ENABLE_POLICY="never"
      extra_args="${extra_args} --labels=k8s:io\\.kubernetes\\.pod\\.namespace "
    fi

		if [ "$IN_CLUSTER_LOADBALANCE" = "true" ]; then
				extra_args="${extra_args} --enable-in-cluster-loadbalance=true "
				echo "turning up in cluster loadbalance, passing args \"${extra_args}\""
		fi

    # shellcheck disable=SC2086
    exec cilium-agent --kube-proxy-replacement=disabled --tunnel=disabled --enable-ipv4-masquerade=false --enable-ipv6-masquerade=false \
      --enable-policy=$ENABLE_POLICY \
      --agent-health-port=9099 --disable-envoy-version-check=true \
      --enable-local-node-route=false --ipv4-range=169.254.10.0/30 --ipv6-range=fe80:2400:3200:baba::/30 --enable-endpoint-health-checking=false \
      --enable-health-checking=false --enable-service-topology=true --disable-cnp-status-updates=true --k8s-heartbeat-timeout=0 --enable-session-affinity=true \
      --install-iptables-rules=false --enable-l7-proxy=false \
      --ipam=cluster-pool ${extra_args}
  else
    echo "unsupported kernel version"
    exit 1
  fi
else
  if [ -z "$DISABLE_POLICY" ] || [ "$DISABLE_POLICY" = "false" ] || [ "$DISABLE_POLICY" = "0" ]; then
    exec calico-felix
  fi
fi

config_masquerade
cleanup_felix
# for health check
if [ "$FELIX_HEALTHPORT" != "" ]; then
  # shellcheck disable=SC2016
  exec socat TCP-LISTEN:"$FELIX_HEALTHPORT",bind=127.0.0.1,fork,reuseaddr system:'sleep 2;kill -9 $SOCAT_PID 2>/dev/null'
else
  # shellcheck disable=SC2016
  exec socat TCP-LISTEN:9099,bind=127.0.0.1,fork,reuseaddr system:'sleep 2;kill -9 $SOCAT_PID 2>/dev/null'
fi
