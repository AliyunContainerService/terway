# Terway 网络插件
CNI plugin for alibaba cloud VPC/ENI 

[![CircleCI](https://circleci.com/gh/AliyunContainerService/terway.svg?style=svg)](https://circleci.com/gh/AliyunContainerService/terway)

[English](./README.md) | 简体中文

## 安装Kubernetes
使用kubeadm的指导文档 https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/ 来创建集群

安装好了之后要将iptables的policy换成ACCEPT，`iptables -P FORWARD ACCEPT`

通过`kubectl get cs`验证集群安装完成

## 安装terway插件

修改[terway.yml](./terway.yml)文件中的eni.conf的配置中的授权和网段配置，以及Network的网段配置，然后通过`kubectl apply -f terway.conf`来安装terway插件。

使用`kubectl get ds terway`看到插件在每个节点上都运行起来后，表明插件安装成功。

## 验证terway的功能

### 一般VPC网络的容器
在容器没有做任何特殊配置时，terway会通过在节点上的podCidr中去分配地址然后配置给容器。
例如：

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

### 使用ENI弹性网卡获得等同于底层网络的性能

在Pod的其中一个container的`requests`中增加对eni的需求： `aliyun/eni: 1`， 下面的例子将创建一个Nginx Pod，并分配一个ENI

```
Kind: Pod
metadata:
  labels:
    app: nginx
spec:
  containers:
  - name: nginx
    image: nginx
    ports:
    - containerPort: 80
    resources:
      limits:
        aliyun/eni: 1
```

然后我们exec到这个容器中就可以看到terway创建并绑定了一个ECS的弹性网卡：

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

### 使用NetworkPolicy来限制容器间访问

Terway插件兼容标准的K8S中的NetworkPolicy来控制容器间的访问，例如：

1. 启动一个用于测试的服务
	
	```
	[root@iZbp126bomo449eksjknkeZ ~]# kubectl run nginx --image=nginx --replicas=2
	deployment "nginx" created
	[root@iZbp126bomo449eksjknkeZ ~]# kubectl expose deployment nginx --port=80
	service "nginx" exposed
	```
2. 验证到这个服务是可以访问的
	
	```
	[root@iZbp126bomo449eksjknkeZ ~]# kubectl run busybox --rm -ti --image=busybox /bin/sh
	If you don't see a command prompt, try pressing enter.
	/ # wget --spider --timeout=1 nginx
	Connecting to nginx (172.21.0.225:80)
	/ #
	```

3. 配置network policy规则，只允许某些标签的服务访问
	
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

4. 测试没有指定标签的Pod访问服务被拒绝了，而指定标签的容器能够正常的访问

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
	

### 限制容器的出入带宽

Terway插件通过配置容器网卡上的限流规则来实现对容器的流量控制，避免由于单个容器的流量占满整个节点的流量，通过配置Pod上的`k8s.aliyun.com/ingress-bandwidth`和`k8s.aliyun.com/egress-bandwidth`分别来配置容器上的进入的和出去的带宽，例如：

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
