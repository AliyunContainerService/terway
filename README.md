# Terway CNI Network Plugin

CNI plugin for Alibaba Cloud VPC/ENI

[![Go Report Card](https://goreportcard.com/badge/github.com/AliyunContainerService/terway)](https://goreportcard.com/report/github.com/AliyunContainerService/terway)
[![codecov](https://codecov.io/gh/AliyunContainerService/terway/branch/main/graph/badge.svg)](https://codecov.io/gh/AliyunContainerService/terway)
[![Linter](https://github.com/AliyunContainerService/terway/workflows/check/badge.svg)](https://github.com/marketplace/actions/super-linter)

English | [简体中文](./README-zh_CN.md)

## Try It

### Install Kubernetes

* Prepare Aliyun ECS instance. The ECS OS we tested is `Centos 7.4/7.6`.
* Install Kubernetes via
  kubeadm: [create-cluster-kubeadm](https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/)

After setup kubernetes cluster.

* Change `iptables` `Forward` default policy to `ACCEPT` on every node of cluster: `iptables -P FORWARD ACCEPT`.
* Check the `rp_filter` in sysctl parameters, set them to "0" on every node of cluster.

Make sure cluster up and healthy by `kubectl get cs`.

### Install Terway network plugin

<br />
Terway plugin have two installation modes

* VPC Mode

    ```shell
    VPC Mode, Using `Aliyun VPC` route table to connect the pods. Can assign dedicated ENI to Pod. Install method: <br />
    Replace `Network` and `access_key/access_secret` in [terway.yml](./terway.yml) with your cluster pod subnet and aliyun openapi credentials. Then use `kubectl apply -f terway.yml` to install Terway into kubernetes cluster.
    ```

* ENI Secondary IP Mode

    ```shell
    ENI Secondary IP Mode, Using `Aliyun ENI's secondary ip` to connect the pods. This mode not limited by VPC route tables quotation. Install method: <br />
    Replace `access_key/access_secret` and `security_group/vswitches` in [terway-multiip.yml](./terway-multiip.yml) with your aliyun openapi credentials and resources id. Then use `kubectl apply -f terway-multiip.yml` to install Terway into kubernetes cluster.
    ```

Terway requires the `access_key` have following [RAM Permissions](https://ram.console.aliyun.com/)

```json
{
  "Version": "1",
  "Statement": [
    {
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

Using `kubectl get ds terway -n kube-system` to watch plugin launching. Plugin install completed while terway daemonset
available pods equal to nodes.

### Terway network plugin usage

#### Vpc network container

On VPC installation mode, Terway will config pod's address using node's `podCidr` when pod not have any special config.
eg:

```sh
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

#### Using ENI network interface to get the performance equivalent to the underlying network

On VPC installation mode, Config `eni` request `aliyun/eni: 1` in one container of pod. The following example will
create an Nginx Pod and assign an ENI:

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

#### ENI Secondary IP Pod

On ENI secondary IP installation mode, Terway will create & allocate ENI secondary IP for pod. The IP of pod will in
same IP Range:

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

#### Using network policy to limit accessible between containers

The Terway plugin is compatible with NetworkPolicy in the standard K8S to control access between containers, for
example:

1. Create and expose an deployment for test

    ```sh
    [root@iZbp126bomo449eksjknkeZ ~]# kubectl run nginx --image=nginx --replicas=2
    deployment "nginx" created
    [root@iZbp126bomo449eksjknkeZ ~]# kubectl expose deployment nginx --port=80
    service "nginx" exposed
    ```

2. Run busybox to test connection to deployment:

    ```sh
    [root@iZbp126bomo449eksjknkeZ ~]# kubectl run busybox --rm -ti --image=busybox /bin/sh
    If you don't see a command prompt, try pressing enter.
    / # wget --spider --timeout=1 nginx
    Connecting to nginx (172.21.0.225:80)
    / #
    ```

3. Config network policy，only allow pod access which have `run: nginx` label:

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

4. The Pod access service without the specified label is rejected, and the container of the specified label can be accessed normally.

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

#### Limit container in/out bandwidth

The Terway network plugin can limit the container's traffic via limit policy in pod's annotations. For example:

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

## Build Terway

Prerequisites:

* Docker >= 17.05 with multi-stage build

```sh
docker build -t acs/terway:latest .
```

## Test

unit test:

```sh
git clone https://github.com/AliyunContainerService/terway.git
docker run -i --rm \
  -v $(pwd)/terway:/go/src/github.com/AliyunContainerService/terway \
  -w /go/src/github.com/AliyunContainerService/terway \
  sunyuan3/gometalinter:v1 bash -c "go test -race ./..."
```

function test:

```sh
export KUBECONFIG=$HOME/.kube/config  # path to your kubeconfig file
cd terway/tests
go test -tags e2e -timeout 30m0s -v ./ 
  -args -trunk=true/false -policy=true/false
```

example:

```sh
go test -tags e2e -timeout 30m0s -v ./ 
  -args -trunk=false -policy=false
```

## Contribute

You are welcome to make new issues and pull requests.

## Built With

[Felix](https://github.com/projectcalico/felix): Terway's NetworkPolicy is implemented by
integrating [`ProjectCalico`](https://projectcalico.org)'s `Felix` components. `Felix` watch `NetworkPolicy`
configuration and config ACL rules on container `veth`.

[Cilium](https://github.com/cilium/cilium): In the `IPvlan` mode, `Terway`
integrate [`Cilium`](https://github.com/cilium/cilium) components to support `NetworkPolicy` and optimize the `Service`
performance. `Cilium` watch `NetworkPolicy` and `Service` configuration and inject `ebpf` program into pod's `IPvlan`
slave device.

## Community

### DingTalk

Join `DingTalk` group by `DingTalkGroup` id "35924643".
