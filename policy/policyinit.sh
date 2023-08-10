#!/bin/bash

mount -o remount rw /proc/sys

export DATASTORE_TYPE=kubernetes
if [ "$DATASTORE_TYPE" = "kubernetes" ]; then
    if [ -z "$KUBERNETES_SERVICE_HOST" ]; then
        echo "can not found k8s apiserver service env, exiting"
        exit 1
    fi
    return_code="$(curl -k -o /dev/null -I -L -s -w "%{http_code}" https://"${KUBERNETES_SERVICE_HOST}":"${KUBERNETES_SERVICE_PORT:-443}")"
    if [ "$return_code" -ne 403 ]&&[ "$return_code" -ne 200 ]&&[ "$return_code" -ne 201 ];then
        echo "can not access kubernetes service, exiting"
        exit 1
    fi
fi

terway_config_val() {
  config_key="$1"
  if [ ! -f /etc/cni/net.d/10-terway.conflist ]; then return; fi
  jq -r ".plugins[] | select(.type == \"terway\") | .${config_key}" /etc/cni/net.d/10-terway.conflist | sed 's/^null$//'
}

# kernel version has already checked in initContainer, so just determine whether plugin chaining exists
if [ "$(terway_config_val 'eniip_virtual_type' | tr '[:upper:]' '[:lower:]')" = "ipvlan" ]; then
  # check kernel version & enable cilium
  KERNEL_MAJOR_VERSION=$(uname -r | awk -F . '{print $1}')
  KERNEL_MINOR_VERSION=$(uname -r | awk -F . '{print $2}')
  # kernel version equal and above 4.19
  if { [ "$KERNEL_MAJOR_VERSION" -eq 4 ] && [ "$KERNEL_MINOR_VERSION" -ge 19 ]; } ||
     [ "$KERNEL_MAJOR_VERSION" -gt 4 ]; then
    if [ -z "$DISABLE_POLICY" ] || [ "$DISABLE_POLICY" = "false" ] || [ "$DISABLE_POLICY" = "0" ]; then
      ENABLE_POLICY="default"
    else
      ENABLE_POLICY="never"
    fi

    extra_args=$(terway_config_val 'cilium_args')
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

		if bpftool -j feature probe | grep bpf_skb_ecn_set_ce ; then
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
         --ipam=cluster-pool ${extra_args}
  fi
fi
  # shellcheck disable=SC1091
  source uninstall_policy.sh

  # check kernel version
  KERNEL_MAJOR_VERSION=$(uname -r | awk -F . '{print $1}')

  export FELIX_IPTABLESBACKEND=Auto
  if ( uname -r | grep -E "el7|an7" && [ "${KERNEL_MAJOR_VERSION}" -eq 3 ] ) || ( uname -r | grep -E "al7" && [ "${KERNEL_MAJOR_VERSION}" -eq 4 ] ); then
    export FELIX_IPTABLESBACKEND=Legacy
  elif ( uname -r | grep -E "el8|an8" && [ "${KERNEL_MAJOR_VERSION}" -ge 4 ] ) || ( uname -r | grep -E "al8|lifsea8" && [ "${KERNEL_MAJOR_VERSION}" -ge 5 ] ); then
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

  if [ -z "$DISABLE_POLICY" ] || [ "$DISABLE_POLICY" = "false" ] || [ "$DISABLE_POLICY" = "0" ]; then
      exec calico-felix
  else
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
  fi
