#!/bin/bash

# this script creates a deployment with several pods and SERVICE_TOTAL service(s)
# see: ../templates/testcases/stress/service.yml && ../templates/testcases/stress/nginx-pod-service.yml

SERVICE_YAML=$(cat ../templates/testcases/stress/service.yml)
SERVICE_TOTAL=2000

generate_service() {
  local name
  name="nginx-service-$1"
  echo "${SERVICE_YAML/SERVICENAME/$name}"
}

# apply deployment
kubectl apply -f ../templates/testcases/stress/nginx-pod-service.yml

# apply service
for (( i=0; i<SERVICE_TOTAL; i=i+1 )); do
  echo "Apply service $i"
  generate_service $i | kubectl apply -f '-'
done

