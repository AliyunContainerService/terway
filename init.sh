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

sysctl -w net.ipv4.conf.eth0.rp_filter=0
modprobe sch_htb || true
chroot /host sh -c "systemctl disable eni.service; rm -f /etc/udev/rules.d/75-persistent-net-generator.rules /lib/udev/rules.d/60-net.rules /lib/udev/rules.d/61-eni.rules /lib/udev/write_net_rules"