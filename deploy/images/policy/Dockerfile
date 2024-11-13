# syntax=docker/dockerfile:1.11.1
FROM --platform=$TARGETPLATFORM calico/go-build:v0.94 AS felix-builder
ARG GOPROXY
ARG GIT_VERSION
ENV GOPROXY $GOPROXY
ENV GIT_BRANCH=v3.24.5
ENV GIT_COMMIT=f1a1611acb98d9187f48bbbe2227301aa69f0499

RUN mkdir -p /go/src/github.com/projectcalico/ && cd /go/src/github.com/projectcalico/ && \
    git clone -b ${GIT_BRANCH} --depth 1 https://github.com/projectcalico/calico.git && \
    cd calico && [ "`git rev-parse HEAD`" = "${GIT_COMMIT}" ]
COPY ../../../policy/felix /terway_patch
RUN cd /go/src/github.com/projectcalico/calico && git apply /terway_patch/*.patch
RUN cd /go/src/github.com/projectcalico/calico && \
    CGO_ENABLED=0 go build -o bin/calico-felix -ldflags \
    "-s -w -X github.com/projectcalico/calico/felix/buildinfo.GitVersion=${GIT_BRANCH} \
    -X github.com/projectcalico/calico/felix/buildinfo.BuildDate=$(date -u +'%FT%T%z') \
    -X github.com/projectcalico/calico/felix/buildinfo.GitRevision=${GIT_VERSION} \
    -B 0x${GIT_VERSION}" "github.com/projectcalico/calico/felix/cmd/calico-felix" && \
    ( ! $(readelf -d bin/calico-felix | grep -q NEEDED) || ( echo "Error: bin/calico-felix was not statically linked"; false )) \
    && chmod +x /go/src/github.com/projectcalico/calico/bin/calico-felix

FROM --platform=$TARGETPLATFORM quay.io/cilium/cilium-builder:86e436425fc11708b65423805e3b9939071e55e9@sha256:a440f8ca734205b5d61a483f0ac99aaf758ba89f1d2a8892219b6c0f8c475695 as cilium-builder
ARG GOPROXY
ENV GOPROXY=$GOPROXY
ARG CILIUM_SHA=""
ARG GIT_VERSION=""
LABEL cilium-sha=${CILIUM_SHA}
LABEL maintainer="maintainer@cilium.io"
WORKDIR /go/src/github.com/cilium
RUN rm -rf cilium
ENV GIT_TAG=v1.16.8
ENV GIT_COMMIT=dc65ba9bb5f8201e0f51babfb6e7a02de2527460
RUN git clone -b $GIT_TAG --depth 1 https://github.com/cilium/cilium.git && \
    cd cilium && git config --global user.email terway && git config --global user.name terway && \
    [ "`git rev-parse HEAD`" = "${GIT_COMMIT}" ]
COPY ../../../policy/cilium /cilium_patch
RUN cd cilium && git am --whitespace=fix /cilium_patch/*.patch
ARG NOSTRIP
ARG LOCKDEBUG
ARG V
ARG LIBNETWORK_PLUGIN
#
# Please do not add any dependency updates before the 'make install' here,
# as that will mess with caching for incremental builds!
#
RUN cd cilium && make NOSTRIP=$NOSTRIP LOCKDEBUG=$LOCKDEBUG PKG_BUILD=1 GIT_VERSION=${GIT_VERSION} V=$V LIBNETWORK_PLUGIN=$LIBNETWORK_PLUGIN \
    SKIP_DOCS=true DESTDIR=/tmp/install clean-container build-container install-container
RUN cp /tmp/install/opt/cni/bin/cilium-cni /tmp/install/usr/bin/

RUN cd /go/src/github.com/cilium/cilium/operator && make cilium-operator-generic \
    && mv cilium-operator-generic /tmp/install/usr/bin/

FROM scratch
COPY --from=felix-builder /go/src/github.com/projectcalico/calico/bin/calico-felix /bin/calico-felix
COPY --from=cilium-builder /tmp/install/ /tmp/install/
