#!/usr/bin/env bats
load helpers

function setup() {
	kubectl delete ns network-test || true
	retry 30 3 object_not_exist ns network-test
}

@test "network policy" {
	kubectl apply -f templates/testcases/network_policy/network-policy.yml
    retry 5 20 bash -c "kubectl get pod -n network-test policy-cli | grep Completed"
    result=`kubectl get pod -n network-test -o jsonpath='{range .status.containerStatuses[*]}{.state.terminated.reason}{end}' policy-cli`
    [ "$result" = "CompletedCompleted" ]
    result=`kubectl get pod -n network-test -o jsonpath='{range .status.containerStatuses[*]}{.state.terminated.reason}{end}' non-policy-cli`
    [ "$result" = "CompletedError" ]
    kubectl delete -f templates/testcases/network_policy/network-policy.yml
    retry 30 2 object_not_exist ns network-test
}