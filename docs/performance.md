# terway网络性能测试
## 测试说明
本测试基于阿里云容器服务Kubernetes版，Kubernetes集群使用阿里云控制台创建，测试分两部分：
- 同可用区网络性能测试
- 跨可用区网络性能测试
本测试的所有网络流量均为跨节点通信（容器分布在不同的宿主机节点上）
### 关键指标
- 吞吐量（Gbit/sec）
- PPS（Packet Per Second）
- 延时（ms）
### 测试方法
吞吐量，PPS测试使用iperf3版本如下：
```
iperf 3.6 (cJSON 1.5.2)
Linux iperf3-terway-57b5fd565-bwc28 3.10.0-957.5.1.el7.x86_64 #1 SMP Fri Feb 1 14:54:57 UTC 2019 x86_64
Optional features available: CPU affinity setting, TCP congestion algorithm setting, sendfile / zerocopy, socket pacing
```
测试机命令：
```
# 启动服务器模式，暴露在端口16000，每1秒输出一次统计数据
iperf3 -s -i 1 -p 16000
```
陪练机命令：
```
# 测试吞吐量
# 客户端模式，默认使用tcp通信，目标机为172.16.13.218，持续时间45，-P参数指定网卡队列数为4（跟测试的机型有关），目标端口16000
iperf3 -c 172.16.13.218 -t 45 -P 4 -p 16000
# 测试PPS
# 客户端模式，使用udp发包，包大小为16字节，持续时间45秒，-A指定CPU亲和性绑定到第0个CPU
iperf3 -u -l 16 -b 100m -t 45 -c 172.16.13.218 -i 1 -p 16000 -A 0
# 测试延迟
# ping目标机30次
ping -c 30 172.16.13.218
```
## 测试结果
### 同可用区网络性能测试
#### 机型说明
测试机型选用ecs.sn1ne.2xlarge，规格详情如下

实例规格 | vCPU |  内存（GiB） | 网络带宽能力（出/入）（Gbit/s） | 网络收发包能力（出/入）（万PPS） | 多队列**** | 弹性网卡（包括一块主网卡）
-|-|-|-|-|-|-
ecs.sn1ne.2xlarge | 8 | 16.0 | 2.0 | 100 | 是 | 4 | 4 |

#### 测试结果

说明：纵轴表达流量流出方向，横轴表达流量流入方向，所以组合情况一共有9种

| 吞吐量 | terway-eni | terway | host |
| ------ | ------ | ------ | ------ |
| terway-eni | 2.06 Gbits/sec | 2.06 Gbits/sec | 2.07 Gbits/sec |
| terway | 2.06 Gbits/sec | 2.06 Gbits/sec | 2.06 Gbits/sec |
| host | 2.06 Gbits/sec | 2.07 Gbits/sec | 2.06 Gbits/sec |

PPS | terway-eni | terway | host
-|-|-|-
terway-eni | 193K | 170K | 130K
terway | 127K | 100K | 128.4K
host | 163K | 130k | 132K |
			
延时 | terway-eni | terway | host
-|-|-|-
terway-eni | 0.205 ms | 0.250 ms | 0.211 ms
terway | 0.258 ms | 0.287 ms | 0.282 ms
host | 0.170 ms | 0.219 ms | 0.208 ms

#### 结果解读
- 各种模式下均可将网卡带宽打满，从吞吐量上看结果无明显区别
- 从流量流入容器角度看数据，流向terway-eni模式在各项指标均接近甚至超过流向宿主机的性能
- 从流量流出容器角度看数据，从terway-eni模式性能接近但略低于宿主机流量流出性能，但明显高于terway默认网络
