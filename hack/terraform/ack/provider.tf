terraform {
  required_version = ">= 1.0.0"

  required_providers {
    alicloud = {
      source  = "aliyun/alicloud"
      version = "~> 1.262.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

provider "alicloud" {
  # 阿里云访问凭证可以通过以下方式配置：
  # 1. 环境变量：ALICLOUD_ACCESS_KEY, ALICLOUD_SECRET_KEY, ALICLOUD_REGION
  # 2. 阿里云 CLI 配置：通过 shared_credentials_file 和 profile 参数读取
  # 3. Terraform 变量：access_key, secret_key, region
  # 4. 阿里云配置文件：~/.aliyun/config.json (需要指定 shared_credentials_file 和 profile)

  region = var.region_id

  # 从 ~/.aliyun/config.json 读取凭证
  shared_credentials_file = pathexpand("~/.aliyun/config.json")
  profile                 = "terraform" # 使用 config.json 中 current 指定的 profile

  # 可选：如果不使用配置文件，可以在这里指定
  # access_key = var.access_key
  # secret_key = var.secret_key
}
