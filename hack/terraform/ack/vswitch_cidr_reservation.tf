# ============================================
# Pod 交换机 CIDR Prefix 预留
# mask+2 规则: vSwitch /16 -> 预留 /18 (IPv4), /64 -> /66 (IPv6)
# ============================================

locals {
  pod_vswitches = [
    { id = alicloud_vswitch.terway_vswitches[0].id, zone = var.availability_zone[0] }, # cn-hangzhou-j: 172.17.0.0/16
    { id = alicloud_vswitch.terway_vswitches[1].id, zone = var.availability_zone[1] }, # cn-hangzhou-k: 172.18.0.0/16
  ]
}

# ============================================
# Pod 交换机 IPv4 Prefix CIDR 预留
# 每个 /16 vSwitch 预留 1 个 /18 块 (mask+2 规则):
#   /18 = 16384 IPs = 1024 个 /28 prefix
# 远超 4 节点满载需求 (4 × 84 = 336 prefix)
# ============================================
resource "alicloud_vpc_vswitch_cidr_reservation" "pod_vswitch_ipv4_prefix" {
  count = length(local.pod_vswitches)

  vswitch_id            = local.pod_vswitches[count.index].id
  cidr_reservation_mask = "18" # /16 + 2 = /18
  cidr_reservation_type = "Prefix"
  ip_version            = "IPv4"
}

# ============================================
# Pod 交换机 IPv6 Prefix CIDR 预留
# 每个 vSwitch IPv6 CIDR 为 /64，预约 mask 必须至少 /66 (vSwitch mask + 2)
# ============================================
resource "alicloud_vpc_vswitch_cidr_reservation" "pod_vswitch_ipv6_prefix" {
  count = length(local.pod_vswitches)

  vswitch_id            = local.pod_vswitches[count.index].id
  cidr_reservation_mask = "66" # /64 + 2 = /66
  cidr_reservation_type = "Prefix"
  ip_version            = "IPv6"
}

# ============================================
# 输出 IPv4 预留信息
# ============================================
output "vswitch_ipv4_cidr_reservations" {
  description = "交换机 IPv4 Prefix CIDR 预留信息"
  value = [
    for i, reservation in alicloud_vpc_vswitch_cidr_reservation.pod_vswitch_ipv4_prefix : {
      reservation_id   = reservation.id
      vswitch_id       = reservation.vswitch_id
      reservation_mask = reservation.cidr_reservation_mask
      ip_version       = reservation.ip_version
      type             = reservation.cidr_reservation_type
    }
  ]
}

# ============================================
# 输出 IPv6 预留信息
# ============================================
output "vswitch_ipv6_cidr_reservations" {
  description = "交换机 IPv6 Prefix CIDR 预留信息"
  value = [
    for i, reservation in alicloud_vpc_vswitch_cidr_reservation.pod_vswitch_ipv6_prefix : {
      reservation_id   = reservation.id
      vswitch_id       = reservation.vswitch_id
      reservation_mask = reservation.cidr_reservation_mask
      ip_version       = reservation.ip_version
      type             = reservation.cidr_reservation_type
    }
  ]
}
