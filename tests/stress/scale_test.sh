#!/usr/bin/env bash

source ../helpers.bash

stress_deploys=()
stress_ns=stress-scale
pod_networking_yaml=../templates/testcases/stress/pod-networking.yml
deploymet_yaml=../templates/testcases/stress/nginx-pod-scale.yml

total=200

setup_env() {
  if [[ ! -z ${trunk} ]]; then
  		kubectl apply -f ${pod_networking_yaml}
  fi
	kubectl apply -f ${deploymet_yaml}
	for i in $(kubectl get deploy -n ${stress_ns} -o name)
  do
    stress_deploys+=($i)
  done
}


teardown() {
	if [[ ! -z ${trunk} ]]; then
    kubectl delete -f ${pod_networking_yaml}
  fi
  kubectl delete -f ${deploymet_yaml}
}


max_scale=20
scale_period=3 #minutes
scale_jitter=1 #minutes
test_scale() {
	deploy=$1
	for (( i=0; i<10; i++ )); do
		scale_num=$((RANDOM%max_scale))
		kubectl -n ${stress_ns} scale --replicas ${scale_num} deploy "${deploy}"
		jitter_time=$((RANDOM%(scale_jitter*2*60)-scale_jitter*60))
		sleep $((scale_period*60+jitter_time))
	done
}

delete_count=2
delete_period=1 #minutes
test_random_delete() {
	deploy=$1
	for (( i=0; i<10; i++ )); do
		podlist=($(kubectl get pod -n ${stress_ns} -o name))
		pod_len=${#podlist[@]}
		if [ "${pod_len}" -gt "${delete_count}" ]; then
			pod_len=${delete_count}
		fi
		for (( i=0; i<pod_len; i++ )) do
			kubectl -n ${stress_ns} delete "${podlist[$i]}"
		done
		sleep $((delete_period*60))
	done
}

parse_args $@
setup_env
for (( i=0; i<total; i++)); do
  test_scale nginx-deployment
  test_random_delete nginx-deployment
done
teardown

