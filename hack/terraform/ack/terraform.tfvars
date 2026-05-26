# ============================================
# Terraform 变量配置文件 - 阿里云 ACK 集群
# ============================================

# 阿里云区域配置
region_id = "cn-hangzhou"

# 集群规格 (ack.standard 或 ack.pro.small)
cluster_spec = "ack.pro.small"

# 可用区配置
availability_zone = ["cn-hangzhou-j", "cn-hangzhou-k"]

# 节点交换机配置 (留空将自动创建)
node_vswitch_ids = []

# 节点交换机 CIDR (当 node_vswitch_ids 为空时使用)
node_vswitch_cidrs = ["172.16.2.0/23", "172.16.4.0/23"]

# Terway Pod 交换机配置 (留空将自动创建)
terway_vswitch_ids = []

# Terway Pod 交换机 CIDR (当 terway_vswitch_ids 为空时使用)
terway_vswitch_cidrs = ["172.17.0.0/16", "172.18.0.0/16"]

# 工作节点实例类型
worker_instance_types = ["ecs.g7nex.2xlarge", "ecs.g7nex.4xlarge", "ecs.g7ne.xlarge", "ecs.g9i.2xlarge", "ecs.g9i.3xlarge"]

# 集群名称前缀
k8s_name_prefix = "tf-ack-hangzhou"

# ============================================
# Kubernetes 集群配置
# ============================================

# Kubernetes 版本
kubernetes_version = "1.34.3-aliyun.1"

# 服务网络 CIDR (dual stack 时需逗号分隔: "ipv4-cidr,ipv6-cidr")
# e2e-cluster.sh 会按 profile 通过 -var 注入对应值。
service_cidr = "192.168.0.0/16"

# RRSA 设置 (RAM Roles for Service Accounts)
enable_rrsa = false

# 代理模式 (ipvs 或 iptables)
proxy_mode = "ipvs"

# 删除保护
deletion_protection = false

# 时区
timezone = "Asia/Shanghai"

# IP 协议栈 (ipv4 或 dual)。e2e-cluster.sh 会按 profile 通过 -var 注入对应值。
ip_stack = "ipv4"

# CNI 部署模式: "byo" (helm chart 自装 terway) 或 "ack" (ACK addon 自动装 terway-eniip)。
# e2e-cluster.sh 会按 profile 通过 -var 注入对应值。
cluster_mode = "ack"

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
# 敏感信息配置 (请根据实际情况修改)
# ============================================


# 节点池 SSH 密码 (如果需要)
# password = "your-ssh-password-here"

# ============================================
# 组件配置
# ============================================
# cluster_addons 由 terraform_e2e.tf 中 locals.all_addons 根据 cluster_mode 自动生成,
# 不再通过 tfvars 手动配置, 避免与 cluster_mode (byo/ack) 决策不一致。
