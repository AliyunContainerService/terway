#!/bin/sh

# auto test network policy continuously with TEST_INTERVAL
TEST_INTERVAL=300

while true
do
  echo "Begin test at $(date)"

  bats network_policy.bats
  if [ $? -ne 0 ]; then
      curl -X POST "https://oapi.dingtalk.com/robot/send?access_token=$TOKEN" -H 'cache-control: no-cache' -H 'content-type: application/json' -d '{
        "msgtype": "text",
        "text": {
            "content": "terway network policy test failed!"
          }
        }'
    else
      echo "Test succeed at $(date)"
    fi

    sleep $TEST_INTERVAL
done
