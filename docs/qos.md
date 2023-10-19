# QoS

Traffic control mainly have two aspect

- Traffic Shaping
- Traffic Priority

| Terway Mode                     | Egress Shaping | Ingress Shaping | Egress priority |
|---------------------------------|----------------|-----------------|-----------------|
| vpc mode                        | ☑️             | ☑️              | -               |
| shared eni (eniip)              | ☑️             | ☑️              | ☑️              |
| shared eni (eniip)+ IPvlan eBPF | ☑️             | -               | ☑️              |
| exclusive eni                   | -              | -               | -               |
| trunking                        | -              | -               | -               |

## shaping

| Annotation                             | Mean             |
|----------------------------------------|------------------|
| `kubernetes.io/ingress-bandwidth: 10M` | ingress banwidth |
| `kubernetes.io/egress-bandwidth: 10M`  | egress banwidth  |

### config shaping

to enable shaping, follow config need to add in `eni-config`

```yaml
# kubectl edit cm -n kube-system eni-config
apiVersion: v1
data:
  10-terway.conf: |
    {
      "cniVersion": "0.4.0",
      "name": "terway",
      "capabilities": {"bandwidth": true}, # add 
      "type": "terway"
    }
```

## priority

We have three annotations available for pod, to control different priority.

| Annotation                                       | Mean                          | bands |
|--------------------------------------------------|-------------------------------|-------|
| `k8s.aliyun.com/network-priority: "guaranteed"`  | for latency sensitive service | 0     |
| `k8s.aliyun.com/network-priority: "burstable"`   | normal traffic                | 1     |
| `k8s.aliyun.com/network-priority: "best-effort"` | for lowest priority           | 2     |

When priority is set, a `priority qdisc` is set to the `eni` related to pod.  
Pod egress traffic will classify into different bands based on the config.  
For more info about how priority works , please refer to [tc-prio](https://man7.org/linux/man-pages/man8/tc-prio.8.html)
.

> note: default qdisc will be replaced

### config priority

to enable priority, follow config need to add in `eni-config`

```yaml
# kubectl edit cm -n kube-system eni-config
apiVersion: v1
data:
  10-terway.conf: |
    {
      "cniVersion": "0.4.0",
      "name": "terway",
      "enable_network_priority": true, # add
      "type": "terway"
    }
```
