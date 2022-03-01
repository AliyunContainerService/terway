ARG WINDBAGRELEASE_BASE_IMAGE_TAG="7.1.4-nanoserver-1809-20210812"

# --
# -- construct builder,
# -- inspired from https://raw.githubusercontent.com/rancher/rancher/master/package/windows/Dockerfile.agent
# --

FROM registry.cn-hongkong.aliyuncs.com/acs/golang-windows:1.17.3-windowsservercore as builder
MAINTAINER weijia.mwj "weijia.mwj@alibaba-inc.com"
ARG GOPROXY

# $ProgressPreference: https://github.com/PowerShell/PowerShell/issues/2138#issuecomment-251261324
SHELL ["powershell", "-Command", "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue';"]

# --
# -- download rancher-wins
# --

ENV WINS_VERSION v0.1.3
RUN $URL = ('https://aliacs-k8s-cn-hongkong.oss-cn-hongkong.aliyuncs.com/public/pkg/windows/wins/wins-{0}.zip' -f $env:WINS_VERSION); \
    \
    Write-Host ('Downloading wins from {0} ...' -f $URL); \
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; \
    Invoke-WebRequest -UseBasicParsing -OutFile c:\wins.zip -Uri $URL; \
    \
    Write-Host 'Expanding ...'; \
    Expand-Archive c:\wins.zip c:\; \
    \
    Write-Host 'Complete.'

# --
# -- download yq
# --

ENV YQ_VERSION v4.6.3
RUN $URL = ('https://aliacs-k8s-cn-hongkong.oss-cn-hongkong.aliyuncs.com/public/pkg/windows/yq/yq-{0}.zip' -f $env:YQ_VERSION); \
    \
    Write-Host ('Downloading yq from {0} ...' -f $URL); \
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; \
    Invoke-WebRequest -UseBasicParsing -OutFile c:\yq.zip -Uri $URL; \
    \
    Write-Host 'Expanding ...'; \
    Expand-Archive c:\yq.zip c:\; \
    \
    Write-Host 'Complete.'

# --
# -- download cni plugins
# --

ENV CNI_PLUGINS_VERSION v0.9.2-aliyun.4
RUN $URL = ('https://github.com/thxCode/containernetworking-plugins/releases/download/{0}/cni-plugins-windows-amd64-{0}.tgz' -f $env:CNI_PLUGINS_VERSION); \
    \
    Write-Host ('Downloading cni plugins from {0} ...' -f $URL); \
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; \
    New-Item -Force -ItemType Directory -Path c:\containernetworking\bin | Out-Null; \
    Invoke-WebRequest -UseBasicParsing -OutFile c:\containernetworking\bin\plugins.tgz -Uri $URL; \
    \
    Write-Host 'Expanding ...'; \
    pushd c:\containernetworking\bin; \
    tar -xf plugins.tgz; \
    popd; \
    \
    Write-Host 'Complete.'

# --
# -- download calico-felix
# --

ENV CALICO_VERSION v3.21.2.3-8d55145b-aliyun
RUN $URL = ('https://aliacs-k8s-cn-hongkong.oss-cn-hongkong.aliyuncs.com/public/pkg/windows/calico/calico-windows-{0}.zip' -f $env:CALICO_VERSION); \
    \
    Write-Host ('Downloading calico-felix from {0} ...' -f $URL); \
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; \
    Invoke-WebRequest -UseBasicParsing -OutFile c:\calico.zip -Uri $URL; \
    \
    Write-Host 'Expanding ...'; \
    Expand-Archive c:\calico.zip c:\; \
    \
    Write-Host 'Complete.'


# --
# -- build
# --

WORKDIR /gopath/src/github.com/AliyunContainerService/terway/

# specify environment
ENV GOPROXY=$GOPROXY \
    CGO_ENABLED=0 \
    GOOS=windows \
    GO111MODULE=on

# cache vendor
COPY go.mod .
COPY go.sum .
RUN go mod download

# go build
COPY . .
RUN Write-Host 'Getting hash ...'; \
    $VSC_DATE = $(Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ'); \
    $VSC_VERSION = $(git describe --tags --match='v*' --long --abbrev=7 --dirty 2>$null); \
    $VSC_HASH = $(git rev-parse --short HEAD 2>$null); \
    \
    Write-Host 'Building terwayd ...'; \
    $TERWAYD_BUILD_LDFLAGS=$('-X "k8s.io/client-go/pkg/version.buildDate={0}" -X "k8s.io/client-go/pkg/version.gitVersion={1}" -X "k8s.io/client-go/pkg/version.gitCommit={2}" -X "github.com/AliyunContainerService/terway/pkg/aliyun.kubernetesAlicloudIdentity=Kubernetes.Alicloud/{2}"' -f $VSC_DATE,$VSC_VERSION,$VSC_HASH); \
    go build -tags default_build -trimpath \
        -ldflags $TERWAYD_BUILD_LDFLAGS \
        -o terwayd.exe ./cmd/terway; \
    \
    Write-Host 'Building terway cni plugin ...'; \
    $TERWAY_BUILD_LDFLAGS=$('-X "github.com/containernetworking/plugins/pkg/utils/buildversion.BuildVersion={0}({1})"' -f $VSC_VERSION,$VSC_DATE); \
    go build -tags default_build -trimpath \
        -ldflags $TERWAY_BUILD_LDFLAGS \
        -o terway.exe ./plugin/terway; \
    \
    Write-Host 'Building terway cli ...'; \
    go build -tags default_build -trimpath \
        -o terway-cli.exe ./cmd/terway-cli; \
    \
    Write-Host 'Complete.'


# --
# -- construct terway
# --

FROM mcr.microsoft.com/powershell:${WINDBAGRELEASE_BASE_IMAGE_TAG}
MAINTAINER weijia.mwj "weijia.mwj@alibaba-inc.com"

USER ContainerAdministrator

ENV CLUSTER_SERVER="" \
    LOG_LEVEL="info" \
    BACKEND_TYPE="" \
    DEBUG_SOCKET="unix:///var/run/eni/eni_debug.socket" \
    POOL_CHECK_PERIOD_SECONDS="0"\
    NODE_NAME="" \
    POD_NAMESPACE="" \
    POD_NAME="" \
    CONTAINER_NAME="" \
    ENABLE_METRICS="false" \
    DISABLE_POLICY="false" \
    NETWORK_NAME_REGEX="^ethernet_.*"

# copy artifacts from builder
COPY --from=builder /Windows/System32/certoc.exe /Windows/System32/netapi32.dll /Windows/System32/
COPY --from=builder /yq/yq.exe /wins/wins.exe /Windows/System32/
COPY --from=builder /containernetworking/bin/host-local.exe /opt/cni/bin/
COPY --from=builder /CalicoWindows/calico-felix.exe /opt/bin/
COPY --from=builder /gopath/src/github.com/AliyunContainerService/terway/terway-cli.exe /Windows/System32/
COPY --from=builder /gopath/src/github.com/AliyunContainerService/terway/terway.exe /opt/cni/bin/
COPY --from=builder /gopath/src/github.com/AliyunContainerService/terway/terwayd.exe /opt/bin/

# copy scripts from host
COPY policy/*.ps1 /
COPY entrypoint.ps1 /

ENTRYPOINT ["pwsh.exe", "-NoLogo"]
