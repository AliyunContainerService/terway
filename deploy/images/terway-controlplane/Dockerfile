# syntax=docker/dockerfile:1-labs
ARG TERWAY_POLICY_IMAGE=registry-cn-zhangjiakou.ack.aliyuncs.com/acs/terway:policy-04728dad@sha256:819a778edd3a9a3693482ff424b3686481bfb3089adee5aeeb43af57d225d4c9

FROM --platform=$TARGETPLATFORM ${TERWAY_POLICY_IMAGE} AS policy-dist

FROM --platform=$BUILDPLATFORM golang:1.23.2 AS builder
ARG GOPROXY
ARG TARGETOS
ARG TARGETARCH
ENV GOPROXY=$GOPROXY
WORKDIR /go/src/github.com/AliyunContainerService/terway/
COPY --parents go.mod go.sum .git cmd daemon deploy deviceplugin pkg plugin rpc tests types ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags default_build \
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
COPY --from=cert /etc/ssl/certs /etc/ssl/certs
COPY --from=builder /go/src/github.com/AliyunContainerService/terway/terway-controlplane /usr/bin/terway-controlplane
COPY --from=policy-dist /tmp/install/usr/bin/cilium-operator-generic /usr/bin/cilium-operator-generic
USER 1000:1000

ENTRYPOINT  ["/usr/bin/terway-controlplane"]