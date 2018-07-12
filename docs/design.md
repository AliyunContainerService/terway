使用方式
=========

terway插件以完整yaml文件的方式提供给用户，用户只要执行`kubectl apply -f terway.yml`就可以完成安装，接下来能够部署使用ENI网卡的Pod。一个完整的`terway.yml`文件示例如下：

```
kind: ConfigMap
apiVersion: v1
metadata:
  name: eni-config
  namespace: kube-system
data:
  eni_conf: |
    version: "1"
    config: 
      service_cidr: 10.1.0.0/16  //集群service的段
      prefix: "eth"  //可选，默认eth。如果ecs上已经存在eni网卡，指定已有网卡的前缀
      max_pool_size: 5  //可选，默认5。eni插件维护一个interface pool，提升分配eni的性能，同时避免频繁的调用阿里云api。可以指定池的大小。超过池子的最大值开始释放网卡
      min_pool_size: 2 //可选，默认2。地域池子的最小值异步补充网卡
      vswitches:   //可选，默认使用当前ecs的交换机。网卡必须属于同一个可用区下的交换机，当集群跨可用区时，每个可用区指定一个交换机。
        cn-hangzhou-a: switch-a 
        cn-hangzhou-b: switch-b
      security-group: security-group-001 //可选，eni网卡所属的安全组，可以不指定，默认使用eth0(${prefix}0)相同的安全组

---

apiVersion: extensions/v1beta1
kind: DaemonSet
metadata:
  name: terway
  namespace: kube-system
spec:
  template:
    metadata:
      labels:
        app: terway
    spec:
      hostNetwork: true
      containers:
      - name: eni
        image: registry.cn-hangzhou.aliyuncs.com/acs/eni:latest
        command: ["/eni"]
        imagePullPolicy: Always
        securityContext:
          privileged: true
        env:
          - name: AK_SECRET
            valueFrom:
              secretKeyRef: 
                name: aliyun_eni_ak
                key: ak_secret
          - name: AK_ID
            valueFrom:
              secretKeyRef: 
                name: aliyun_eni_ak
                key: ak_id
        volumeMounts:
        - mountPath: /var/lib/eni
          name: config-dir
        - name: config-volume
          path: /etc/eni
      - name: install-cni
        - command:
			- /bin/sh
			- -c
			- set -e -x; cp -f /etc/cni-conf.json /etc/cni/net.d/10-eni.conf;
			  while true; do sleep 3600; done
		image: registry.cn-hangzhou.aliyuncs.com/acs/eni:lastest
		imagePullPolicy: IfNotPresent
      volumes:
      - name: cni
        hostPath:
          path: /etc/cni/net.d
          type: ""
      - name: config-dir
        hostPath:
          path: /var/lib/eni
          type: "Directory"
      - name: config-volume
        configMap: 
          name: eni-config
          items:
		  - key: eni_conf
            path: eni.conf
```

用户需要关注的选项包括这样几个：


- aliyun_eni_ak: 
    Kubernetes secret，可选。用户可以创建一个具有访问eni api权限的子账号，并将其ak访问k8s secret中传给eni plugin。如果没有传入，默认使用instance meta中的sts token。
- service_cidr: 
    必选，集群service的段, 比如10.1.0.0/16，没有正确设置的情况下，可能导致eni pod不能正常访问service。暂时没有办法从Kubernetes api取到。
- prefix: "eth"  
    可选，如果集群ecs上已经存在的eni网卡，此eni插件也可以管理这些网卡。此处需要设置网卡名称的前缀，默认是eth
- max_pool_size: 5  
    可选，eni插件维护一个interface pool，提升分配eni的性能，同时避免频繁的调用阿里云api。可以指定池的大小。超过池子的最大值开始释放网卡。默认5个
- min_pool_size: 2
    池子的最小值，默认0个（即不补充）。地域池子的最小值异步补充网卡
- vswitches:
    可选。创建eni网卡必须关联同可用区下的交换机。由于一个集群可以跨几个可用区（默认3个），不用可用区必须使用不同的交换机。为了简化使用，如果没有指定可用区对应的交换机，默认使用和ECS相同的交换机。
- security-group:
    可选。创建eni网卡必须指定一个安全组。默认使用当前ecs ${prefix}0网卡相同的安全组。

Pool的实现
==========

Pool是ENI Plugin的核心组件，负责ENI网卡资源的管理，池子里的网卡不足时，调用ECS API创建新网卡，挂载到本机。池子里的网卡太多时，释放掉多余的网卡，避免一台ECS占用太多的空闲网卡。

调用ENI API时，需要注意：

1. 限制到ENI的并发请求，一般不超过8个并发。
2. 判断ENI API的响应结果，如果响应结果为“已经达到ECS最大ENI数量”，此时应该将本机置为“不能继续创建ENI”的状态，对后续创建ENI的请求直接拒绝，直到有网卡释放。Plugin后台低频次重试创建ENI（20-30分钟一次）

结合VPC网络
===========

纯ENI网络受限于每台ECS上ENI网卡数量，很多时候会显得不实用。一种方式是在ENI插件中支持VPC网络(现在的容器VPC网络，基于vrouter转发的方式)，用户可以选择一个Pod使用ENI网络还是VPC网络，如果选择ENI网络，Pod独占ENI网卡，否则Pod只分配一个VPC内能用的IP地址。
优先级P2

如何来与调度结合以保证需要eni网卡的机器能调度到正确的节点上
===========

1. 节点能调度的能力：
采用device plugin的方式去统计本节点上支持调度的网卡数量，支持两种统计模式：

* 只统计已绑定网卡，不使用热插拔时
* 通过节点的规格返回最大支持绑定的网卡量，在热插拔时使用

2. Pod如何使用弹性网卡

	1. Pod中通过声明request: alicloud/eni: 1的方式来声明需要弹性网卡
	2. 容器中声明了request: alicloud/eni 的Pod不需要再添加annotation来支持弹性网卡， 一个pod中只需要一个容器声明
	3. 不计划再兼容之前annotation的声明使用eni网卡的格式

3. device plugin启动流程：

	1. 启动时分配节点上可以分配网卡数量的ID，格式为 nodename-$index
	2. 在kubelet Allocate的时候不需要返回任何mount和device信息
