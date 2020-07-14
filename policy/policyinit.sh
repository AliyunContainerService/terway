#!/bin/sh
export DATASTORE_TYPE=kubernetes
if [ "$DATASTORE_TYPE" = "kubernetes" ]; then
    if [ -z "$KUBERNETES_SERVICE_HOST" ]; then
        echo "can not found k8s apiserver service env, exiting"
        exit 1
    fi
fi

# kernel version has already checked in initContainer, so just determine whether plugin chaining exists
if [ -f "/etc/cni/net.d/10-terway.conflist" ]; then
    if [ -z "$DISABLE_POLICY" ] || [ x"$DISABLE_POLICY" = x"false" ] || [ x"$DISABLE_POLICY" = x"0" ]; then
      ENABLE_POLICY="default"
    else
      ENABLE_POLICY="never"
    fi

    echo "using cilium as network routing & policy"
    exec cilium-agent --tunnel=disabled --masquerade=false --enable-ipv6=false --enable-policy=$ENABLE_POLICY \
         --agent-health-port=9099 --disable-envoy-version-check=true \
         --enable-local-node-route=false --ipv4-range=169.254.0.0/16
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
