variable "image_registry_list_string" {
  type    = string
  default = "registry.cn-hongkong.aliyuncs.com"
}

variable "image_registry_username" {
  type = string
}

variable "image_registry_password" {
  type = string
}

variable "image_namespace" {
  type    = string
  default = "acs"
}

variable "image_name" {
  type    = string
  default = "terway"
}

variable "image_tag" {
  type    = string
  default = "v0.0.0-windows-amd64-1809"
}

variable "image_dockerfile_path" {
  type    = string
  default = "Dockerfile.windows"
}

provider "windbag" {
  docker {
    version             = "19.03"
    push_foreign_layers = true
  }
}

locals {
  image_registry_list = split(" ", var.image_registry_list_string)
}

# image
resource "windbag_image" "default" {
  tag          = [
  for registry in local.image_registry_list :
  join(":", [join("/", [registry, var.image_namespace, var.image_name]), var.image_tag])
  ]
  push         = true
  push_timeout = "3h"
  file         = var.image_dockerfile_path

  build_arg_release_mapper {
    release   = "1809"
    build_arg = {
      "BASE_IMAGE_TAG"    = "7.1.4-nanoserver-1809-20210812"
    }
  }
  build_arg_release_mapper {
    release   = "1909"
    build_arg = {
      "BASE_IMAGE_TAG"    = "7.1.4-nanoserver-1909-20210812"
    }
  }
  build_arg_release_mapper {
    release   = "2004"
    build_arg = {
      "BASE_IMAGE_TAG"    = "7.1.4-nanoserver-2004-20210812"
    }
  }

  dynamic "registry" {
    for_each = local.image_registry_list
    content {
      address       = registry.value
      username      = var.image_registry_username
      password      = var.image_registry_password
      login_timeout = "30m"
    }
  }

  dynamic "worker" {
    for_each = alicloud_eip_address.default.*.ip_address
    content {
      address = format("%s:22", worker.value)
      ssh {
        username = "root"
        password = var.host_password
      }
    }
  }

  timeouts {
    create = "2h"
    read   = "6h"
    update = "12h"
  }
}
