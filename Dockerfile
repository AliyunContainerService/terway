ARG CILIUM_LLVM_IMAGE=quay.io/cilium/cilium-llvm:0147a23fdada32bd51b4f313c645bcb5fbe188d6@sha256:24fd3ad32471d0e45844c856c38f1b2d4ac8bd0a2d4edf64cffaaa3fd0b21202
ARG CILIUM_BPFTOOL_IMAGE=quay.io/cilium/cilium-bpftool:b5ba881d2a7ec68d88ecd72efd60ac551c720701@sha256:458282e59657b8f779d52ae2be2cdbeecfe68c3d807ff87c97c8d5c6f97820a9
ARG CILIUM_IPROUTE2_IMAGE=quay.io/cilium/cilium-iproute2:4db2c4bdf00ce461406e1c82aada461356fac935@sha256:e4c9ba92996a07964c1b7cd97c4aac950754ec75d7ac8c626a99c79acd0479ab

FROM --platform=$BUILDPLATFORM ${CILIUM_LLVM_IMAGE} as llvm-dist
FROM --platform=$BUILDPLATFORM ${CILIUM_BPFTOOL_IMAGE} as bpftool-dist
FROM --platform=$BUILDPLATFORM ${CILIUM_IPROUTE2_IMAGE} as iproute2-dist

FROM --platform=$BUILDPLATFORM golang:1.17.2 as builder
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

FROM --platform=$BUILDPLATFORM calico/go-build:v0.57 as felix-builder
ARG GOPROXY
ENV GOPROXY $GOPROXY
ENV GIT_BRANCH=v3.20.2
ENV GIT_COMMIT=ab06c3940caa8ac201f85c1313b2d72d724409d2

RUN mkdir -p /go/src/github.com/projectcalico/ && cd /go/src/github.com/projectcalico/ && \
    git clone -b ${GIT_BRANCH} --depth 1 https://github.com/projectcalico/felix.git && \
    cd felix && [ "`git rev-parse HEAD`" = "${GIT_COMMIT}" ]
COPY policy/felix /terway_patch
RUN cd /go/src/github.com/projectcalico/felix && git apply /terway_patch/*.patch
RUN cd /go/src/github.com/projectcalico/felix && \
    go build -v -o bin/calico-felix -v -ldflags \
    "-X github.com/projectcalico/felix/buildinfo.GitVersion=${GIT_BRANCH} \
    -X github.com/projectcalico/felix/buildinfo.BuildDate=$(date -u +'%FT%T%z') \
    -X github.com/projectcalico/felix/buildinfo.GitRevision=${GIT_COMMIT} \
    -B 0x${GIT_COMMIT}" "github.com/projectcalico/felix/cmd/calico-felix" && \
    ( ldd bin/calico-felix 2>&1 | grep -q -e "Not a valid dynamic program" \
    -e "not a dynamic executable" || \
    ( echo "Error: bin/calico-felix was not statically linked"; false ) ) \
    && chmod +x /go/src/github.com/projectcalico/felix/bin/calico-felix

FROM --platform=$BUILDPLATFORM quay.io/cilium/cilium-builder:6ad397fc5d0e2ccba203d5c0fe5a4f0a08f6fb5a@sha256:d760b821c46ce41361a2d54386b12fd9831fbc0ba36539b86604706dd68f05d1 as cilium-builder
ARG GOPROXY
ENV GOPROXY $GOPROXY
ARG CILIUM_SHA=""
LABEL cilium-sha=${CILIUM_SHA}
LABEL maintainer="maintainer@cilium.io"
WORKDIR /go/src/github.com/cilium
RUN rm -rf cilium
ENV GIT_TAG=v1.10.1
ENV GIT_COMMIT=e6f34c3c98fe2e247fde581746e552d8cb18c33c
RUN git clone -b $GIT_TAG --depth 1 https://github.com/cilium/cilium.git && \
    cd cilium && \
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

FROM --platform=$BUILDPLATFORM ubuntu:20.04
RUN apt-get update && apt-get install -y kmod libelf1 libmnl0 iptables nftables kmod curl ipset bash ethtool bridge-utils socat grep findutils jq conntrack iputils-ping && \
    apt-get purge --auto-remove && apt-get clean && rm -rf /var/lib/apt/lists/*
COPY --from=llvm-dist /usr/local/bin/clang /usr/local/bin/llc /bin/
COPY --from=bpftool-dist /usr/local /usr/local
COPY --from=iproute2-dist /usr/local /usr/local
COPY --from=felix-builder /go/src/github.com/projectcalico/felix/bin/calico-felix /bin/calico-felix
COPY policy/policyinit.sh /bin/
COPY policy/uninstall_policy.sh /bin/
COPY init.sh /bin/
COPY --from=cilium-builder /tmp/install/ /
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/cmd/terway/terwayd /usr/bin/terwayd
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/plugin/terway/terway /usr/bin/terway
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/cmd/terway-cli/terway-cli /usr/bin/terway-cli
COPY hack/iptables-wrapper-installer.sh /iptables-wrapper-installer.sh
RUN /iptables-wrapper-installer.sh
ENTRYPOINT ["/usr/bin/terwayd"]
