1. 从daemon获取IP和对应ENI网卡，Pool，记录信息, GC
2. binary创建veth网卡绑定IP和配置策略路由

# cni binary
## 网络连通性
### 从daemon获取IP和对应ENI网卡
### 创建veth网卡绑定IP和配置策略路由
## 失败处理
删除路由和网卡，从daemon返还IP地址

# daemon
## pod信息管理
### 监听apiserver的事件，关心本节点pod事件
### 维护本地pod信息存储
## eni和IP状态管理
## eni网卡和其对应的路由表初始化
## GC
## QOS reconcile
### 配置流量和带宽限制

# policy
## calico v3.x