#!/usr/bin/env bats
load helpers

# This testcase for measuring the deployment time of pods
# see: templates/testcases/network_connection/nginx-pod.yml

# get interval of a pod from "Initialized" to "Ready"
function get_interval() {
  # check pod ready
  local ready
  ready=$(kubectl get $1 -o jsonpath='{range .status.conditions[?(@.type == "Ready")]}{.status}{end}')
  [ $ready = "True" ]

  local init_time
  local ready_time

  init_time=$(kubectl get $1 -o jsonpath='{range .status.conditions[?(@.type == "Initialized")]}{.lastTransitionTime}{end}')
  ready_time=$(kubectl get $1 -o jsonpath='{range .status.conditions[?(@.type == "Ready")]}{.lastTransitionTime}{end}')
  init_time=$(date --date "$init_time" "+%s")
  ready_time=$(date --date "$ready_time" "+%s")

  echo $((ready_time - init_time))
}

# executed before each test
setup() {
    # make log dir
    mkdir logs || true
    # clean deployment
    kubectl delete deployment nginx-deployment || true
    retry 30 3 object_not_exist pod -l app=nginx-test
}

@test "startup pod" {
  # apply deployment, with name "nginx-deployment" and pod label "app=nginx-test"
  kubectl apply -f templates/testcases/network_connection/nginx-pod.yml
  # wait for all pods ready
  retry 20 5 deployment_ready deployment nginx-deployment
  # wait for readiness probe to detect the network connectivity
  sleep 10
  # get intervals
  local file_name="logs/startup_time_$(date "+%m%d.%H-%M-%S").log"
  local count=0
  local max=0
  # initial value of min should be bigger than timeout (wait for all pods ready)
  local min=1000
  local avg=0

  for i in $(kubectl get pod -l app=nginx-test --field-selector="status.phase=Running" -o name)
  do
    count=$((count + 1))

    local interval
    interval=$(get_interval $i)

    echo "$interval  $i" >> $file_name # <interval>\t<pod_name>

    if [ $min -gt $interval ]; then min=$interval; fi
    if [ $max -lt $interval ]; then max=$interval; fi
    avg=$((avg + interval))
  done
  avg=$((avg / count))

  echo "min: $min, max: $max, avg: $avg" >> $file_name

  # delete resources
  kubectl delete -f templates/testcases/network_connection/nginx-pod.yml
}

