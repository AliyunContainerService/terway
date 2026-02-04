# syntax=docker/dockerfile:1-labs
ARG TERWAY_POLICY_IMAGE=registry-cn-zhangjiakou.ack.aliyuncs.com/acs/terway:policy-d45146a6@sha256:2a1641efacd98527056aa4d03f0ba184fdd24db1ba4caffa15701b54e497d214
ARG UBUNTU_IMAGE=registry.cn-hangzhou.aliyuncs.com/acs/ubuntu:24.04-update
ARG CILIUM_LLVM_IMAGE=quay.io/cilium/cilium-llvm:9f1bfe736009afb1fbb562718bbc42ea07d37d8e@sha256:a666a7a01a2dc610c3ab6e32f25ca5e294201f3cbbc01f233320c527955deee3
ARG CILIUM_BPFTOOL_IMAGE=quay.io/cilium/cilium-bpftool:0db3a73729ceb42e947d826bb96a655be79e5317@sha256:de23c9546c4eafab33f75d6f5d129947bbbafc132dbd113c0cecc9a61929e6b0
ARG CILIUM_IPTABLES_IMAGE=quay.io/cilium/iptables:67f517af50e18f64cd12625021f1c39246bb4f92@sha256:d075f03e89aacf51908346ec8ed5d251b8d3ad528ce30a710fcd074cdf91f11d

FROM --platform=$TARGETPLATFORM ${TERWAY_POLICY_IMAGE} AS policy-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_LLVM_IMAGE} AS llvm-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_BPFTOOL_IMAGE} AS bpftool-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_IPTABLES_IMAGE} AS iptables-dist

FROM --platform=$BUILDPLATFORM golang:1.25.6 AS builder
ARG GOPROXY
ARG TARGETOS
ARG TARGETARCH
ENV GOPROXY=$GOPROXY
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY --parents go.mod go.sum .git cmd daemon deploy deviceplugin pkg plugin rpc tests types ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    export CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH && \
    go build -tags default_build \
    -ldflags \
    "-s -w -X \"github.com/AliyunContainerService/terway/pkg/version.gitCommit=`git rev-parse HEAD`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/version.buildDate=`date -u +'%Y-%m-%dT%H:%M:%SZ'`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/version.gitVersion=`git describe --tags --match='v*' --abbrev=14`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/aliyun/credential.kubernetesAlicloudIdentity=terwayd/`git rev-parse --short HEAD 2>/dev/null`\"" -o terwayd ./cmd/terway && \
    go build -tags default_build -ldflags "-s -w" -o terway ./plugin/terway && \
    go build -tags default_build -ldflags "-s -w" -o terway-cli ./cmd/terway-cli

FROM --platform=$TARGETPLATFORM ${UBUNTU_IMAGE}
RUN apt-get update && apt-get install -y iproute2 kmod libelf1 libmnl0 iptables nftables kmod curl ipset bash ethtool bridge-utils socat grep findutils jq conntrack iputils-ping && \
    apt-get purge --auto-remove && apt-get clean && rm -rf /var/lib/apt/lists/*

RUN --mount=type=bind,from=iptables-dist,source=/iptables,target=/iptables \
    dpkg -i /iptables/*\.deb
RUN --mount=type=bind,source=/hack/iptables-wrapper-installer.sh,target=/iptables-wrapper-installer.sh \
    /iptables-wrapper-installer.sh --no-sanity-check

COPY --link --from=llvm-dist /usr/local/bin/clang /usr/local/bin/llc /usr/bin/
COPY --link --from=bpftool-dist /usr/local /usr/local
COPY --link --from=policy-dist /bin/calico-felix /usr/local/bin/calico-felix

COPY --link ../../../policy/policyinit.sh ./../../policy/uninstall_policy.sh ../../../hack/init.sh /usr/bin/
COPY --link --from=policy-dist \
    --exclude=usr/bin/cilium-operator-generic \
    --exclude=usr/bin/cilium-bugtool \
    --exclude=usr/bin/cilium-health* \
    /tmp/install/ /
COPY --link --from=builder /go/src/github.com/AliyunContainerService/terway/terwayd /go/src/github.com/AliyunContainerService/terway/terway /go/src/github.com/AliyunContainerService/terway/terway-cli /usr/bin/
ENTRYPOINT ["/usr/bin/terwayd"]
