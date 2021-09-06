#!/bin/bash

# Print run outputs to test.out
function debug_output() {
    echo test: $BATS_TEST_NAME line: $BATS_TEST_NUMBER
	echo test: $BATS_TEST_NAME line: $BATS_TEST_NUMBER  >> test.out
	printf '%s\n' "${lines[@]}"
	printf '%s\n' "${lines[@]}" >> test.out
}

function debug_echo() {
	echo test: $BATS_TEST_NAME line: $BATS_TEST_NUMBER >> test.out
	echo "$@" >> test.out
}

# Retry a command $1 times until it succeeds. Wait $2 seconds between retries.
function retry() {
	local attempts=$1
	shift
	local delay=$1
	shift
	local i

	for ((i=0; i < attempts; i++)); do
		run "$@"
		if [[ "$status" -eq 0 ]] ; then
			return 0
		fi
		sleep $delay
	done

	echo "Command \"$@\" failed $attempts times. Output: $output"
	false
}

function object_exist() {
	run kubectl get $@
	if [[ "$status" -eq 0 ]] && [[ ${#lines[@]} -gt 1 ]]; then
		return 0
	fi
	echo "object $@ not ready, status: $status, lines: ${#lines[@]} output $output"
	false
}

function pod_running() {
	run kubectl get $@
	if [[ "$status" -eq 0 ]] && [[ ${#lines[@]} -gt 1 ]] && echo $output | grep -q "Running"; then
		return 0
	fi
	echo "object $@ not ready, status: $status, lines: ${#lines[@]} output $output"
	false
}

function pods_all_running() {
	run kubectl get $@ --no-headers
	if [[ "$status" -eq 0 ]] && [[ ${#lines[@]} -gt 0 ]]; then
	  local running
	  local all
	  running=$(echo "$output" | grep -c "Running")
    all=$(echo "$output" | wc -l)
		if [[ "$running" -eq "$all" ]]; then
		  return 0
		fi
	fi
	echo "object $@ not ready, status: $status, lines: ${#lines[@]} output $output"
	false
}

function object_not_exist() {
	run kubectl get $@
	if [[ "$status" -gt 0 ]] || [[ ${#lines[@]} -eq 1 ]]; then
		return 0
	fi
	echo "object $@ exist, status: $status, lines: ${#lines[@]} output $output"
	false
}

function loadbalancer_ready() {
	run kubectl get $@
	if [[ "$status" -eq 0 ]] && [[ ${#lines[@]} -gt 1 ]]; then
		if echo $output | grep -q "pending"; then
			false
			echo "object $@ pending, status: $status, lines: ${#lines[@]} output $output"
			return 1
		fi
		return 0
	fi
	echo "object $@ exist, status: $status, lines: ${#lines[@]} output $output"
	false
}

function deployment_ready() {
  run kubectl get $@ -o json
  if [[ "$status" -eq 0 ]] && [[ ${#lines[@]} -gt 1 ]] && echo $output | jq ".status.replicas == .status.readyReplicas" | grep "true"; then
		return 0
	fi
	echo "deployment $@ not ready, status: $status, lines: ${#lines[@]}"
	false
}

# Prepare curl operation
function prepare_curl_options() {
	if [ x"$DOCKER_TLS_VERIFY" = x"1" ]; then
		DOCKER_HOST_HTTPS=`echo $DOCKER_HOST | sed 's/tcp:\/\//https:\/\//g'`
		CURL_OPTION="-sw \\\\n%{http_code} --insecure  --cert \"$DOCKER_CERT_PATH/cert.pem\" --key \"$DOCKER_CERT_PATH/key.pem\""
		#DOCKER_URL=$DOCKER_HOST_HTTPS/$API_VERSION
		DOCKER_URL=$DOCKER_HOST_HTTPS
	else
		DOCKER_HOST_HTTP=`echo $DOCKER_HOST | sed 's/tcp:\/\//http:\/\//g'`
		CURL_OPTION="-sw \\\\n%{http_code}"
		#DOCKER_URL=$DOCKER_HOST_HTTP/$API_VERSION
		DOCKER_URL=$DOCKER_HOST_HTTP
	fi
}

function curl_get() {
	echo "curl $CURL_OPTION $DOCKER_URL$@" >> test.out
	echo $CURL_OPTION $DOCKER_URL$@ | xargs -t curl
}

function curl_post_json() {
	url="$DOCKER_URL$1"
	shift

	echo $CURL_OPTION $url -d "'$@'"| xargs -t curl -X POST --header "Content-Type:application/json;charset=UTF-8"

}

function curl_post_yaml() {
	url="$DOCKER_URL$1"
	shift
	echo $CURL_OPTION $url $@| xargs -t curl -X POST --header "Content-Type:text/yaml;charset=UTF-8"
}

function curl_post() {
	echo $CURL_OPTION $DOCKER_URL$@ | xargs -t curl -X POST
}

function curl_delete() {
	echo $CURL_OPTION $DOCKER_URL$@ | xargs -t curl -X DELETE
}

function status_code() {
	echo ${lines[${#lines[@]}-1]}
}

function parse_args() {
  while [[ $# -ge 1 ]]; do
    key=$1
      shift
      case "$key" in
        --trunk)
          export trunk=1
          ;;
        *)
          echo 'invalid argument'
          exit 1
          ;;
      esac
  done
}

prepare_curl_options