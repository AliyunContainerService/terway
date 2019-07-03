#!/bin/bash

# example: ./test.sh --cluster-id cxxx --access-key LTXXX --access-secret XXX --region cn-hangzhou --category vpc --image registry.aliyuncs.com/acs/terway:v1.0.9.10-gfc1045e-aliyun

set -e

source install_env.sh "$@"

# test terway pod ready and device plugin
bats cni_ready.bats
# test pod connection between diff type of pods
bats pod_connection.bats
# test network policy
bats network_policy.bats
# test service or loadbalancer
bats service.bats
