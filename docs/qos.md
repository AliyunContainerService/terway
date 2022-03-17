# QoS

Traffic control mainly have two aspect

- Traffic Shaping
- Traffic Priority

| Terway Mode                     | Egress Shaping | Ingress Shaping | Egress priority |
|---------------------------------| -------------- | -------------- | --------------- |
| vpc mode                        | ☑️             | ☑️             | -               |
| shared eni (eniip)              | ☑️             | ☑️             | ☑️              |
| shared eni (eniip)+ IPvlan eBPF | -              | -              | ☑️              |
| exclusive eni                   | -              | -              | -               |
| trunking                        | -              | -              | -               |

## shaping

| Annotation                             | Mean             |
| -------------------------------------- | ---------------- |
| `k8s.aliyun.com/ingress-bandwidth: 1m` | ingress banwidth |
| `k8s.aliyun.com/egress-bandwidth: 1m`  | egress banwidth  |

## priority

We have three annotations available for pod, to control different priority.

| Annotation                                       | Mean                          | bands |
| ------------------------------------------------ |-------------------------------| ----- |
| `k8s.aliyun.com/network-priority: "best-effort"` | normal traffic                | 1     |
| `k8s.aliyun.com/network-priority: "burstable"`   | for high throughput service   | 2     |
| `k8s.aliyun.com/network-priority: "guaranteed"`  | for latency sensitive service | 0     |

When priority is set, a `priority qdisc` is set to the `eni` related to pod.  
Pod egress traffic will classify into different bands based on the config.  
For more info about how priority works , please refer to [tc-prio](https://man7.org/linux/man-pages/man8/tc-prio.8.html)
.
