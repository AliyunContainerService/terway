#!/bin/sh
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
    if [ -z "$DISABLE_POLICY" ] || [ x"$DISABLE_POLICY" = x"false" ] || [ x"$DISABLE_POLICY" = x"0" ]; then
      ENABLE_POLICY="default"
    else
      ENABLE_POLICY="never"
    fi

    extra_args=""
    if [ "$(terway_config_val 'cilium_enable_hubble' | tr '[:upper:]' '[:lower:]')" = "true" ]; then
      cilium_hubble_metrics=$(terway_config_val 'cilium_hubble_metrics')
      cilium_hubble_metrics=${cilium_hubble_metrics:="drop"}
      cilium_hubble_listen_address=$(terway_config_val 'cilium_hubble_listen_address')
      cilium_hubble_listen_address=${cilium_hubble_listen_address:=":4244"}
      cilium_hubble_metrics_server=$(terway_config_val 'cilium_hubble_metrics_server')
      cilium_hubble_metrics_server=${cilium_hubble_metrics_server:=":9091"}
      extra_args="${extra_args} --enable-hubble=true --hubble-metrics=${cilium_hubble_metrics}"
      extra_args="${extra_args} --hubble-listen-address=${cilium_hubble_listen_address} --hubble-metrics-server=${cilium_hubble_metrics_server}"
      echo "turning up hubble, passing args \"${extra_args}\""
    fi

    echo "using cilium as network routing & policy"
    # shellcheck disable=SC2086
    exec cilium-agent --tunnel=disabled --enable-ipv4-masquerade=false --enable-ipv6-masquerade=false \
         --enable-ipv6=false --enable-policy=$ENABLE_POLICY \
         --agent-health-port=9099 --disable-envoy-version-check=true \
         --enable-local-node-route=false --ipv4-range=169.254.10.0/30 --enable-endpoint-health-checking=false \
         --ipam=cluster-pool --bpf-map-dynamic-size-ratio=0.0025 ${extra_args}
  fi
fi

  # default for veth
  export FELIX_LOGSEVERITYSYS=none
  export FELIX_LOGSEVERITYSCREEN=info
  export CALICO_NETWORKING_BACKEND=none
  export CLUSTER_TYPE=k8s,aliyun
  export CALICO_DISABLE_FILE_LOGGING=true
  # shellcheck disable=SC2154
  export CALICO_IPV4POOL_CIDR=${Network}
  export FELIX_IPTABLESREFRESHINTERVAL=${IPTABLESREFRESHINTERVAL:-60}
  export FELIX_IPV6SUPPORT=false
  export WAIT_FOR_DATASTORE=true
  export IP=""
  export NO_DEFAULT_POOLS=true
  export FELIX_DEFAULTENDPOINTTOHOSTACTION=ACCEPT
  export FELIX_HEALTHENABLED=true
  export FELIX_LOGFILEPATH=/dev/null
  exec 2>&1
  if [ ! -z "$NODENAME" ]; then
      export FELIX_FELIXHOSTNAME=$NODENAME
  fi
  if [ ! -z $DATASTORE_TYPE ]; then
      export FELIX_DATASTORETYPE=$DATASTORE_TYPE
  fi

  if [ -z "$DISABLE_POLICY" ] || [ x"$DISABLE_POLICY" = x"false" ] || [ x"$DISABLE_POLICY" = x"0" ]; then
      exec calico-felix
  else
      exec uninstall_policy.sh
  fi
