variable "region_id" {
  type    = string
  default = "cn-hangzhou"
}

variable "cluster_spec" {
  type        = string
  description = "The cluster specifications of kubernetes cluster,which can be empty. Valid values:ack.standard : Standard managed clusters; ack.pro.small : Professional managed clusters."
  default     = "ack.pro.small"
}

# 指定虚拟交换机（vSwitches）的可用区。
variable "availability_zone" {
  description = "The availability zones of vswitches."
  default     = ["cn-hangzhou-i", "cn-hangzhou-j", "cn-hangzhou-k"]
}

# 指定交换机ID（vSwitch IDs）的列表。
variable "node_vswitch_ids" {
  description = "List of existing node vswitch ids for terway."
  type        = list(string)
  default     = []
}

# 用于创建新vSwitches的CIDR地址块列表。
variable "node_vswitch_cidrs" {
  description = "List of cidr blocks used to create several new vswitches when 'node_vswitch_ids' is not specified."
  type        = list(string)
  default     = ["172.16.0.0/23", "172.16.2.0/23", "172.16.4.0/23"]
}

# 指定网络组件Terway配置。如果为空，默认会根据terway_vswitch_cidrs的创建新的terway vSwitch。
variable "terway_vswitch_ids" {
  description = "List of existing pod vswitch ids for terway."
  type        = list(string)
  default     = []
}

# 当没有指定terway_vswitch_ids时，用于创建Terway使用的vSwitch的CIDR地址块。
variable "terway_vswitch_cidrs" {
  description = "List of cidr blocks used to create several new vswitches when 'terway_vswitch_ids' is not specified."
  type        = list(string)
  default     = ["172.16.208.0/20", "172.16.224.0/20", "172.16.240.0/20"]
}

# 定义了用于启动工作节点的ECS实例类型。
variable "worker_instance_types" {
  description = "The ecs instance types used to launch worker nodes."
  default     = ["ecs.g7ne.2xlarge", "ecs.g7ne.4xlarge"]
}

variable "cluster_addons" {
  type = list(object({
    name     = optional(string)
    config   = optional(string)
    disabled = optional(bool)
  }))
  default = [
  ]
}

variable "k8s_name_prefix" {
  description = "The name prefix used to create managed kubernetes cluster."
  default     = "tf-ack-hangzhou"
}

variable "password" {
  description = "The password used to login to the kubernetes cluster nodes."
  type        = string
  default     = ""
  sensitive   = true
}

variable "kubernetes_version" {
  description = "The Kubernetes version of the cluster."
  type        = string
  default     = "1.34.3-aliyun.1"
}

variable "service_cidr" {
  description = "The CIDR block for services."
  type        = string
  default     = "192.168.0.0/16"
}

variable "enable_rrsa" {
  description = "Whether to enable RAM Roles for Service Accounts."
  type        = bool
  default     = false
}

variable "proxy_mode" {
  description = "The proxy mode of the cluster."
  type        = string
  default     = "ipvs"
}

variable "deletion_protection" {
  description = "Whether to enable deletion protection for the cluster."
  type        = bool
  default     = false
}

variable "timezone" {
  description = "The timezone of the cluster."
  type        = string
  default     = "Asia/Shanghai"
}

variable "ip_stack" {
  description = "The IP stack of the cluster."
  type        = string
  default     = "ipv4"
}

variable "cluster_profile" {
  description = "The profile of the cluster."
  type        = string
  default     = "Default"
}

variable "is_enterprise_security_group" {
  description = "Whether to use enterprise security group."
  type        = bool
  default     = true
}

variable "api_audiences" {
  description = "The API audiences for the cluster."
  type        = list(string)
  default     = ["https://kubernetes.default.svc"]
}

variable "service_account_issuer" {
  description = "The service account issuer for the cluster."
  type        = string
  default     = "https://kubernetes.default.svc"
}

variable "control_plane_log_components" {
  description = "The control plane log components."
  type        = list(string)
  default     = ["apiserver", "kcm", "scheduler", "ccm"]
}

variable "kubeconfig_duration_minutes" {
  description = "Duration of kubeconfig validity in minutes."
  type        = number
  default     = 3600
}

variable "kubeconfig_output_file" {
  description = "File path to save the kubeconfig. Empty string means not to save."
  type        = string
  default     = ""
}

variable "auto_upgrade_enabled" {
  description = "Whether to enable automatic cluster upgrade."
  type        = bool
  default     = true
}

variable "auto_upgrade_channel" {
  description = "The channel for automatic cluster upgrade."
  type        = string
  default     = "stable"
}

variable "maintenance_window_enabled" {
  description = "Whether to enable maintenance window."
  type        = bool
  default     = true
}

variable "maintenance_window_duration" {
  description = "The duration of maintenance window."
  type        = string
  default     = "3h"
}

variable "maintenance_window_weekly_period" {
  description = "The weekly period for maintenance window."
  type        = string
  default     = "Wednesday"
}

variable "maintenance_window_time" {
  description = "The maintenance time."
  type        = string
  default     = "2025-11-07T00:00:00.000+08:00"
}

variable "audit_log_enabled" {
  description = "Whether to enable audit logging."
  type        = bool
  default     = false
}

# 生成随机后缀以避免集群名称冲突
resource "random_string" "cluster_suffix" {
  length  = 4
  lower   = true
  upper   = false
  numeric = true
  special = false
}

# 默认资源名称。
locals {
  k8s_name_terway         = substr(join("-", [var.k8s_name_prefix, "terway", random_string.cluster_suffix.result]), 0, 63)
  new_vpc_name            = "tf-vpc-172-16"
  new_vsw_name_azD        = "tf-vswitch-azD-172-16-0"
  new_vsw_name_azE        = "tf-vswitch-azE-172-16-2"
  new_vsw_name_azF        = "tf-vswitch-azF-172-16-4"
  nodepool_name           = "default-nodepool"
  managed_nodepool_name   = "managed-node-pool"
  autoscale_nodepool_name = "autoscale-node-pool"
  log_project_name        = "log-for-${local.k8s_name_terway}"
}

# 节点ECS实例配置。将查询满足CPU、Memory要求的ECS实例类型。
data "alicloud_instance_types" "default" {
  cpu_core_count       = 8
  memory_size          = 32
  availability_zone    = var.availability_zone[0]
  kubernetes_node_role = "Worker"
}

# 专有网络。
resource "alicloud_vpc" "default" {
  vpc_name   = local.new_vpc_name
  cidr_block = "172.16.0.0/12"
}

# Node交换机。
resource "alicloud_vswitch" "vswitches" {
  count      = length(var.node_vswitch_ids) > 0 ? 0 : length(var.node_vswitch_cidrs)
  vpc_id     = alicloud_vpc.default.id
  cidr_block = element(var.node_vswitch_cidrs, count.index)
  zone_id    = element(var.availability_zone, count.index)
}

# Pod交换机。
resource "alicloud_vswitch" "terway_vswitches" {
  count      = length(var.terway_vswitch_ids) > 0 ? 0 : length(var.terway_vswitch_cidrs)
  vpc_id     = alicloud_vpc.default.id
  cidr_block = element(var.terway_vswitch_cidrs, count.index)
  zone_id    = element(var.availability_zone, count.index)
}

# Kubernetes托管版。
resource "alicloud_cs_managed_kubernetes" "default" {
  name                         = local.k8s_name_terway                                         # Kubernetes集群名称。
  cluster_spec                 = var.cluster_spec                                              # 创建Pro版集群。
  vswitch_ids                  = split(",", join(",", alicloud_vswitch.vswitches.*.id))        # 节点池所在的vSwitch。指定一个或多个vSwitch的ID，必须在availability_zone指定的区域中。
  pod_vswitch_ids              = split(",", join(",", alicloud_vswitch.terway_vswitches.*.id)) # Pod网络所在的vSwitch，Terway网络插件必需。
  new_nat_gateway              = true                                                          # 是否在创建Kubernetes集群时创建新的NAT网关。默认为true。
  service_cidr                 = var.service_cidr                                              # 服务网络的CIDR块。
  slb_internet_enabled         = true                                                          # 是否为API Server创建Internet负载均衡。默认为false。
  enable_rrsa                  = var.enable_rrsa                                               # RAM Roles for Service Accounts设置。
  proxy_mode                   = var.proxy_mode                                                # 代理模式。
  deletion_protection          = var.deletion_protection                                       # 删除保护设置。
  timezone                     = var.timezone                                                  # 时区设置。
  ip_stack                     = var.ip_stack                                                  # IP协议栈。
  version                      = var.kubernetes_version                                        # Kubernetes版本。
  profile                      = var.cluster_profile                                           # 集群配置文件。
  is_enterprise_security_group = var.is_enterprise_security_group                              # 企业级安全组设置。

  # API 受众设置
  api_audiences = var.api_audiences

  # 服务账户颁发者设置
  service_account_issuer = var.service_account_issuer

  # 运维策略
  operation_policy {
    cluster_auto_upgrade {
      channel = var.auto_upgrade_channel
      enabled = var.auto_upgrade_enabled
    }
  }

  # 维护窗口设置
  maintenance_window {
    duration         = var.maintenance_window_duration
    weekly_period    = var.maintenance_window_weekly_period
    enable           = var.maintenance_window_enabled
    maintenance_time = var.maintenance_window_time
  }

  # 审计日志配置
  audit_log_config {
    enabled = var.audit_log_enabled
  }

  # 控制平面日志组件
  control_plane_log_components = var.control_plane_log_components

  # 组件管理
  dynamic "addons" {
    for_each = var.cluster_addons
    content {
      name     = lookup(addons.value, "name", null)
      config   = lookup(addons.value, "config", null)
      disabled = lookup(addons.value, "disabled", null)
    }
  }
}

# 普通节点池。
resource "alicloud_cs_kubernetes_node_pool" "default" {
  cluster_id            = alicloud_cs_managed_kubernetes.default.id              # Kubernetes集群名称。
  node_pool_name        = "default"                                              # 节点池名称。
  vswitch_ids           = split(",", join(",", alicloud_vswitch.vswitches.*.id)) # 节点池所在的vSwitch。指定一个或多个vSwitch的ID，必须在availability_zone指定的区域中。
  instance_types        = var.worker_instance_types
  instance_charge_type  = "PostPaid"
  desired_size          = 2
  install_cloud_monitor = true
  system_disk_category  = "cloud_essd"
  system_disk_size      = 100
  image_type            = "AliyunLinux3"
  data_disks {              # 节点数据盘配置。
    category = "cloud_essd" # 节点数据盘种类。
    size     = 120          # 节点数据盘大小。
  }

}

# 独占ENI节点池。
resource "alicloud_cs_kubernetes_node_pool" "exclusive_eni" {
  cluster_id            = alicloud_cs_managed_kubernetes.default.id # Kubernetes集群名称。
  node_pool_name        = "exclusive_eni"
  vswitch_ids           = split(",", join(",", alicloud_vswitch.vswitches.*.id)) # 节点池所在的vSwitch。指定一个或多个vSwitch的ID，必须在availability_zone指定的区域中。
  instance_types        = var.worker_instance_types
  instance_charge_type  = "PostPaid"
  desired_size          = 2
  install_cloud_monitor = true
  system_disk_category  = "cloud_essd"
  system_disk_size      = 100
  image_type            = "AliyunLinux3"
  data_disks {              # 节点数据盘配置。
    category = "cloud_essd" # 节点数据盘种类。
    size     = 120          # 节点数据盘大小。
  }
  labels {
    key   = "k8s.aliyun.com/exclusive-mode-eni-type"
    value = "secondary"
  }
}

# 获取集群凭据和 kubeconfig
data "alicloud_cs_cluster_credential" "auth" {
  cluster_id                 = alicloud_cs_managed_kubernetes.default.id
  temporary_duration_minutes = var.kubeconfig_duration_minutes
  output_file                = "kubeconfig-${local.k8s_name_terway}"
}

# 输出集群信息和 kubeconfig
output "cluster_id" {
  description = "The ID of the Kubernetes cluster"
  value       = alicloud_cs_managed_kubernetes.default.id
}

output "cluster_name" {
  description = "The name of the Kubernetes cluster"
  value       = alicloud_cs_managed_kubernetes.default.name
}

output "kubeconfig" {
  description = "The kubeconfig for accessing the Kubernetes cluster"
  value       = data.alicloud_cs_cluster_credential.auth.kube_config
  sensitive   = true
}

output "kubeconfig_expiration" {
  description = "The expiration time of the kubeconfig"
  value       = data.alicloud_cs_cluster_credential.auth.expiration
}

output "kubeconfig_file_path" {
  description = "The file path where kubeconfig is saved"
  value       = "kubeconfig-${local.k8s_name_terway}"
}
