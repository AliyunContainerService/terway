## 构建binary

```
git clone git@gitlab.alibaba-inc.com:cos/dhcp.git
cd dhcp 
env GOARCH="amd64" GOOS="linux" go build

git clone git@gitlab.alibaba-inc.com:cos/terway.git
cd terway
env GOARCH="amd64" GOOS="linux" go build
```

将`dhcp`和`terway`两个binary放在`/opt/cni/bin/`目录下，`dhcp`重命名为`eni-dhcp`


## 配置文件
增加配置文件`/etc/cni/net.d/10-eni.conf`，内容

```
{
  "cniVersion": "0.3.0",
  "name": "eni",
  "type": "terway",
  "ipam": {
    "type": "eni-dhcp"
  },
  "prefix": "eth",
  "config-dir": "/var/lib/eni",
  "service-cidr": "172.19.0.0/20"
}
```

# TODO

- 预先获取DHCP信息、缓存DHCP结果。减少ADD网卡需要的时间

