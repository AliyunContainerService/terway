#!/bin/bash
set -e
set -x
cd ..
GIT_SHA=`git rev-parse --short HEAD || echo "HEAD"`
GOMAXPROCS=1 CGO_ENABLED=0 GOOS=linux go build -ldflags "-X \"main.gitVer=`git rev-parse --short HEAD 2>/dev/null`\" " -o terwayd . && cp terwayd ci/
cd plugin && CGO_ENABLED=0 GOOS=linux go build -o terway . && cp terway ../ci/
cd ../ci
cp -r ../script .

cat > Dockerfile <<EOF
FROM registry.aliyuncs.com/wangbs/netdia:latest
COPY script/ /bin/
RUN apk --update add ipset bash && chmod +x /bin/traffic && chmod +x /bin/policyinit.sh && rm -f /var/cache/apk/*
RUN curl -sSL -o /bin/calico-felix https://docker-plugin.oss-cn-shanghai.aliyuncs.com/calico-felix && chmod +x /bin/calico-felix
COPY terwayd terway /usr/bin/
ENTRYPOINT ["/usr/bin/terwayd"]
EOF
docker build --no-cache -t acs/terway:1.0-$GIT_SHA . 
