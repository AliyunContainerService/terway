module github.com/AliyunContainerService/terway

go 1.14

replace (
        github.com/docker/docker v1.13.1 => github.com/docker/engine v0.0.0-20191113042239-ea84732a7725
        github.com/googleapis/gnostic => github.com/googleapis/gnostic v0.0.0-20170729233727-0c5108395e2d
        k8s.io/api => k8s.io/api v0.0.0-20190918155943-95b840bb6a1f
        k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
        k8s.io/client-go => k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90
)

require (
        github.com/Microsoft/go-winio v0.4.15 // indirect
        github.com/alexflint/go-filemutex v1.1.0 // indirect
        github.com/boltdb/bolt v1.3.1
        github.com/containerd/containerd v1.4.3 // indirect
        github.com/containernetworking/cni v0.6.0
        github.com/containernetworking/plugins v0.7.5
        github.com/coreos/go-iptables v0.4.5 // indirect
        github.com/denverdino/aliyungo v0.0.0-20201203064902-695c04c361ee
        github.com/docker/distribution v2.7.1+incompatible // indirect
        github.com/docker/docker v1.13.1
        github.com/docker/go-connections v0.4.0 // indirect
        github.com/docker/go-units v0.4.0 // indirect
        github.com/evanphx/json-patch v4.9.0+incompatible
        github.com/golang/protobuf v1.4.3
        github.com/googleapis/gnostic v0.4.1 // indirect
        github.com/imdario/mergo v0.3.11 // indirect
        github.com/magiconair/properties v1.8.4 // indirect
        github.com/morikuni/aec v1.0.0 // indirect
        github.com/opencontainers/go-digest v1.0.0 // indirect
        github.com/opencontainers/image-spec v1.0.1 // indirect
        github.com/pkg/errors v0.9.1
        github.com/prometheus/client_golang v1.8.0
        github.com/sirupsen/logrus v1.7.0
        github.com/stretchr/testify v1.6.1
        github.com/vishvananda/netlink v1.1.0
        github.com/vishvananda/netns v0.0.0-20200728191858-db3c7e526aae
        golang.org/x/net v0.0.0-20201202161906-c7110b5ffcbb
        golang.org/x/sys v0.0.0-20201204225414-ed752295db88
        google.golang.org/grpc v1.34.0
        gopkg.in/yaml.v2 v2.4.0
        k8s.io/api v0.19.4
        k8s.io/apimachinery v0.19.4
        k8s.io/client-go v0.19.4
        k8s.io/klog v1.0.0 // indirect
        k8s.io/kubelet v0.19.4
        k8s.io/utils v0.0.0-20201110183641-67b214c5f920 // indirect
)