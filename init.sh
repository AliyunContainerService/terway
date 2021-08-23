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

setup_networkmanager() {
  nsenter -t 1 -m -- tee /tmp/setup_network.sh<<EOF
set -x

config_network_manager() {
  echo "setup rh NetworkManager"
  if [ -f "/usr/lib/NetworkManager/conf.d/eni.conf" ]; then
    return
  fi
  tee /usr/lib/NetworkManager/conf.d/eni.conf<<EOF2
[main]
plugins = ifcfg-rh,keyfile

[keyfile]
unmanaged-devices=interface-name:eth*, except:interface-name:eth0

[logging]

EOF2
  systemctl reload NetworkManager
}

if [[ "\$(systemctl is-active NetworkManager)" == "active" ]]; then
  mkdir -p "/usr/lib/NetworkManager/conf.d/"
  OS_ID=\$(awk -F= '\$1=="ID" { print \$2 ;}' /etc/os-release)
  VERSION_ID=\$(awk -F= '\$1=="VERSION_ID" { print \$2 ;}' /etc/os-release)

  echo "detect os \${OS_ID} version \${VERSION_ID}"
  if [[ "\$OS_ID" == *alinux* && "\$VERSION_ID" == "\"3\"" ]]; then
    config_network_manager
  fi

  if [[ "\$OS_ID" == *centos* && "\$VERSION_ID" == "\"8\"" ]]; then
    config_network_manager
  fi
fi

EOF
  nsenter -t 1 -m -- chmod +x /tmp/setup_network.sh
  nsenter -t 1 -m -- bash -c /tmp/setup_network.sh
}


set -o errexit
set -o nounset

# install CNIs
cp -f /usr/bin/terway /opt/cni/bin/
cp -f /usr/bin/cilium-cni /opt/cni/bin/
chmod +x /opt/cni/bin/terway
chmod +x /opt/cni/bin/cilium-cni

cp /etc/eni/10-terway.conf /etc/cni/net.d/
ENIIP_VIRTUAL_TYPE=$(jq .eniip_virtual_type? -r < /etc/eni/10-terway.conf | tr '[:upper:]' '[:lower:]')

if [ "$ENIIP_VIRTUAL_TYPE" = "ipvlan" ]; then
  # check kernel version & enable cilium
  KERNEL_MAJOR_VERSION=$(uname -r | awk -F . '{print $1}')
  KERNEL_MINOR_VERSION=$(uname -r | awk -F . '{print $2}')
  # kernel version equal and above 4.19
  if { [ "$KERNEL_MAJOR_VERSION" -eq 4 ] && [ "$KERNEL_MINOR_VERSION" -ge 19 ]; } ||
     [ "$KERNEL_MAJOR_VERSION" -gt 4 ]; then
    echo "Init node BPF"
    init_node_bpf
    echo "Creating 10-terway.conflist"
    jq '
{
  "cniVersion": "0.3.1",
  "name": "terway-chainer",
  "plugins": [
      del(.name,.cniVersion),
      {
         "type": "cilium-cni"
      }
   ]
}' < /etc/eni/10-terway.conf > /etc/cni/net.d/10-terway.conflist

  rm -f /etc/cni/net.d/10-terway.conf || true
  else
    echo "Linux kernel version <= 4.19, skipping cilium config"
  fi
else
  rm -f /etc/cni/net.d/10-terway.conflist || true
fi

sysctl -w net.ipv4.conf.eth0.rp_filter=0
modprobe sch_htb || true
chroot /host sh -c "systemctl disable eni.service; rm -f /etc/udev/rules.d/75-persistent-net-generator.rules /lib/udev/rules.d/60-net.rules /lib/udev/rules.d/61-eni.rules /lib/udev/write_net_rules && udevadm control --reload-rules && udevadm trigger"

setup_networkmanager