#!/bin/bash

set -e

install_kind(){
  # For AMD64 / x86_64
  [ $(uname -m) = x86_64 ] && curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.26.0/kind-linux-amd64 && curl -LO "https://dl.k8s.io/release/v1.30.8/bin/linux/amd64/kubectl"
  # For ARM64
  [ $(uname -m) = aarch64 ] && curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.26.0/kind-linux-arm64 && curl -LO "https://dl.k8s.io/release/v1.30.8/bin/linux/arm64/kubectl"
  chmod +x ./kind ./kubectl
  sudo mv ./kind /usr/local/bin/kind
  sudo mv ./kubectl /usr/local/bin/kubectl
}

install_helm(){
  curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
}

build_image(){
  BUILD_ARGS=(buildx build --load)
  if [ "$GITHUB_ACTIONS" ]; then
    BUILD_ARGS+=(--cache-from "type=gha,scope=$1")
  fi
  shift
  docker "${BUILD_ARGS[@]}" "$@" ../../
}

build_terway_images(){
  build_image terway -t local/terway:1 -f ../../deploy/images/terway/Dockerfile
  build_image controlplane -t local/terway-controlplane:1 -f ../../deploy/images/terway-controlplane/Dockerfile
}

prepare_kind(){
  kind delete cluster || true
  kind create cluster --config cluster.yml
  kind load docker-image local/terway:1 local/terway-controlplane:1
  kubectl cluster-info --context kind-kind
}

get_cilium_cmdline() {
  ctrlID=$(docker ps --filter "name=kind-control-plane" --format "{{.ID}}")
  echo "pid=\$(pidof cilium-agent);if [ -z \"\$pid\" ];then exit 1;fi; cat /proc/\${pid}/cmdline" > cmd
  docker cp cmd "${ctrlID}:/"
  docker exec "${ctrlID}" bash /cmd
}

tear_down_callback(){
  helm uninstall -n kube-system terway-eniip
  kind delete cluster || true
}

eniip_default_setup(){
  prepare_kind
  helm install -n kube-system terway-eniip ../../charts/terway \
   --replace --force \
   --set terway.image.repository=local/terway \
   --set terway.image.tag=1 \
   --set terway.accessKey=foo \
   --set terwayControlplane.accessSecret=bar \
   --set terwayControlplane.image.repository=local/terway-controlplane \
   --set terwayControlplane.image.tag=1 \
   --set terwayControlplane.accessKey=foo \
   --set terwayControlplane.accessSecret=bar \
   --set terway.enableNetworkPolicy=true
}

eniip_default_check() {
  echo "Checking eniip default setup..." >&2
  local current=""
  for ((i=1; i<=10; i++)); do
    set +e
    current=$(get_cilium_cmdline)
    exit_code=$?
    set -e
    if [ $exit_code -eq 0 ]; then
      echo "Success on attempt $i" >&2
      break
    else
      echo "Attempt $i failed. Retrying in 10 seconds..." >&2
      sleep 10
    fi
  done

  if ! diff -w <(echo "$current") conf/eniip_default_cmdline; then
    echo "Files are not equal."
    exit 1
  fi
}

eniip_datapathv2_setup(){
  prepare_kind
  helm install -n kube-system terway-eniip ../../charts/terway \
   --replace --force \
   --set terway.image.repository=local/terway \
   --set terway.image.tag=1 \
   --set terway.accessKey=foo \
   --set terwayControlplane.accessSecret=bar \
   --set terwayControlplane.image.repository=local/terway-controlplane \
   --set terwayControlplane.image.tag=1 \
   --set terwayControlplane.accessKey=foo \
   --set terwayControlplane.accessSecret=bar \
   --set terway.enableDatapathV2=true
}

eniip_datapathv2_check() {
  echo "Checking eniip datapathv2 setup..." >&2
  local current=""
  for ((i=1; i<=10; i++)); do
    set +e
    current=$(get_cilium_cmdline)
    exit_code=$?
    set -e
    if [ $exit_code -eq 0 ]; then
      echo "Success on attempt $i" >&2
      break
    else
      echo "Attempt $i failed. Retrying in 10 seconds..." >&2
      sleep 10
    fi
  done

  if ! diff -w <(echo "$current") conf/eniip_datapathv2_cmdline; then
    echo "Files are not equal."
    exit 1
  fi
}

eniip_legacy_ciliumargs_setup(){
  prepare_kind
  helm install -n kube-system terway-eniip ../../charts/terway \
   --replace --force \
   --set terway.image.repository=local/terway \
   --set terway.image.tag=1 \
   --set terway.accessKey=foo \
   --set terwayControlplane.accessSecret=bar \
   --set terwayControlplane.image.repository=local/terway-controlplane \
   --set terwayControlplane.image.tag=1 \
   --set terwayControlplane.accessKey=foo \
   --set terwayControlplane.accessSecret=bar \
   --set terway.enableNetworkPolicy=true \
   --set terway.ciliumArgs="--disable-per-package-lb=true"
}


eniip_legacy_ciliumargs_check() {
  echo "Checking eniip legacy setup..." >&2
  local current=""
  for ((i=1; i<=10; i++)); do
    set +e
    current=$(get_cilium_cmdline)
    exit_code=$?
    set -e
    if [ $exit_code -eq 0 ]; then
      echo "Success on attempt $i" >&2
      break
    else
      echo "Attempt $i failed. Retrying in 10 seconds..." >&2
      sleep 10
    fi
  done

  if ! diff -w <(echo "$current") conf/eniip_legacy_ciliumargs_cmdline; then
    echo "Files are not equal."
    exit 1
  fi
}


run_test_function() {
    local test_name="$1"
    echo "Running test $test_name"

}

run_test() {
    local setup_callback="$1"
    local run_test_callback="$2"
    local tear_down_callback="$3"

    if [ -n "$setup_callback" ]; then
        $setup_callback
    fi

    if [ -n "$run_test_callback" ]; then
        $run_test_callback
    fi

    if [ -n "$tear_down_callback" ]; then
        $tear_down_callback
    fi
}

set -e

install_kind
install_helm
build_terway_images

tests=(
    "eniip_default_setup eniip_default_check tear_down_callback"
    "eniip_datapathv2_setup eniip_datapathv2_check tear_down_callback"
    "eniip_legacy_ciliumargs_setup eniip_legacy_ciliumargs_check tear_down_callback"
)

for test in "${tests[@]}"; do
    IFS=' ' read -r setup_callback run_test_callback tear_down_callback <<< "$test"
    run_test "$setup_callback" "$run_test_callback" "$tear_down_callback"
done