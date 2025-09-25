# Idle IP Reclaim Policy

The idle IP reclaim policy allows Terway to automatically reclaim idle IP addresses from the IP pool after a configurable period of inactivity. This feature helps optimize resource utilization by freeing up unused IP addresses.

## Configuration

The idle IP reclaim policy can be configured through the `eni-config` ConfigMap or node-specific dynamic configurations. The following parameters control the reclaim behavior:

| Parameter | Description | Default Value | Example |
|-----------|-------------|---------------|---------|
| `idle_ip_reclaim_after` | Duration an IP must be idle before becoming eligible for reclamation | - | `"30m"`, `"1h"` |
| `idle_ip_reclaim_interval` | Time interval between reclaim checks | `"10m"` | `"15m"`, `"30m"` |
| `idle_ip_reclaim_batch_size` | Maximum number of IPs to reclaim in a single batch | `5` | `3`, `10` |
| `idle_ip_reclaim_jitter_factor` | Jitter factor to randomize reclaim timing (0.0-1.0) | `"0.1"` | `"0.2"`, `"0.5"` |

## How It Works

1. **Idle Detection**: The system monitors IP usage and tracks when IPs were last allocated or used.

2. **Eligibility Check**: After the `idle_ip_reclaim_after` duration has passed since the last pool modification, IPs become eligible for reclamation.

3. **Batch Reclamation**: During each reclaim check (every `idle_ip_reclaim_interval`), up to `idle_ip_reclaim_batch_size` idle IPs are reclaimed, ensuring the pool doesn't drop below `min_pool_size`.

4. **Jitter Prevention**: The `jitter_factor` introduces randomness in reclaim timing to prevent thundering herd problems across multiple nodes.

## Example Configuration

```json
{
  "version": "1",
  "max_pool_size": 10,
  "min_pool_size": 2,
  "idle_ip_reclaim_after": "30m",
  "idle_ip_reclaim_interval": "10m",
  "idle_ip_reclaim_batch_size": 3,
  "idle_ip_reclaim_jitter_factor": "0.1"
}
```

In this example:
- IPs become eligible for reclamation 30 minutes after the last pool modification
- Reclaim checks occur every 10 minutes (with ±10% jitter)
- Up to 3 IPs are reclaimed per check
- The minimum pool size of 2 is always maintained

## Behavior Notes

- The reclaim policy only activates when `idle_ip_reclaim_after` is configured
- Pool modifications (new IP allocations) reset the reclaim timer
- Reclamation respects both `max_pool_size` and `min_pool_size` boundaries
- The feature works independently of normal pool size management
