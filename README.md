# Terway CNI Network Plugin
CNI plugin for alibaba cloud VPC/ENI 

[![CircleCI](https://circleci.com/gh/AliyunContainerService/terway.svg?style=svg)](https://circleci.com/gh/AliyunContainerService/terway)

English | [简体中文](./README-zh_CN.md)

## Try It:
### Install Kubernetes
Install Kubernetes via Kubeadm: https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/

After setup kubernetes cluster. Change `iptables` `Forward` default policy to `ACCEPT` on every node of cluster: `iptables -P FORWARD ACCEPT`.

Make sure cluster up and healthy by `kubectl get cs`.

### Install Terway network plugin

Replace `Network` and `AccessKey/AccessKeySecret` in [terway.yml](./terway.yml) with your cluster pod subnet and aliyun openapi credentials. Then use `kubectl apply -f terway.yml` to install Terway into kubernetes cluster.

Using `kubectl get ds terway -n kube-system` to watch plugin launching. Plugin install completed while terway daemonset available pods equal to nodes.

### Terway network plugin usage:

#### Vpc network container:

Terway will config pod's address using node's `podCidr` when pod not have any especial config. eg:

```
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

#### Using ENI network interface to get the performance equivalent to the underlying network.
Config `eni` request `aliyun/eni: 1` in one container of pod. The following example will create an Nginx Pod and assign an ENI:

```
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

```
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

#### Using network policy to limit accessible between containers.

The Terway plugin is compatible with NetworkPolicy in the standard K8S to control access between containers, for example:

1. Create and expose an deployment for test
	
	```
	[root@iZbp126bomo449eksjknkeZ ~]# kubectl run nginx --image=nginx --replicas=2
	deployment "nginx" created
	[root@iZbp126bomo449eksjknkeZ ~]# kubectl expose deployment nginx --port=80
	service "nginx" exposed
	```
2. Run busybox to test connection to deployment:
	
	```
	[root@iZbp126bomo449eksjknkeZ ~]# kubectl run busybox --rm -ti --image=busybox /bin/sh
	If you don't see a command prompt, try pressing enter.
	/ # wget --spider --timeout=1 nginx
	Connecting to nginx (172.21.0.225:80)
	/ #
	```

3. Config network policy，only allow pod access which have `run: nginx` label:
	
	```
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

	```
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

```
apiVersion: v1
kind: Pod
metadata:
  name: nginx
  annotations:
    k8s.aliyun.com/ingress-bandwidth: 1m
    k8s.aliyun.com/egress-bandwidth: 1m
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

* Go >= 1.10

```
go get github.com/AliyunContainerService/terway
cd $GOPATH/github.com/AliyunContainerService/terway/ci
# This will create a new docker image named acs/terway:<version>
./build.sh
```

## Contribute

You are welcome to make new issues and pull reuqests.

