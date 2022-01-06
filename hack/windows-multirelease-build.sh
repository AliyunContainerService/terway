#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

unset CDPATH

# The root directory
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

export ROOT_SBIN_DIR="${ROOT_DIR}/sbin"
mkdir -p "${ROOT_SBIN_DIR}"

terraform_version=${TERRAFORM_VERSION:-"v0.14.9"}
build_worker_access_key="${BUILD_WORKER_ACCESS_KEY:-}"
build_worker_access_secret="${BUILD_WORKER_ACCESS_SECRET:-}"
build_worker_region="${BUILD_WORKER_REGION:-"cn-hongkong"}"
build_worker_vhds="${BUILD_WORKER_VHDS:-"win2019_1809_x64_dtc_en-us_40G_container_alibase_20210716.vhd wincore_1909_x64_dtc_en-us_40G_container_alibase_20200723.vhd wincore_2004_x64_dtc_en-us_40G_container_alibase_20210716.vhd"}"
build_worker_type="${BUILD_WORKER_TYPE:-"ecs.g6e.4xlarge"}"
build_worker_disk="${BUILD_WORKER_DISK:-"cloud_essd"}"
build_worker_password="${BUILD_WORKER_PASSWORD:-}"
image_registry="${IMAGE_REGISTRY:-"registry.cn-hongkong.aliyuncs.com"}"
image_registry_namespace="${IMAGE_REGISTRY_NAMESPACE:-"acs"}"
image_registry_username="${IMAGE_REGISTRY_USERNAME:-}"
image_registry_password="${IMAGE_REGISTRY_PASSWORD:-}"
image_name=${IMAGE_NAME:-"terway"}
image_version=${IMAGE_VERSION:-"v0.0.0"}

function terraform::bin() {
  local bin="terraform"
  if [[ -f "${ROOT_SBIN_DIR}/terraform" ]]; then
    bin="${ROOT_SBIN_DIR}/terraform"
  fi
  echo "${bin}"
}

function terraform::install() {
  curl -fL "https://releases.hashicorp.com/terraform/${terraform_version#v}/terraform_${terraform_version#v}_linux_amd64.zip" -o /tmp/terraform.zip
  unzip -o /tmp/terraform.zip -d /tmp
  chmod +x /tmp/terraform && mv /tmp/terraform "${ROOT_SBIN_DIR}/terraform"
}

function terraform::validate() {
  # shellcheck disable=SC2046
  if [[ -n "$(command -v $(terraform::bin))" ]]; then
    # shellcheck disable=SC2076
    if [[ $($(terraform::bin) version 2>&1) =~ "Terraform ${terraform_version}" ]]; then
      return 0
    fi
  fi

  if terraform::install; then
    return 0
  fi
  return 1
}

function terraform::build() {
  # validate
  if ! terraform::validate; then
    return 1
  fi

  # init
  if [[ ! -f "${ROOT_DIR}/terraform/.terraform.lock.hcl" ]]; then
    if ! $(terraform::bin) init -no-color -upgrade "${ROOT_DIR}/terraform" 2>&1; then
      return 1
    fi
  fi

  # apply
  cat <<EOF > "${ROOT_DIR}/terway.auto.tfvars"
region = "${build_worker_region}"
access_key = "${build_worker_access_key}"
secret_key = "${build_worker_access_secret}"
host_image_list_string = "${build_worker_vhds}"
host_password = "${build_worker_password}"
host_type = "${build_worker_type}"
host_disk_category = "${build_worker_disk}"
image_registry_list_string = "${image_registry}"
image_registry_username = "${image_registry_username}"
image_registry_password = "${image_registry_password}"
image_namespace = "${image_registry_namespace}"
image_name = "${image_name}"
image_tag = "${image_version}"
EOF
  local err
  set +o errexit
  set +o pipefail
  if ! $(terraform::bin) apply -no-color -auto-approve "${ROOT_DIR}/terraform" 2>&1; then
    err="true"
  fi
  $(terraform::bin) destroy -no-color -auto-approve "${ROOT_DIR}/terraform" >/dev/null 2>&1
  set -o pipefail
  set -o errexit
  [[ "${err:-}" != "true" ]] || return 1
}

terraform::build
