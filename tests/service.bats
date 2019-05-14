#!/usr/bin/env bats
load helpers

function setup() {
	kubectl delete -f templates/testcases/service/loadbalancer.yml || true
	retry 20 2 object_not_exist svc -l test=lbservice
	kubectl apply -f templates/testcases/service/loadbalancer.yml
	retry 20 2 object_exist svc -l test=lbservice
	retry 10 5 loadbalancer_ready svc -l test=lbservice

	retry 20 5 pod_running pod -l app=spod
}

function teardown() {
	kubectl delete -f templates/testcases/service/loadbalancer.yml || true
	retry 20 2 object_not_exist svc -l test=lbservice
}

@test "test loadbalancer service" {
	ip_addr=$(kubectl get svc loadbalancercluster -o jsonpath='{range .status.loadBalancer.ingress[*]}{.ip}{end}')
	retry 5 5 curl $ip_addr
	[ "$status" -eq 0 ]
}

@test "test loadbalancer service traffic local" {
	# eni not support local traffic policy
	if [ "$category" != "eni-only" ]; then
		ip_addr=$(kubectl get svc loadbalancerlocal -o jsonpath='{range .status.loadBalancer.ingress[*]}{.ip}{end}')
		retry 5 5 curl $ip_addr
		[ "$status" -eq 0 ]
	fi
}