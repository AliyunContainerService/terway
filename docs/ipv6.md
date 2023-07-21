# 开启 IPv6 双栈

## 配置

启用双栈模式，需要在 terway 配置中配置 `ip_stack` 为 `dual`，并重启 Terway Pods

```yaml
❯ kubectl get cm -n kube-system eni-config -oyaml
apiVersion: v1
data:
  10-terway.conf: |
    {
      "cniVersion": "0.4.0",
      "name": "terway",
      "eniip_virtual_type": "IPVlan",
      "ip_stack": "dual",   <----- 启用双栈支持
      "type": "terway"
    }
  disable_network_policy: "false"
  eni_conf: |
    {
      "min_pool_size": 0
      ...
    }
kind: ConfigMap
```

确保 Terway 使用的 RAM 包含下面 `RAM` 权限

```json
{
  "Version": "1",
  "Statement": [{
    "Action": [
      "ecs:AssignIpv6Addresses",
      "ecs:UnassignIpv6Addresses"
    ],
    "Resource": [
      "*"
    ],
    "Effect": "Allow"
  }
}
```
