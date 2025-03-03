# Terway 灵骏节点

## 概述

PAI灵骏是一种高密度计算服务，专为大规模计算场景设计。Terway CNI 能够管理灵骏节点，并支持节点上容器的网络通信。
灵骏弹性网卡（Lingjun Elastic Network Interface，简称 LENI）是灵骏 GPU 实例接入专有网络 VPC 的虚拟网络接口。它连接灵骏节点与 VPC，实现与 VPC 内其他云资源的高效互联互通。
与 ECS 弹性网卡类似，LENI 也支持辅助 IP 配置。

## RAM

确保Terway 使用的凭据，具备下面的 RAM 权限：

```json
{
  "Version": "1",
  "Statement": [
    {
      "Action": [
        "eflo:CreateElasticNetworkInterface",
        "eflo:DeleteElasticNetworkInterface",
        "eflo:AssignLeniPrivateIpAddress",
        "eflo:UnassignLeniPrivateIpAddress",
        "eflo:GetElasticNetworkInterface",
        "eflo:ListLeniPrivateIpAddresses",
        "eflo:ListElasticNetworkInterfaces",
        "eflo:GetNodeInfoForPod"
      ],
      "Resource": [
        "*"
      ],
      "Effect": "Allow"
    }
  ]
}
```

## 节点配置

- 添加节点标签 `alibabacloud.com/lingjun-worker": "true"`
- 确保节点上arp 设置正确

```text
sysctl -w net.ipv4.conf.all.arp_announce = 0
sysctl -w net.ipv4.conf.all.arp_ignore = 0
```

## 部署 Terway

```bash
echo "
centralizedIPAM:true
featureGates=\"EFLO=true\"
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
