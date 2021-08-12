
```yaml
❯ kubectl get cm -n kube-system eni-config -oyaml
apiVersion: v1
data:
  10-terway.conf: |
    {
      "cniVersion": "0.3.0",
      "name": "terway",
      "eniip_virtual_type": "IPVlan",
      "ip_stack": "dual",   <----- 启用双栈支持
      "type": "terway"
    }
  disable_network_policy: "false"
  eni_conf: |
    {
      "min_pool_size": 0,
      "ip_stack": "dual",   <----- 启用双栈支持
      ...
    }
kind: ConfigMap
```


https://ram.console.aliyun.com/roles/AliyunCSManagedNetworkRole?spm=a2c8b.12215514.permissionlist.principal_detail.5e07336ann7etp
terway-controlplane 需要授权中包含以下 [`RAM 权限`](https://ram.console.aliyun.com/)

```json
{
  "Version": "1",
  "Statement": [{
    "Action": [
      "ecs:AssignIpv6Addresses"  <--- new
    ],
    "Resource": [
      "*"
    ],
    "Effect": "Allow"
  }
}
```