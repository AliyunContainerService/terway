terraform {
  required_providers {
    alicloud = {
      source  = "aliyun/alicloud"
      version = "1.139.0"
    }
    windbag  = {
      source  = "thxcode/windbag"
      version = "0.3.1"
    }
  }
}

# tf plan \
#  --var 'region=cn-hongkong' \
#  --var 'access_key=...' \
#  --var 'secret_key=...' \
#  --var 'host_image_list=["win2019_1809_x64_dtc_en-us_40G_container_alibase_20210716.vhd","wincore_1909_x64_dtc_en-us_40G_container_alibase_20200723.vhd","wincore_2004_x64_dtc_en-us_40G_container_alibase_20210716.vhd"]' \
#  --var 'host_password=Just4Test' \
#  --var 'image_repository_list=["registry.cn-hangzhou.aliyuncs.com", "registry.cn-hongkong.aliyuncs.com"]' \
#  --var 'image_name=terway' \
#  --var 'image_tag=v0.0.0' \
#  --var 'image_registry_username=...' \
#  --var 'image_registry_password=...'
