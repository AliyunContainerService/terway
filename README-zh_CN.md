# Terway 网络插件  

CNI plugin for alibaba cloud VPC/ENI

[![Go Report Card](https://goreportcard.com/badge/github.com/AliyunContainerService/terway)](https://goreportcard.com/report/github.com/AliyunContainerService/terway)
[![codecov](https://codecov.io/gh/AliyunContainerService/terway/branch/main/graph/badge.svg)](https://codecov.io/gh/AliyunContainerService/terway)
[![Linter](https://github.com/AliyunContainerService/terway/workflows/check/badge.svg)](https://github.com/marketplace/actions/super-linter)

[English](./README.md) | 简体中文

## 安装Kubernetes

* 准备阿里云ECS机器，我们验证过的ECS镜像是`Centos 7.4/7.6`
* 使用kubeadm的[指导文档](https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/)来创建集群

安装好了之后要:

* 将iptables的policy换成ACCEPT，`iptables -P FORWARD ACCEPT`。
* 检查节点上的"rp_filter"内核参数，并在每个节点上将其设置为"0"。

通过`kubectl get cs`验证集群安装完成

## 安装terway插件

Terway有两种安装模式：

* VPC模式

    VPC模式，使用Aliyun VPC路由来打通网络，可以使用独立ENI给Pod，安装方式：<br />
    修改[terway.yml](./terway.yml)文件中的eni.conf的配置中的授权和网段配置，以及Network的网段配置，然后通过`kubectl apply -f terway.yml`来安装terway插件。

* ENI多IP模式

    ENI多IP模式，使用Aliyun ENI的辅助IP来打通网络，不受VPC的路由条目限制，安装方式：<br />
    修改[terway-multiip.yml](./terway-multiip.yml)文件中的eni.conf的配置中的授权和资源配置，然后通过`kubectl apply -f terway-multiip.yml`来安装terway插件。

Terway需要授权中包含以下 [`RAM 权限`](https://ram.console.aliyun.com/)

```json
{
  "Version": "1",
  "Statement": [{
      "Action": [
        "ecs:CreateNetworkInterface",
        "ecs:DescribeNetworkInterfaces",
        "ecs:AttachNetworkInterface",
        "ecs:DetachNetworkInterface",
        "ecs:DeleteNetworkInterface",
        "ecs:DescribeInstanceAttribute",
        "ecs:DescribeInstanceTypes",
        "ecs:AssignPrivateIpAddresses",
        "ecs:UnassignPrivateIpAddresses",
        "ecs:DescribeInstances",
        "ecs:ModifyNetworkInterfaceAttribute"
      ],
      "Resource": [
        "*"
      ],
      "Effect": "Allow"
    },
    {
      "Action": [
        "vpc:DescribeVSwitches"
      ],
      "Resource": [
        "*"
      ],
      "Effect": "Allow"
    }
  ]
}
```

使用`kubectl get ds terway`看到插件在每个节点上都运行起来后，表明插件安装成功。

## 验证terway的功能

### 一般VPC网络的容器

在VPC安装模式下，在容器没有做任何特殊配置时，terway会通过在节点上的podCidr中去分配地址然后配置给容器。
例如：

```yaml
[root@iZj6c86lmr8k9rk78ju0ncZ ~]# kubectl run -it --rm --image busybox busybox
If you don't see a command prompt, try pressing enter.
/ # ip link
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue qlen 1
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
3: eth0@if7: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1500 qdisc noqueue
    link/ether 46:02:02:6b:65:1e brd ff:ff:ff:ff:ff:ff
/ # ip addr show
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue qlen 1
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host
       valid_lft forever preferred_lft forever
3: eth0@if7: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1500 qdisc noqueue
    link/ether 46:02:02:6b:65:1e brd ff:ff:ff:ff:ff:ff
    inet 172.30.0.4/24 brd 172.30.0.255 scope global eth0
       valid_lft forever preferred_lft forever
    inet6 fe80::4402:2ff:fe6b:651e/64 scope link
       valid_lft forever preferred_lft forever
```

#### 使用ENI弹性网卡获得等同于底层网络的性能

在VPC安装模式下，在Pod的其中一个container的`requests`中增加对eni的需求： `aliyun/eni: 1`， 下面的例子将创建一个Nginx Pod，并分配一个ENI

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nginx
spec:
  containers:
  - name: nginx
    image: nginx
    resources:
      limits:
        aliyun/eni: 1
```

然后我们exec到这个容器中就可以看到terway创建并绑定了一个ECS的弹性网卡：

```sh
[root@iZj6c86lmr8k9rk78ju0ncZ ~]# kubectl exec -it nginx sh
# ip addr show
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN qlen 1
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host
       valid_lft forever preferred_lft forever
3: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UNKNOWN qlen 1000
    link/ether 00:16:3e:02:38:05 brd ff:ff:ff:ff:ff:ff
    inet 172.31.80.193/20 brd 172.31.95.255 scope global eth0
       valid_lft forever preferred_lft forever
    inet6 fe80::216:3eff:fe02:3805/64 scope link
       valid_lft forever preferred_lft forever
4: veth1@if8: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1500 qdisc noqueue state UP
    link/ether 1e:60:c7:cb:1e:0e brd ff:ff:ff:ff:ff:ff
    inet6 fe80::1c60:c7ff:fecb:1e0e/64 scope link
       valid_lft forever preferred_lft forever
```

#### ENI辅助IP的容器

在ENI多IP安装模式下，Terway会通过创建和分配ENI和ENI网卡上的辅助IP地址给Pod使用，Pod上的IP地址将和VPC和VSwitch的IP地址相同段，例如：

```sh
[root@iZj6c86lmr8k9rk78ju0ncZ ~]# kubectl get pod -o wide
NAME                     READY   STATUS    RESTARTS   AGE   IP              NODE                                 NOMINATED NODE
nginx-64f497f8fd-ckpdm   1/1     Running   0          4d    192.168.0.191   cn-hangzhou.i-j6c86lmr8k9rk78ju0nc   <none>
[root@iZj6c86lmr8k9rk78ju0ncZ ~]# kubectl get node -o wide cn-hangzhou.i-j6c86lmr8k9rk78ju0nc
NAME                                 STATUS   ROLES    AGE   VERSION   INTERNAL-IP     EXTERNAL-IP   OS-IMAGE                KERNEL-VERSION              CONTAINER-RUNTIME
cn-hangzhou.i-j6c86lmr8k9rk78ju0nc   Ready    <none>   12d   v1.11.5   192.168.0.154   <none>        CentOS Linux 7 (Core)   3.10.0-693.2.2.el7.x86_64   docker://17.6.2
[root@iZj6c86lmr8k9rk78ju0ncZ ~]# kubectl exec -it nginx-64f497f8fd-ckpdm bash
root@nginx-64f497f8fd-ckpdm:/# ip addr show
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
3: eth0@if106: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP group default
    link/ether 4a:60:eb:97:f4:07 brd ff:ff:ff:ff:ff:ff link-netnsid 0
    inet 192.168.0.191/32 brd 192.168.0.191 scope global eth0
       valid_lft forever preferred_lft forever
```

### 使用NetworkPolicy来限制容器间访问

Terway插件兼容标准的K8S中的NetworkPolicy来控制容器间的访问，例如：

1. 启动一个用于测试的服务

    ```sh
    [root@iZbp126bomo449eksjknkeZ ~]# kubectl run nginx --image=nginx --replicas=2
    deployment "nginx" created
    [root@iZbp126bomo449eksjknkeZ ~]# kubectl expose deployment nginx --port=80
    service "nginx" exposed
    ```

2. 验证到这个服务是可以访问的

    ```sh
    [root@iZbp126bomo449eksjknkeZ ~]# kubectl run busybox --rm -ti --image=busybox /bin/sh
    If you don't see a command prompt, try pressing enter.
    / # wget --spider --timeout=1 nginx
    Connecting to nginx (172.21.0.225:80)
    / #
    ```

3. 配置network policy规则，只允许某些标签的服务访问

    ```sh
    kind: NetworkPolicy
    apiVersion: networking.k8s.io/v1
    metadata:
      name: access-nginx
    spec:
      podSelector:
        matchLabels:
          run: nginx
      ingress:
      - from:
        - podSelector:
            matchLabels:
              access: "true"
      ```

4. 测试没有指定标签的Pod访问服务被拒绝了，而指定标签的容器能够正常的访问

    ```sh
    [root@iZbp126bomo449eksjknkeZ ~]# kubectl run busybox --rm -ti --image=busybox /bin/sh
    If you don't see a command prompt, try pressing enter.
    / # wget --spider --timeout=1 nginx
    Connecting to nginx (172.21.0.225:80)
    wget: download timed out
    / #

    [root@iZbp126bomo449eksjknkeZ ~]# kubectl run busybox --rm -ti --labels="access=true" --image=busybox /bin/sh
    If you don't see a command prompt, try pressing enter.
    / # wget --spider --timeout=1 nginx
    Connecting to nginx (172.21.0.225:80)
    / #
    ```

### 限制容器的出入带宽

Terway插件通过配置容器网卡上的限流规则来实现对容器的流量控制，避免由于单个容器的流量占满整个节点的流量，通过配置Pod上的`kubernetes.io/ingress-bandwidth`和`kubernetes.io/egress-bandwidth`分别来配置容器上的进入的和出去的带宽，例如：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nginx
  annotations:
    kubernetes.io/ingress-bandwidth: 10M
    kubernetes.io/egress-bandwidth: 10M
spec:
  nodeSelector:
    kubernetes.io/hostname: cn-shanghai.i-uf63p6s96kf4jfh8wpwn
  containers:
  - name: nginx
    image: nginx:1.7.9
    ports:
    - containerPort: 80
```
