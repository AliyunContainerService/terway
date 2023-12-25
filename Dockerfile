ARG TERWAY_POLICY_IMAGE=registry.cn-hongkong.aliyuncs.com/acs/terway:policy-20231222-e587c3c9@sha256:2e2d936d95a8eb4f0c822ce7b00dcfbbd0a3a4b0886655d321b19cdfb9b0c034
ARG UBUNTU_IMAGE=registry.cn-hangzhou.aliyuncs.com/acs/ubuntu:20.04-update
ARG CILIUM_LLVM_IMAGE=quay.io/cilium/cilium-llvm:547db7ec9a750b8f888a506709adb41f135b952e@sha256:4d6fa0aede3556c5fb5a9c71bc6b9585475ac9b1064f516d4c45c8fb691c9d9e
ARG CILIUM_BPFTOOL_IMAGE=quay.io/cilium/cilium-bpftool:78448c1a37ff2b790d5e25c3d8b8ec3e96e6405f@sha256:99a9453a921a8de99899ef82e0822f0c03f65d97005c064e231c06247ad8597d
ARG CILIUM_IPROUTE2_IMAGE=quay.io/cilium/cilium-iproute2:3570d58349efb2d6b0342369a836998c93afd291@sha256:1abcd7a5d2117190ab2690a163ee9cd135bc9e4cf8a4df662a8f993044c79342
ARG CILIUM_IPTABLES_IMAGE=quay.io/cilium/iptables-20.04:e6f83206c57e606282056903ffd3aab0183bdaed@sha256:7ce0de449d356a5259021dc13f2b00a8bddfbea57a1c91ff8f146d455cace9e5

FROM --platform=$TARGETPLATFORM ${TERWAY_POLICY_IMAGE} as policy-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_LLVM_IMAGE} as llvm-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_BPFTOOL_IMAGE} as bpftool-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_IPROUTE2_IMAGE} as iproute2-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_IPTABLES_IMAGE} as iptables-dist

FROM --platform=$BUILDPLATFORM golang:1.21.3 as builder
ARG GOPROXY
ARG TARGETOS
ARG TARGETARCH
ENV GOPROXY $GOPROXY
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY go.sum go.mod ./
RUN go mod download
COPY . .
RUN cd cmd/terway && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags default_build \
    -ldflags \
    "-s -w -X \"github.com/AliyunContainerService/terway/pkg/version.gitCommit=`git rev-parse HEAD`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/version.buildDate=`date -u +'%Y-%m-%dT%H:%M:%SZ'`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/version.gitVersion=`git describe --tags --match='v*' --abbrev=14`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/aliyun.kubernetesAlicloudIdentity=Kubernetes.Alicloud/`git rev-parse --short HEAD 2>/dev/null`\"" -o terwayd .
RUN cd plugin/terway && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags default_build -ldflags "-s -w" -o terway .
RUN cd cmd/terway-cli && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags default_build -ldflags "-s -w" -o terway-cli .

FROM --platform=$TARGETPLATFORM ${UBUNTU_IMAGE}
RUN apt-get update && apt-get install -y kmod libelf1 libmnl0 iptables nftables kmod curl ipset bash ethtool bridge-utils socat grep findutils jq conntrack iputils-ping && \
    apt-get purge --auto-remove && apt-get clean && rm -rf /var/lib/apt/lists/*

COPY --from=llvm-dist /usr/local/bin/clang /usr/local/bin/llc /bin/
COPY --from=bpftool-dist /usr/local /usr/local
COPY --from=iproute2-dist /usr/local /usr/local
COPY --from=iproute2-dist /usr/lib/libbpf* /usr/lib/
COPY --from=policy-dist /bin/calico-felix /bin/calico-felix
COPY --from=iptables-dist /iptables /iptables
RUN dpkg -i /iptables/*\.deb && rm -rf /iptables

COPY policy/policyinit.sh /bin/
COPY policy/uninstall_policy.sh /bin/
COPY init.sh /bin/
COPY --from=policy-dist /tmp/install/ /
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/cmd/terway/terwayd /go/src/github.com/AliyunContainerService/terway/plugin/terway/terway /go/src/github.com/AliyunContainerService/terway/cmd/terway-cli/terway-cli /usr/bin/
COPY hack/iptables-wrapper-installer.sh /iptables-wrapper-installer.sh
RUN /iptables-wrapper-installer.sh --no-sanity-check
ENTRYPOINT ["/usr/bin/terwayd"]
