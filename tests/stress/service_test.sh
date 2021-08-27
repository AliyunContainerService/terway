#!/bin/bash

# this script creates a deployment with several pods and SERVICE_TOTAL service(s)
# see: ../templates/testcases/stress/service.yml && ../templates/testcases/stress/nginx-pod-service.yml
source ../helpers.bash

SERVICE_YAML=$(cat ../templates/testcases/stress/service.yml)
SERVICE_TOTAL=2000

generate_service() {
  local name
  name="nginx-service-$1"
  echo "${SERVICE_YAML/SERVICENAME/$name}"
}

parse_args $@
if [[ ! -z ${trunk} ]]; then
  kubectl apply -f ../templates/testcases/stress/pod-networking.yml
fi

# apply deployment
kubectl apply -f ../templates/testcases/stress/nginx-pod-service.yml

# apply service
for (( i=0; i<SERVICE_TOTAL; i=i+1 )); do
  echo "Apply service $i"
  generate_service $i | kubectl apply -f '-'
done

#sleep 3600
## delete service
#echo "Delete services"
#kubectl delete svc --all -n stress-service
#
#kubectl delete -f ../templates/testcases/stress/nginx-pod-service.yml
#
#if [[ ! -z ${trunk} ]]; then
#  		kubectl delete -f ../templates/testcases/stress/pod-networking.yml
#fi