# syntax=docker/dockerfile:1-labs
ARG TERWAY_POLICY_IMAGE=registry-cn-zhangjiakou.ack.aliyuncs.com/acs/terway:policy-71baf890@sha256:20e5741a0ad2dc4358ade7a8d41ebf192e6804bff94da95f34f1be7397b3cf4e
ARG UBUNTU_IMAGE=registry.cn-hangzhou.aliyuncs.com/acs/ubuntu:24.04-update
ARG CILIUM_IPROUTE2_IMAGE=quay.io/cilium/cilium-iproute2:3570d58349efb2d6b0342369a836998c93afd291@sha256:1abcd7a5d2117190ab2690a163ee9cd135bc9e4cf8a4df662a8f993044c79342
ARG CILIUM_LLVM_IMAGE=quay.io/cilium/cilium-llvm:02191b3a68f4003f075e69a61b61faa8d12d4b6d@sha256:54ca04f65a6de3aa5243a7da392aed342c64f579ebd94ea1290590ce77a433a8
ARG CILIUM_BPFTOOL_IMAGE=quay.io/cilium/cilium-bpftool:5a9c4852a21287686009bfe1cdc1fed6e7aabdea@sha256:188e398ee30456373530698d0d29de66dda0c4428068ea430027ceb7e9c15b7c
ARG CILIUM_IPTABLES_IMAGE=quay.io/cilium/iptables:1331e9b1b03f70c9d8b08626d9a7126963f86478@sha256:d761d967243aced2729adde1e332a9c9def6baeb61f5f6cde5758b04e9a79355

FROM --platform=$TARGETPLATFORM ${TERWAY_POLICY_IMAGE} AS policy-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_LLVM_IMAGE} AS llvm-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_BPFTOOL_IMAGE} AS bpftool-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_IPROUTE2_IMAGE} AS iproute2-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_IPTABLES_IMAGE} AS iptables-dist

FROM --platform=$BUILDPLATFORM golang:1.25.3 AS builder
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
RUN apt-get update && apt-get install -y kmod libelf1 libmnl0 iptables nftables kmod curl ipset bash ethtool bridge-utils socat grep findutils jq conntrack iputils-ping && \
    apt-get purge --auto-remove && apt-get clean && rm -rf /var/lib/apt/lists/*

RUN --mount=type=bind,from=iptables-dist,source=/iptables,target=/iptables \
    dpkg -i /iptables/*\.deb
RUN --mount=type=bind,source=/hack/iptables-wrapper-installer.sh,target=/iptables-wrapper-installer.sh \
    /iptables-wrapper-installer.sh --no-sanity-check

COPY --link --from=llvm-dist /usr/local/bin/clang /usr/local/bin/llc /usr/bin/
COPY --link --from=bpftool-dist /usr/local /usr/local
COPY --link --from=iproute2-dist /usr/local /usr/local
COPY --link --from=iproute2-dist /usr/lib/libbpf* /usr/lib/
COPY --link --from=policy-dist /bin/calico-felix /usr/local/bin/calico-felix

COPY --link ../../../policy/policyinit.sh ./../../policy/uninstall_policy.sh ../../../hack/init.sh /usr/bin/
COPY --link --from=policy-dist \
    --exclude=usr/bin/cilium-operator-generic \
    --exclude=usr/bin/cilium-bugtool \
    --exclude=usr/bin/cilium-health* \
    /tmp/install/ /
COPY --link --from=builder /go/src/github.com/AliyunContainerService/terway/terwayd /go/src/github.com/AliyunContainerService/terway/terway /go/src/github.com/AliyunContainerService/terway/terway-cli /usr/bin/
ENTRYPOINT ["/usr/bin/terwayd"]
