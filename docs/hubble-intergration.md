# Hubble 接入

## 背景

- 当 Terway 处于 IPVLAN + Cilium 模式中时，我们可以启用 Cilium Agent 的 Hubble 观测能力。
- 本文档分为两部分，第一部分介绍如何打开节点上 Terway 内置 Cilium Agent 的 Hubble 观测能力，第二部分介绍如何部署 Hubble UI、Hubble Relay 进行网络流量展示 如您使用阿里云 ACK
  集群，第二部分可以选用应用目录 `ack-terway-hubble` 组件进行部署。

## 打开 Hubble 观测能力操作流程

1. 执行以下命令，打开 Terway 配置文件修改

    ```bash
    kubectl -n kube-system edit cm eni-config -o yaml
    ```

2. 可以看到默认的配置包含以下内容，请参考以下注释部分配置：

   ```json
   10-terway.conf: |
   {
     "cniVersion": "0.4.0",
     "name": "terway",
     "eniip_virtual_type": "IPVlan",
     // 新增以下配置，以打开 Hubble 网络流量分析
     "cilium_enable_hubble": "true",
     // 打开 Hubble 网络流量信息暴露地址，默认为 4244 端口
     "cilium_hubble_listen_address": ":4244",
     // Hubble Metrics 暴露的地址，默认为 9091
     "cilium_hubble_metrics_server": ":9091",
     // Hubble 需要采集的 Metrics，以逗号分割，以下为全量支持的 Metrics
     // 打开 Metrics 数量多对性能有一定影响，目前不支持 dns、http 这类 L7 的能力
     "cilium_hubble_metrics": "drop,tcp,flow,port-distribution,icmp",
     "type": "terway"
   }
   ```

3. 执行以下命令，过滤出当前 Terway 的所有 DaemonSet 容器组

   ```bash
   kubectl -n kube-system get pod | grep terway-eniip
   terway-eniip-7d56l         2/2     Running   0          30m
   terway-eniip-s7m2t         2/2     Running   0          30m
   ```

4. 执行以下命令，触发 Terway 容器组重建

   ```bash
   kubectl -n kube-system delete pod terway-eniip-7d56l terway-eniip-s7m2t
   ```

5. 登录任意集群节点，执行以下命令，查看 Terway 配置文件，如果包含刚刚添加的 `cilium_enable_hubble` 参数，则说明变更成功

    ```bash
    cat /etc/cni/net.d/*
    ```

6. 目前您已经打开 Cilium Agent 的 Hubble 观测能力暴露的配置，如您为阿里云 ACK 集群，可以在控制台应用目录中安装 `ack-terway-hubble` 完成 Hubble UI + Hubble Relay
   的部署。

## 部署 Hubble UI + Hubble Relay 操作流程

1. 执行以下命令部署，需要本地安装有 `Helm v3`：

   ```bash
   # deploy hubble-relay and hubble-ui
   git clone github.com/cilium/cilium
   git checkout v1.8.1
   helm install hubble-ui install/kubernetes/cilium/charts/hubble-ui --set global.hubble.ui.enabled=true --set global.hubble.enabled=true --set global.hubble.relay.enabled=true --set ingress.enabled=true --set ingress.hosts={hubble.local} --namespace kube-system
   helm install hubble-relay install/kubernetes/cilium/charts/hubble-relay  --set global.hubble.enabled=true --set global.hubble.relay.enabled=true --set global.hubble.socketPath=/var/run/cilium/hubble.sock --set image.repository=quay.io/cilium/hubble-relay:v1.8.1 --namespace kube-system
   ```

2. 默认情况下，`hubble-relay` 上面有一个亲和性，原意是为了和 Cilium Agent 一起部署，需要删除：

   ```yaml
   # kubectl -n kube-system edit deployment hubble-relay
   spec:
     template:
       spec:
         // 删除此处 affinity
         affinity:
           podAffinity:
             requiredDuringSchedulingIgnoredDuringExecution:
               - labelSelector:
                 matchExpressions:
                   - key: k8s-app
                   operator: In
                   values:
                   - cilium
             topologyKey: kubernetes.io/hostname
         // 以下不删除
         containers:
         ...
   ```

3. 创建以下 SVC，用于被 Prometheus 采集 Metrics：

   ```yaml
   ---
   kind: Service
   apiVersion: v1
   metadata:
     name: hubble-metrics
     namespace: kube-system
     annotations:
       prometheus.io/scrape: 'true'
       prometheus.io/port: '9091'
     labels:
       k8s-app: hubble
   spec:
     clusterIP: None
     type: ClusterIP
     ports:
     - name: hubble-metrics
       port: 9091
       protocol: TCP
       targetPort: 9091
     selector:
       app: terway-eniip
   ```

4. 访问 Hubble WEB UI 修改本地 hosts 绑定，将 `hubble.local` 指向 Ingress `hubble-ui` 的 IP 地址，访问 `http://hubble.local:80`

5. 采集 Hubble Metrics 配置 Prometheus 采集 `kube-system` 下 `hubble-metrics` 即可采集到 Hubble 暴露的指标，可参考 [cilium] 进行 Dashboard 配置。

[cilium]: https://docs.cilium.io/en/v1.8/operations/metrics/
