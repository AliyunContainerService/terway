FROM golang:1.11 as builder
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X \"main.gitVer=`git rev-parse --short HEAD 2>/dev/null`\" " -o terwayd .
RUN cd plugin/terway && CGO_ENABLED=0 GOOS=linux go build -o terway .
RUN cd cli && CGO_ENABLED=0 GOOS=linux go build -o terway-cli .

FROM calico/go-build:v0.20 as felix-builder
RUN apk --no-cache add ip6tables tini ipset iputils iproute2 conntrack-tools file git
ENV GIT_BRANCH=v3.5.8
ENV GIT_COMMIT=7e12e362499ed281e5f5ca2747a0ba4e76e896b6
#ENV http_proxy=1.1.1.1:1080
#ENV https_proxy=1.1.1.1:1080
RUN mkdir -p /go/src/github.com/projectcalico/ && cd /go/src/github.com/projectcalico/ && \
    git clone -b ${GIT_BRANCH} https://github.com/projectcalico/felix.git && \
    cd felix && [ "`git rev-parse HEAD`" = "${GIT_COMMIT}" ]
COPY policy /terway_patch
RUN cd /go/src/github.com/projectcalico/felix && git apply /terway_patch/*.patch && glide up --strip-vendor || glide install --strip-vendor
RUN cd /go/src/github.com/projectcalico/felix && \
    go build -v -i -o bin/calico-felix-amd64 -v -ldflags \
    "-X github.com/projectcalico/felix/buildinfo.GitVersion=${GIT_BRANCH} \
    -X github.com/projectcalico/felix/buildinfo.BuildDate=$(date -u +'%FT%T%z') \
    -X github.com/projectcalico/felix/buildinfo.GitRevision=${GIT_COMMIT} \
    -B 0x${GIT_COMMIT}" "github.com/projectcalico/felix/cmd/calico-felix" && \
    ( ldd bin/calico-felix-amd64 2>&1 | grep -q -e "Not a valid dynamic program" \
    -e "not a dynamic executable" || \
    ( echo "Error: bin/calico-felix-amd64 was not statically linked"; false ) )

FROM alpine:3.8
COPY policy/policyinit.sh /bin/
COPY policy/uninstall_policy.sh /bin/
RUN apk --update add curl ipset bash iproute2 ethtool bridge-utils socat grep findutils && chmod +x /bin/policyinit.sh /bin/uninstall_policy.sh && rm -f /var/cache/apk/*
COPY --from=felix-builder /go/src/github.com/projectcalico/felix/bin/calico-felix-amd64 /bin/calico-felix
RUN chmod +x /bin/calico-felix
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/terwayd /usr/bin/terwayd
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/plugin/terway/terway /usr/bin/terway
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/cli/terway-cli /usr/bin/terway-cli
ENTRYPOINT ["/usr/bin/terwayd"]
