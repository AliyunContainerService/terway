FROM golang:1.9.4 as builder
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X \"main.gitVer=`git rev-parse --short HEAD 2>/dev/null`\" " -o terwayd .
RUN cd plugin && CGO_ENABLED=0 GOOS=linux go build -o terway .

FROM alpine:3.8
COPY script/ /bin/
RUN apk --update add curl ipset bash iproute2 ethtool bridge-utils && chmod +x /bin/traffic && chmod +x /bin/policyinit.sh && rm -f /var/cache/apk/*
RUN curl -sSL -o /bin/calico-felix https://docker-plugin.oss-cn-shanghai.aliyuncs.com/calico-felix && chmod +x /bin/calico-felix
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/terwayd /usr/bin/terwayd
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/plugin/terway /usr/bin/terway
ENTRYPOINT ["/usr/bin/terwayd"]
