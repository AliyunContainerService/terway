#!/usr/bin/env bash

stress_deploys=()
stress_ns=stresstest

setup_env() {
	kubectl create ns stresstest
	kubectl run --namespace ${stress_ns} --image nginx nginx
	stress_deploys+=('nginx')
	if [[ ! -z ${include_eni} ]]; then
		kubectl run --namespace ${stress_ns} --limits='aliyun/eni=1' --image nginx nginx-eni
		stress_deploys+=('nginx-eni')
	fi
}


teardown() {
	for deploy in "${stress_deploys[@]}"; do
		kubectl delete deploy "${deploy}"
	done
	kubectl delete ns stresstest
}


max_scale=20
#eni_max_scale=10
scale_period=3 #minutes
scale_jitter=1 #minutes
test_scale() {
	deploy=$1
	while true; do
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
	while true; do
		sleep $((delete_period*60))
		podlist=($(kubectl -n ${stress_ns} get pod -l run="${deploy}" -o name))
		pod_len=${#podlist[@]}
		if [ "${pod_len}" -gt "${delete_count}" ]; then
			pod_len=${delete_count}
		fi
		for (( i=0; i<pod_len; i++ )) do
			kubectl -n ${stress_ns} delete "${podlist[$i]}"
		done
	done
}


while [[ $# -ge 1 ]]
do
    key=$1
    shift
    case "$key" in
        --include-eni)
            export include_eni=1
            ;;
        *)
            echo 'invalid argument'
            exit 1
            ;;
    esac
done

setup_env

test_scale nginx &
if [[ ! -z ${include_eni} ]]; then
	test_scale nginx-eni &
fi
test_random_delete nginx &
if [[ ! -z ${include_eni} ]]; then
	test_random_delete nginx-eni &
fi
wait

