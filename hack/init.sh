#!/bin/sh

set -o errexit
set -o nounset

# install CNIs
cp -f /usr/bin/terway /opt/cni/bin/
chmod +x /opt/cni/bin/terway

cp -f /usr/bin/cilium-cni /opt/cni/bin/
chmod +x /opt/cni/bin/cilium-cni

EXTRA_FLAGS="${1:-}"

# init cni config
terway-cli cni --output /etc/cni/net.d/10-terway.conflist "$EXTRA_FLAGS"
terway-cli nodeconfig "$EXTRA_FLAGS"
cat /etc/cni/net.d/10-terway.conflist

node_capabilities=/var/run/eni/node_capabilities
if [ ! -f "$node_capabilities" ]; then
  echo "Init node capabilities"
  mkdir -p /var/run/eni
  touch "$node_capabilities"
fi

require_erdma=$(jq '.enable_erdma' -r </etc/eni/eni_conf)
if [ "$require_erdma" = "true" ]; then
  echo "Init erdma driver"
  if modprobe erdma; then
    echo "node support erdma"
    echo "erdma = true" >>"$node_capabilities"
    if ! grep -q "erdma *= *true" "$node_capabilities"; then
      sed -i '/erdma *=/d' "$node_capabilities"
      echo "erdma = true" >> "$node_capabilities"
    fi
  else
    sed -i '/erdma *= *true/d' "$node_capabilities"
    echo "node not support erdma, pls install the latest erdma driver"
  fi
fi

# copy node capabilities to tmpfs so policy container can read it
cp $node_capabilities /var-run-eni/node_capabilities
cat $node_capabilities
sysctl -w net.ipv4.conf.eth0.rp_filter=0
modprobe sch_htb || true

if grep -qE '\bkube_proxy_replacement\s*=\s*true\b' "$node_capabilities"; then
  mkdir -p 0755 /host/var/run/cilium/cgroupv2
  cp -f /bin/cilium-mount /host/opt/cni/bin/cilium-mount
  nsenter --cgroup=/host/proc/1/ns/cgroup --mount=/host/proc/1/ns/mnt /opt/cni/bin/cilium-mount /var/run/cilium/cgroupv2;
  rm -f /host/opt/cni/bin/cilium-mount
fi

set +o errexit

chroot /host systemctl disable eni.service
chroot /host rm -f /etc/udev/rules.d/75-persistent-net-generator.rules /lib/udev/rules.d/60-net.rules /lib/udev/rules.d/61-eni.rules /lib/udev/write_net_rules
