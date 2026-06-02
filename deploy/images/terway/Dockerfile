ARG TERWAY_POLICY_IMAGE=registry-cn-zhangjiakou.ack.aliyuncs.com/acs/terway:policy-14cd6c0e@sha256:ded6e8a952bd33e76a40c828c091f8ea384b161fae1a99f67d31bdeb8e57a7d9
ARG UBUNTU_IMAGE=registry.cn-hangzhou.aliyuncs.com/acs/ubuntu:24.04-update
ARG CILIUM_LLVM_IMAGE=quay.io/cilium/cilium-llvm:1763560084-a4eb8f4@sha256:cadc2001d43a1f410280aa82178fdfd5558916e6f9ca0acd52c22ba5b023458a
ARG CILIUM_BPFTOOL_IMAGE=quay.io/cilium/cilium-bpftool:5a9c4852a21287686009bfe1cdc1fed6e7aabdea@sha256:188e398ee30456373530698d0d29de66dda0c4428068ea430027ceb7e9c15b7c
ARG CILIUM_IPTABLES_IMAGE=quay.io/cilium/iptables:1331e9b1b03f70c9d8b08626d9a7126963f86478@sha256:d761d967243aced2729adde1e332a9c9def6baeb61f5f6cde5758b04e9a79355

FROM --platform=$TARGETPLATFORM ${TERWAY_POLICY_IMAGE} AS policy-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_LLVM_IMAGE} AS llvm-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_BPFTOOL_IMAGE} AS bpftool-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_IPTABLES_IMAGE} AS iptables-dist

FROM --platform=$BUILDPLATFORM golang:1.26.2 AS builder
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
