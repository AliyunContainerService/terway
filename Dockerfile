ARG TERWAY_POLICY_IMAGE=registry.cn-hongkong.aliyuncs.com/acs/terway:policy-20220412-a5beea9@sha256:6e0345d8d4b5ed0d28bc3fade0478dae13e0ff03fec24a51f9c3408699360b09
ARG CILIUM_LLVM_IMAGE=quay.io/cilium/cilium-llvm:0147a23fdada32bd51b4f313c645bcb5fbe188d6@sha256:24fd3ad32471d0e45844c856c38f1b2d4ac8bd0a2d4edf64cffaaa3fd0b21202
ARG CILIUM_BPFTOOL_IMAGE=quay.io/cilium/cilium-bpftool:b5ba881d2a7ec68d88ecd72efd60ac551c720701@sha256:458282e59657b8f779d52ae2be2cdbeecfe68c3d807ff87c97c8d5c6f97820a9
ARG CILIUM_IPROUTE2_IMAGE=quay.io/cilium/cilium-iproute2:4db2c4bdf00ce461406e1c82aada461356fac935@sha256:e4c9ba92996a07964c1b7cd97c4aac950754ec75d7ac8c626a99c79acd0479ab

FROM --platform=$TARGETPLATFORM ${TERWAY_POLICY_IMAGE} as policy-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_LLVM_IMAGE} as llvm-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_BPFTOOL_IMAGE} as bpftool-dist
FROM --platform=$TARGETPLATFORM ${CILIUM_IPROUTE2_IMAGE} as iproute2-dist

FROM --platform=$TARGETPLATFORM golang:1.18.0 as builder
ARG GOPROXY
ENV GOPROXY $GOPROXY
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY go.sum go.sum
COPY go.mod go.mod
RUN go mod download
COPY . .
RUN cd cmd/terway && CGO_ENABLED=0 GOOS=linux go build -tags default_build \
    -ldflags \
    "-X \"k8s.io/client-go/pkg/version.gitCommit=`git rev-parse HEAD`\" \
    -X \"k8s.io/client-go/pkg/version.buildDate=`date -u +'%Y-%m-%dT%H:%M:%SZ'`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/aliyun.kubernetesAlicloudIdentity=Kubernetes.Alicloud/`git rev-parse --short HEAD 2>/dev/null`\"" -o terwayd .
RUN cd plugin/terway && CGO_ENABLED=0 GOOS=linux go build -tags default_build -o terway .
RUN cd cmd/terway-cli && CGO_ENABLED=0 GOOS=linux go build -tags default_build -o terway-cli .

FROM --platform=$TARGETPLATFORM ubuntu:20.04
RUN apt-get update && apt-get install -y kmod libelf1 libmnl0 iptables nftables kmod curl ipset bash ethtool bridge-utils socat grep findutils jq conntrack iputils-ping && \
    apt-get purge --auto-remove && apt-get clean && rm -rf /var/lib/apt/lists/*
COPY --from=llvm-dist /usr/local/bin/clang /usr/local/bin/llc /bin/
COPY --from=bpftool-dist /usr/local /usr/local
COPY --from=iproute2-dist /usr/local /usr/local
COPY --from=policy-dist /bin/calico-felix /bin/calico-felix
COPY policy/policyinit.sh /bin/
COPY policy/uninstall_policy.sh /bin/
COPY init.sh /bin/
COPY --from=policy-dist /tmp/install/ /
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/cmd/terway/terwayd /usr/bin/terwayd
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/plugin/terway/terway /usr/bin/terway
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/cmd/terway-cli/terway-cli /usr/bin/terway-cli
COPY hack/iptables-wrapper-installer.sh /iptables-wrapper-installer.sh
RUN /iptables-wrapper-installer.sh --no-sanity-check
ENTRYPOINT ["/usr/bin/terwayd"]
