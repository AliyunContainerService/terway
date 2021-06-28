FROM golang:1.16 as builder
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X \"main.gitVer=`git rev-parse --short HEAD 2>/dev/null`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/aliyun.kubernetesAlicloudIdentity=Kubernetes.Alicloud/`git rev-parse --short HEAD 2>/dev/null`\"" -o terwayd .
RUN cd plugin/terway && CGO_ENABLED=0 GOOS=linux go build -o terway .
RUN cd cli && CGO_ENABLED=0 GOOS=linux go build -o terway-cli .

FROM calico/go-build:v0.20 as felix-builder
RUN apk --no-cache add ip6tables tini ipset iputils iproute2 conntrack-tools file git
ENV GIT_BRANCH=v3.5.8
ENV GIT_COMMIT=7e12e362499ed281e5f5ca2747a0ba4e76e896b6
RUN mkdir -p /go/src/github.com/projectcalico/ && cd /go/src/github.com/projectcalico/ && \
    git clone -b ${GIT_BRANCH} https://github.com/projectcalico/felix.git && \
    cd felix && [ "`git rev-parse HEAD`" = "${GIT_COMMIT}" ]
COPY policy/felix /terway_patch
RUN cd /go/src/github.com/projectcalico/felix && git apply /terway_patch/*.patch && glide up --strip-vendor || glide install --strip-vendor
RUN cd /go/src/github.com/projectcalico/felix && \
    go build -v -i -o bin/calico-felix-amd64 -v -ldflags \
    "-X github.com/projectcalico/felix/buildinfo.GitVersion=${GIT_BRANCH} \
    -X github.com/projectcalico/felix/buildinfo.BuildDate=$(date -u +'%FT%T%z') \
    -X github.com/projectcalico/felix/buildinfo.GitRevision=${GIT_COMMIT} \
    -B 0x${GIT_COMMIT}" "github.com/projectcalico/felix/cmd/calico-felix" && \
    ( ldd bin/calico-felix-amd64 2>&1 | grep -q -e "Not a valid dynamic program" \
    -e "not a dynamic executable" || \
    ( echo "Error: bin/calico-felix-amd64 was not statically linked"; false ) ) \
    && chmod +x /go/src/github.com/projectcalico/felix/bin/calico-felix-amd64

FROM quay.io/cilium/cilium-builder:2020-06-08@sha256:06868f045a14e38e8ff0e8ac03d66630cfa42eacffd777ae126e5692367fd8a6 as cilium-builder
ARG CILIUM_SHA=""
LABEL cilium-sha=${CILIUM_SHA}
LABEL maintainer="maintainer@cilium.io"
WORKDIR /go/src/github.com/cilium
ENV GIT_BRANCH=master
ENV GIT_TAG=v1.8.1
ENV GIT_COMMIT=5ce2bc7b34e988e1bd4edbeca4209a8df52636b5
RUN git clone https://github.com/cilium/cilium.git && \
    cd cilium && git checkout ${GIT_TAG} && \
    [ "`git rev-parse HEAD`" = "${GIT_COMMIT}" ]
COPY policy/cilium /cilium_patch
RUN cd cilium && git apply /cilium_patch/*.patch
ARG NOSTRIP
ARG LOCKDEBUG
ARG V
ARG LIBNETWORK_PLUGIN
#
# Please do not add any dependency updates before the 'make install' here,
# as that will mess with caching for incremental builds!
#
RUN cd cilium && make NOSTRIP=$NOSTRIP LOCKDEBUG=$LOCKDEBUG PKG_BUILD=1 V=$V LIBNETWORK_PLUGIN=$LIBNETWORK_PLUGIN \
    SKIP_DOCS=true DESTDIR=/tmp/install clean-container build-container install-container
RUN cp /tmp/install/opt/cni/bin/cilium-cni /tmp/install/usr/bin/


FROM ubuntu:20.04
RUN apt-get update && apt-get install -y kmod libelf1 libmnl0 iptables kmod curl ipset bash ethtool bridge-utils socat grep findutils jq && \
    apt-get purge --auto-remove && apt-get clean && rm -rf /var/lib/apt/lists/*
COPY --from=docker.io/cilium/cilium-llvm:178583d8925906270379830fb44641c38f7cc062 /bin/clang /bin/llc /bin/
COPY --from=docker.io/cilium/cilium-bpftool:f0bbd0cb389ce92b33ff29f0489c17c8e33f9da7 /bin/bpftool /bin/
COPY --from=docker.io/cilium/cilium-iproute2:044e7a6a43d5a42a8ce696535b3dbf773f82dbec /bin/tc /bin/ip /bin/ss /bin/
COPY --from=felix-builder /go/src/github.com/projectcalico/felix/bin/calico-felix-amd64 /bin/calico-felix
COPY policy/policyinit.sh /bin/
COPY policy/uninstall_policy.sh /bin/
COPY init.sh /bin/
COPY --from=cilium-builder /tmp/install/ /
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/terwayd /usr/bin/terwayd
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/plugin/terway/terway /usr/bin/terway
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/cli/terway-cli /usr/bin/terway-cli
COPY hack/iptables-wrapper-installer.sh /iptables-wrapper-installer.sh
RUN /iptables-wrapper-installer.sh
ENTRYPOINT ["/usr/bin/terwayd"]
