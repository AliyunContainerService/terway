#!/bin/sh

init_node_bpf() {
  nsenter -t 1 -m -- bash -c '
  mount | grep "/sys/fs/bpf type bpf" || {
  # Mount the filesystem until next reboot
  echo "Mounting BPF filesystem..."
  mount bpffs /sys/fs/bpf -t bpf

  echo "Link information:"
  ip link

  echo "Routing table:"
  ip route

  echo "Addressing:"
  ip -4 a
  ip -6 a
#  date > /tmp/cilium-bootstrap-time
  echo "Node initialization complete"
}'
}
set -o errexit
set -o nounset

# install CNIs
cp -f /usr/bin/terway /opt/cni/bin/
cp -f /usr/bin/cilium-cni /opt/cni/bin/
chmod +x /opt/cni/bin/terway
chmod +x /opt/cni/bin/cilium-cni

# init cni config
cp /tmp/eni/eni_conf /etc/eni/eni.json

terway-cli cni /tmp/eni/10-terway.conflist /tmp/eni/10-terway.conf --output /etc/cni/net.d/10-terway.conflist
# remove legacy cni config
rm -f /etc/cni/net.d/10-terway.conf

# mount bpffs
ENIIP_VIRTUAL_TYPE=$(jq 'recurse|.eniip_virtual_type?' -r < /etc/cni/net.d/10-terway.conflist | grep -i ipvlan | tr '[:upper:]' '[:lower:]')
if [ "$ENIIP_VIRTUAL_TYPE" = "ipvlan" ]; then
      echo "Init node BPF"
      init_node_bpf
fi

node_capabilities=/var/run/eni/node_capabilities
if [ ! -f "$node_capabilities" ]; then
  echo "Init node capabilities"
  mkdir -p /var/run/eni
  touch "$node_capabilities"
fi

require_erdma=$(jq '.enable_erdma' -r < /etc/eni/eni.json)
if [ "$require_erdma" = "true" ]; then
  echo "Init erdma driver"
  if modprobe erdma; then
    echo "node support erdma"
    if ! grep -q "erdma=true" "$node_capabilities"; then
      sed -i '/erdma=/d' "$node_capabilities"
      echo "erdma=true" >> "$node_capabilities"
    fi
  else
    sed -i '/erdma=true/d' "$node_capabilities"
    echo "node not support erdma, pls install the latest erdma driver"
  fi
fi


sysctl -w net.ipv4.conf.eth0.rp_filter=0
modprobe sch_htb || true
chroot /host sh -c "systemctl disable eni.service; rm -f /etc/udev/rules.d/75-persistent-net-generator.rules /lib/udev/rules.d/60-net.rules /lib/udev/rules.d/61-eni.rules /lib/udev/write_net_rules"
