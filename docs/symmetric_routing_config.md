# Symmetric Routing Configuration

## Overview

Terway supports symmetric routing configuration through the `symmetric_routing_config` field in the CNI configuration.
This allows users to customize the mark, mask, table ID, rule priority, and comment values used for symmetric routing.

## Configuration

The `symmetric_routing_config` is an optional field in the terway CNI configuration. If not provided, default values
will be used.
When using `dataPathv2`, SNAT to node IP is required for proper functionality.

### Default Values

- `interface`: "eth0"
- `mark`: 0x10 (16)
- `mask`: 0x10 (16)
- `table_id`: 100
- `rule_priority`: 600
- `comment`: "terway-symmetric"
- `backend`: "iptables"

### Configuration Structure

```json
{
  "symmetric_routing_config": {
    "interface": "eth0",
    "mark": 32,
    "mask": 32,
    "table_id": 100,
    "rule_priority": 600,
    "comment": "custom-terway-symmetric",
    "backend": "nftables"
  }
}
```

### Field Descriptions

- `interface`: The interface name used for symmetric routing (string)
- `mark`: The fwmark value used for symmetric routing (integer)
- `mask`: The fwmark mask value used for symmetric routing (integer)
- `table_id`: The routing table ID used for symmetric routing (integer)
- `rule_priority`: The priority of the ip rule (integer)
- `comment`: The comment for iptables rules (string)
- `backend`: The firewall backend used for symmetric routing rules (string, "iptables" or "nftables", default "iptables")

## Example Configuration

### Basic Configuration

```json
{
  "cniVersion": "0.4.0",
  "name": "terway",
  "type": "terway",
  "symmetric_routing": true,
  "symmetric_routing_config": {
    "mark": 32,
    "mask": 32,
    "table_id": 100,
    "rule_priority": 600,
    "comment": "terway-symmetric"
  }
}
```

## Usage Notes

1. The `symmetric_routing` field must be set to `true` for the configuration to take effect.
2. All fields in `symmetric_routing_config` are optional.
3. Only positive integer values are accepted for numeric fields.
4. The configuration applies to both IPv4 and IPv6 when dual-stack is enabled.
5. Changes to the configuration require restarting the terway daemon to take effect.

## Validation

The configuration is validated when the terway daemon starts:

- Numeric fields must be positive integers
- String fields must not be empty if provided
- Invalid configurations will cause the daemon to fail to start

## Troubleshooting

If symmetric routing is not working as expected:

1. Check that `symmetric_routing` is set to `true`
2. Verify the configuration values are valid
3. Check the terway daemon logs for any configuration errors
4. Ensure the mark/mask values don't conflict with other network policies
