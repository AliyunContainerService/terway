# terway网络性能测试
## 测试说明
本测试基于阿里云容器服务Kubernetes版（1.12.6-aliyun.1），Kubernetes集群使用阿里云控制台创建，测试分两部分：
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
所有测试均穿插测试超过3组取结果平均值

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

### 跨可用区网络性能测试
测试机型选用ecs.sn1ne.8xlarge，规格详情如下

实例规格 | vCPU |  内存（GiB） | 网络带宽能力（出/入）（Gbit/s） | 网络收发包能力（出/入）（万PPS） | 多队列**** | 弹性网卡（包括一块主网卡）
-|-|-|-|-|-|-
ecs.sn1ne.8xlarge | 32 | 64.0 | 6.0 | 250 | 是 | 8 | 8 |

#### 测试结果

说明：纵轴表达流量流出方向，横轴表达流量流入方向，所以组合情况一共有9种

| 吞吐量 | terway-eni | terway | host |
| ------ | ------ | ------ | ------ |
| terway-eni | 5.94 Gbits/sec | 4.21 Gbits/sec | 4.58 Gbits/sec |
| terway | 5.94 Gbits/sec | 3.61 Gbits/sec | 3.77 Gbits/sec |
| host | 5.92 Gbits/sec | 4.16 Gbits/sec | 3.71 Gbits/sec |

PPS | terway-eni | terway | host
-|-|-|-
terway-eni | 154K | 158K | 140K
terway | 136K | 118K | 136K
host | 190K | 136K | 172K |
			
延时 | terway-eni | terway | host
-|-|-|-
terway-eni | 0.65 ms | 0.723 ms | 0.858 ms
terway | 0.886 ms | 0.484 ms | 0.804 ms
host | 0.825 ms | 0.626 ms | 0.621 ms
			
#### 结果解读
- 由于增加了跨可用区的调用，使影响结果的因素变多
- host to host的吞吐量，并没有达到网卡的理论最大值，但是流入terway-eni的吞吐量基本达到了机型的带宽6 Gbit/sec，需要进一步调查宿主机间吞吐量上不去的原因
- 从容器流出的流量角度看，terway-eni模式整体明显高于terway默认网络模式，但低于宿主机网络性能
- 从流入容器的流量角度看，terway-eni的PPS结果数据优势比较明显，接近甚至超越宿主机网卡性能
