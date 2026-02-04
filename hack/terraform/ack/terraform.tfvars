# ============================================
# Terraform 变量配置文件 - 阿里云 ACK 集群
# ============================================

# 阿里云区域配置
region_id = "cn-hangzhou"

# 集群规格 (ack.standard 或 ack.pro.small)
cluster_spec = "ack.pro.small"

# 可用区配置
availability_zone = ["cn-hangzhou-i", "cn-hangzhou-j", "cn-hangzhou-k"]

# 节点交换机配置 (留空将自动创建)
node_vswitch_ids = []

# 节点交换机 CIDR (当 node_vswitch_ids 为空时使用)
node_vswitch_cidrs = ["172.16.0.0/23", "172.16.2.0/23", "172.16.4.0/23"]

# Terway Pod 交换机配置 (留空将自动创建)
terway_vswitch_ids = []

# Terway Pod 交换机 CIDR (当 terway_vswitch_ids 为空时使用)
terway_vswitch_cidrs = ["172.16.208.0/20", "172.16.224.0/20", "172.16.240.0/20"]

# 工作节点实例类型
worker_instance_types = ["ecs.g7ne.2xlarge", "ecs.g7ne.4xlarge"]

# 集群名称前缀
k8s_name_prefix = "tf-ack-hangzhou"

# ============================================
# Kubernetes 集群配置
# ============================================

# Kubernetes 版本
kubernetes_version = "1.34.3-aliyun.1"

# 服务网络 CIDR
service_cidr = "192.168.0.0/16"

# RRSA 设置 (RAM Roles for Service Accounts)
enable_rrsa = false

# 代理模式 (ipvs 或 iptables)
proxy_mode = "ipvs"

# 删除保护
deletion_protection = false

# 时区
timezone = "Asia/Shanghai"

# IP 协议栈 (ipv4 或 ipv6 或 dual)
ip_stack = "ipv4"

# 集群配置模板
cluster_profile = "Default"

# 企业级安全组
is_enterprise_security_group = true

# API 受众
api_audiences = ["https://kubernetes.default.svc"]

# 服务账户颁发者
service_account_issuer = "https://kubernetes.default.svc"

# 控制平面日志组件
control_plane_log_components = ["apiserver", "kcm", "scheduler", "ccm"]

# ============================================
# 运维配置
# ============================================

# 自动升级设置
auto_upgrade_enabled = true
auto_upgrade_channel = "stable"

# 维护窗口设置
maintenance_window_enabled       = true
maintenance_window_duration      = "3h"
maintenance_window_weekly_period = "Wednesday"
maintenance_window_time          = "2025-11-07T00:00:00.000+08:00"

# 审计日志
audit_log_enabled = false

# ============================================
# Kubeconfig 配置
# ============================================

# kubeconfig 有效期（分钟）
kubeconfig_duration_minutes = 60

# kubeconfig 输出文件路径（留空表示不输出到文件）
kubeconfig_output_file = ""

# ============================================
# 敏感信息配置 (请根据实际情况修改)
# ============================================


# 节点池 SSH 密码 (如果需要)
# password = "your-ssh-password-here"

# ============================================
# 组件配置
# ============================================

cluster_addons = [
  {
    name     = "metrics-server"
    disabled = true
  },
  {
    name = "coredns"
  },
  {
    name     = "kube-flannel-ds"
    disabled = true
  },
  {
    name = "terway-eniip"
  },
  {
    name = "terway-controlplane"
  },
  {
    name     = "csi-plugin"
    disabled = true
  },
  {
    name     = "nginx-ingress-controller"
    disabled = true
  }
]
