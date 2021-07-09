#!/usr/bin/env bash

go mod tidy
go mod vendor
bash hack/update-codegen.sh
go install -tags tools ./
controller-gen crd paths=./pkg/apis/network.alibabacloud.com/v1beta1/ output:dir=./pkg/apis/crds
rm -rf vendor
