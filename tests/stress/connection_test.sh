#!/usr/bin/env bash

source ../helpers.bash

parse_args $@
if [[ ! -z ${trunk} ]]; then
  kubectl apply -f ../templates/testcases/stress/pod-networking.yml
fi

kubectl apply -f ../templates/testcases/stress/nginx-pod-connection.yml