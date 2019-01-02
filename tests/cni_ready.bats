#!/usr/bin/env bats
load helpers

@test "terway ds ready" {
	terway_ready_count="$(kubectl get ds terway -n kube-system -o jsonpath='{.status.numberReady}')"
	node_count="$(kubectl get node -o name | wc -l)"
	echo $terway_ready_count " " $node_count
	debug_output
	[ "$terway_ready_count" -eq "$node_count" ]
}

@test "node device plugin" {
	if [ "$category" = "vpc" ]; then
		device_plugin_count="$(kubectl get node -o yaml | grep aliyun/eni | wc -l)"
		node_count="$(kubectl get node -o name | wc -l)"
		[ "$device_plugin_count" -eq "$(( $node_count * 2 ))" ]
	fi
}
