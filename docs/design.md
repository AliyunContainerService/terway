# Terway插件

Kubernetes有着种类丰富，数量繁多的网络插件，几乎任何场景，都能找到能用的网络插件。对于有追求的程序员来说，只是能用可不行，必须要选择最好的。很多时候，适合的才是最好的。
Terway是容器服务团队推出的针对阿里云VPC网络的CNI插件，稳定、高性能，支持Kubernetes network policy、流控等高级特性。

![terway](images/terway.png)
Terway的结构还是比较简单的，首先是标准的CNI接口，支持Kubernetes。内置了Network Policy和Traffic Control，同样是在Kubernetes场景下使用，支持Network Policies，可以实现Pod之间的访问隔离。通过在Pod上声明annotation kubernetes.io/ingress-bandwidth和kubernetes.io/egress-bandwidth可以限制Pod的入网和出网带宽。

## 架构设计与考虑

组件列表，时序图交互

### 资源管理和分配

资源状态，资源的池化与缓存，pod关联关系

### 不同资源类型的通信方案

#### vpc

#### ENI

#### ENI多IP

##### veth策略路由

##### ipvlan l3s

### Network Policy

### Pod流量控制
