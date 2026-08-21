#!/bin/sh

masq_eni_only() {
  if ! "$1" -t nat -L terway-masq; then
    # Create a new chain in nat table.
    "$1" -t nat -N terway-masq || return 1
  fi

  if ! "$1" -t nat -L POSTROUTING | grep -q terway-masq; then
    # Append that chain to POSTROUTING table.
    "$1" -t nat -A POSTROUTING -m comment --comment "terway:masq-outgoing" ! -o lo -j terway-masq || return 1
  fi

  # The host-side peer for an exclusive ENI Pod is a cali* veth with no IP
  # address.  Do not let node-to-Pod traffic reach MASQUERADE: its address
  # selection may otherwise fall back to an unrelated host interface.
  # Normalize legacy, duplicate, or misplaced bypass rules. Leave a correct
  # steady-state chain untouched so there is no transient MASQUERADE window.
  managed_rule='-A terway-masq -o cali+ -m comment --comment "terway:masq-pod-bypass" -j RETURN'
  legacy_rule='-A terway-masq -o cali+ -j RETURN'
  rules=$("$1" -t nat -S terway-masq 2>/dev/null) || return 1
  first_rule=$(printf '%s\n' "$rules" | sed -n '2p')
  managed_count=$(printf '%s\n' "$rules" | grep -Fxc -- "$managed_rule")
  legacy_count=$(printf '%s\n' "$rules" | grep -Fxc -- "$legacy_rule")
  if [ "$first_rule" != "$managed_rule" ] || [ "$managed_count" -ne 1 ] || [ "$legacy_count" -ne 0 ]; then
    while "$1" -t nat -C terway-masq -o 'cali+' -j RETURN >/dev/null 2>&1; do
      "$1" -t nat -D terway-masq -o 'cali+' -j RETURN || return 1
    done
    while "$1" -t nat -C terway-masq -o 'cali+' -m comment --comment "terway:masq-pod-bypass" -j RETURN >/dev/null 2>&1; do
      "$1" -t nat -D terway-masq -o 'cali+' -m comment --comment "terway:masq-pod-bypass" -j RETURN || return 1
    done
    "$1" -t nat -I terway-masq 1 -o 'cali+' -m comment --comment "terway:masq-pod-bypass" -j RETURN || return 1
  fi

  if ! "$1" -t nat -L terway-masq | grep -q MASQUERADE; then
    "$1" -t nat -A terway-masq -j MASQUERADE || return 1
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
