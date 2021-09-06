#!/bin/bash

source ../helpers.bash

TEST_INTERVAL=600

while true
do
  echo "Begin test at $(date)"
  parse_args $@
  if [[ ! -z ${trunk} ]]; then
      kubectl apply -f ../templates/testcases/stress/pod-networking.yml
  fi
  bats startup_time.bats
  if [ $? -ne 0 ]; then
      curl -X POST "https://oapi.dingtalk.com/robot/send?access_token=$TOKEN" -H 'cache-control: no-cache' -H 'content-type: application/json' -d '{
        "msgtype": "text",
        "text": {
            "content": "terway startup time test failed!"
          }
        }'
    else
      echo "Test succeed at $(date)"
    fi

    sleep $TEST_INTERVAL
done
