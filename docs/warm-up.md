# IP Warm-Up Configuration

The IP warm-up feature allows Terway to pre-allocate a specified number of IP addresses when a node starts up. This helps reduce pod startup latency by having IPs ready in the pool before pods are scheduled.

## Configuration

The warm-up size can be configured through the `eni-config` ConfigMap:

| Parameter | Description | Default Value | Example |
|-----------|-------------|---------------|---------|
| `ip_warm_up_size` | Number of IPs to pre-allocate during node warm-up | `0` (disabled) | `10`, `20` |

## How It Works

1. **Initialization**: When a node starts and `ip_warm_up_size` is configured (> 0), the controller initializes the warm-up state:
   - `WarmUpTarget`: Set to the configured `ip_warm_up_size`
   - `WarmUpAllocatedCount`: Tracks the number of IPs allocated via OpenAPI during warm-up
   - `WarmUpCompleted`: Set to `false` initially

2. **Allocation**: During reconciliation, if warm-up is not completed, the controller calculates additional IP demand to reach the warm-up target and allocates IPs accordingly.

3. **Completion**: Warm-up is marked as completed when `WarmUpAllocatedCount >= WarmUpTarget`. Once completed, the warm-up process will not run again for that node.

## Relationship with Pool Size

**Important**: The `ip_warm_up_size` is independent of `min_pool_size` and `max_pool_size`. It can be set to a value larger or smaller than the pool size limits.

However, setting `ip_warm_up_size` larger than `max_pool_size` is **not recommended** because:

- The warm-up process will allocate IPs up to `ip_warm_up_size`
- After warm-up completes, the pool management and idle IP reclaim policy will release excess IPs to maintain the pool within `min_pool_size` and `max_pool_size` boundaries
- This results in unnecessary IP allocation and deallocation, wasting resources and API calls

### Recommended Configuration

```json
{
  "version": "1",
  "max_pool_size": 20,
  "min_pool_size": 5,
  "ip_warm_up_size": 15
}
```

In this example:

- On node startup, 15 IPs will be pre-allocated (warm-up)
- The pool will maintain between 5-20 IPs during normal operation
- Since `ip_warm_up_size` (15) is within the pool size range (5-20), all pre-allocated IPs will be retained

### Not Recommended Configuration

```json
{
  "version": "1",
  "max_pool_size": 10,
  "min_pool_size": 2,
  "ip_warm_up_size": 20
}
```

In this example:

- On node startup, 20 IPs will be pre-allocated (warm-up)
- After warm-up completes, 10 IPs will be released because `max_pool_size` is only 10
- This causes unnecessary IP churn and API calls

## Status Fields

The warm-up progress can be monitored through the Node CR status:

| Field | Description |
|-------|-------------|
| `warmUpTarget` | The target number of IPs to allocate during warm-up |
| `warmUpAllocatedCount` | Current count of IPs allocated via OpenAPI during warm-up |
| `warmUpCompleted` | Whether warm-up has been completed |

Example status:

```yaml
status:
  warmUpTarget: 10
  warmUpAllocatedCount: 10
  warmUpCompleted: true
```

## Use Cases

1. **Batch Job Scheduling**: When scheduling many pods simultaneously on a new node, pre-allocated IPs reduce waiting time.

2. **Auto-scaling**: New nodes in auto-scaling groups can have IPs ready before workloads are scheduled.

3. **Low-latency Requirements**: Applications requiring fast pod startup benefit from having IPs pre-allocated.

## Notes

- Warm-up only runs once per node lifecycle (when the node first joins the cluster)
- If a node already has warm-up status initialized, changing `ip_warm_up_size` will not affect the ongoing warm-up
- Warm-up progress is tracked independently of actual IP usage, ensuring consistent behavior even if IPs are allocated/deallocated during warm-up
