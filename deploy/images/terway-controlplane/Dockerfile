# syntax=docker/dockerfile:1-labs
ARG TERWAY_POLICY_IMAGE=registry-cn-zhangjiakou.ack.aliyuncs.com/acs/terway:policy-71baf890@sha256:20e5741a0ad2dc4358ade7a8d41ebf192e6804bff94da95f34f1be7397b3cf4e

FROM --platform=$TARGETPLATFORM ${TERWAY_POLICY_IMAGE} AS policy-dist

FROM --platform=$BUILDPLATFORM golang:1.25.5 AS builder
ARG GOPROXY
ARG TARGETOS
ARG TARGETARCH
ENV GOPROXY=$GOPROXY
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY --parents go.mod go.sum .git cmd daemon deploy deviceplugin pkg plugin rpc tests types ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags default_build \
    -ldflags \
    "-X \"github.com/AliyunContainerService/terway/pkg/version.gitCommit=`git rev-parse HEAD`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/version.gitVersion=`git describe --tags --match='v*' --abbrev=14`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/version.buildDate=`date -u +'%Y-%m-%dT%H:%M:%SZ'`\" \
    -X \"github.com/AliyunContainerService/terway/pkg/aliyun/credential.kubernetesAlicloudIdentity=terway-controlplane/`git rev-parse --short HEAD 2>/dev/null`\"" \
    -o terway-controlplane  cmd/terway-controlplane/terway-controlplane.go

FROM --platform=$TARGETPLATFORM debian:stable-slim AS cert
RUN apt-get update && apt-get -uy upgrade
RUN apt-get -y install ca-certificates && update-ca-certificates

FROM --platform=$TARGETPLATFORM scratch
WORKDIR /
COPY --link --from=cert /etc/ssl/certs /etc/ssl/certs
COPY --link --from=builder /go/src/github.com/AliyunContainerService/terway/terway-controlplane /usr/bin/terway-controlplane
COPY --link --from=policy-dist /tmp/install/usr/bin/cilium-operator-generic /usr/bin/cilium-operator-generic
USER 1000:1000

ENTRYPOINT  ["/usr/bin/terway-controlplane"]