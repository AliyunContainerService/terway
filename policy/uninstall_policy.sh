#!/bin/sh

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

cleanup_rules(){
  # Set FORWARD action to ACCEPT so outgoing packets can go through POSTROUTING chains.
  echo "Setting default FORWARD action to ACCEPT..."
  "$1" -P FORWARD ACCEPT

  echo "Starting the flush Calico policy rules..."
  echo "Make sure calico-node DaemonSet is stopped before this gets executed."

  echo "Flushing all the calico iptables chains in the nat table..."
  "$1"-save -t nat | grep -oP '(?<!^:)cali-[^ ]+' | while read -r line; do "$1" -t nat -F "$line"; done

  echo "Flushing all the calico iptables chains in the raw table..."
  "$1"-save -t raw | grep -oP '(?<!^:)cali-[^ ]+' | while read -r line; do "$1" -t raw -F "$line"; done

  echo "Flushing all the calico iptables chains in the mangle table..."
  "$1"-save -t mangle | grep -oP '(?<!^:)cali-[^ ]+' | while read -r line; do "$1" -t mangle -F "$line"; done

  echo "Flushing all the calico iptables chains in the filter table..."
  "$1"-save -t filter | grep -oP '(?<!^:)cali-[^ ]+' | while read -r line; do "$1" -t filter -F "$line"; done

  echo "Cleaning up calico rules from the nat table..."
  "$1"-save -t nat | grep -e '--comment "cali:' | cut -c 3- | sed 's/^ *//;s/ *$//' | xargs -l1 "$1" -t nat -D

  echo "Cleaning up calico rules from the raw table..."
  "$1"-save -t raw | grep -e '--comment "cali:' | cut -c 3- | sed 's/^ *//;s/ *$//' | xargs -l1 "$1" -t raw -D

  echo "Cleaning up calico rules from the mangle table..."
  "$1"-save -t mangle | grep -e '--comment "cali:' | cut -c 3- | sed 's/^ *//;s/ *$//' | xargs -l1 "$1" -t mangle -D

  echo "Cleaning up calico rules from the filter table..."
  "$1"-save -t filter | grep -e '--comment "cali:' | cut -c 3- | sed 's/^ *//;s/ *$//' | xargs -l1 "$1" -t filter -D
}

cleanup_felix() {
    # Make sure ip_forward sysctl is set to allow ip forwarding.
    sysctl -w net.ipv4.ip_forward=1
    for iptables in 'iptables' 'ip6tables';do
      cleanup_rules ${iptables}
    done
    cleanup_legacy
}

cleanup_legacy() {
  if which iptables-legacy >/dev/null; then
    cleanup_rules iptables-legacy
  fi
}