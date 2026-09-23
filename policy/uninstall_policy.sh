#!/bin/sh

masq_eni_only() {
  # $1 = iptables flavour (iptables or ip6tables)
  # $2 = node address used as the SNAT source for same-node Service traffic
  if [ -z "$2" ]; then
    echo "masq_eni_only: missing node address argument" >&2
    return 1
  fi

  if ! "$1" -t nat -L terway-masq; then
    # Create a new chain in nat table.
    "$1" -t nat -N terway-masq || return 1
  fi

  if ! "$1" -t nat -L POSTROUTING | grep -q terway-masq; then
    # Append that chain to POSTROUTING table.
    "$1" -t nat -A POSTROUTING -m comment --comment "terway:masq-outgoing" ! -o lo -j terway-masq || return 1
  fi

  # On eniOnly nodes, cali* peers lead to local exclusive-ENI Pods. SNAT
  # makes replies to same-node Service traffic return through this host.
  # Use the node address explicitly: these peers have no address for
  # MASQUERADE to select. No per-Pod state is needed for this rule.
  # Preserve the bypass rule after SNAT when upgrading existing chains.
  snat_id='--comment "terway:eni-only-snat"'
  return_id='--comment "terway:masq-pod-bypass"'

  rules=$("$1" -t nat -S terway-masq 2>/dev/null) || return 1
  first_rule=$(printf '%s\n' "$rules" | sed -n '2p')
  second_rule=$(printf '%s\n' "$rules" | sed -n '3p')
  snat_count=$(printf '%s\n' "$rules" | grep -c -- "$snat_id")
  return_count=$(printf '%s\n' "$rules" | grep -c -- "$return_id")

  snat_rule="-A terway-masq -o cali+ -m comment $snat_id -j SNAT --to-source $2"
  return_rule="-A terway-masq -o cali+ -m comment $return_id -j RETURN"

  if [ "$first_rule" != "$snat_rule" ] || [ "$second_rule" != "$return_rule" ] || \
    [ "$snat_count" -ne 1 ] || [ "$return_count" -ne 1 ]; then
    # Delete every managed rule sitting at the head (positional delete), then
    # rebuild the ordered pair. Matching only on the comment marker self-heals
    # a changed node address (whose rendered SNAT text differs) and clears any
    # duplicate or misordered rule, converging to [SNAT, RETURN].
    while :; do
      case "$first_rule" in
        *"$snat_id"* | *"$return_id"*) ;;
        *) break ;;
      esac
      "$1" -t nat -D terway-masq 1 || return 1
      rules=$("$1" -t nat -S terway-masq 2>/dev/null) || return 1
      first_rule=$(printf '%s\n' "$rules" | sed -n '2p')
    done
    "$1" -t nat -I terway-masq 1 -o 'cali+' \
      -m comment --comment "terway:eni-only-snat" -j SNAT --to-source "$2" || return 1
    "$1" -t nat -I terway-masq 2 -o 'cali+' -m comment --comment "terway:masq-pod-bypass" -j RETURN || return 1
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
