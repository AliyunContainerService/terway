# Terway ECS节点

## RAM

确保Terway 使用的凭据，具备下面的 RAM 权限：

Terway需要授权中包含以下 [`RAM 权限`](https://ram.console.aliyun.com/)

```json
{
  "Version": "1",
  "Statement": [
    {
      "Action": [
        "ecs:CreateNetworkInterface",
        "ecs:DescribeNetworkInterfaces",
        "ecs:AttachNetworkInterface",
        "ecs:DetachNetworkInterface",
        "ecs:DeleteNetworkInterface",
        "ecs:DescribeInstanceTypes",
        "ecs:AssignPrivateIpAddresses",
        "ecs:UnassignPrivateIpAddresses",
        "ecs:AssignIpv6Addresses",
        "ecs:UnassignIpv6Addresses"
      ],
      "Resource": [
        "*"
      ],
      "Effect": "Allow"
    },
    {
      "Action": [
        "vpc:DescribeVSwitches"
      ],
      "Resource": [
        "*"
      ],
      "Effect": "Allow"
    }
  ]
}
```

## 部署 Terway

```bash
echo "
centralizedIPAM:true
terway:
  securityGroupIDs:
    - sg-1
    - sg-2
  vSwitchIDs:
    cn-hangzhou-k:
      - vsw-1
      - vsw-2
" | helm template --namespace kube-system terway-eniip . --values -
```
