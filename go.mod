module github.com/AliyunContainerService/terway

go 1.15

require (
	github.com/aliyun/alibaba-cloud-sdk-go v1.61.1099
	github.com/boltdb/bolt v1.3.1
	github.com/containerd/containerd v1.4.3 // indirect
	github.com/containernetworking/cni v0.8.0
	github.com/containernetworking/plugins v0.8.7
	github.com/denverdino/aliyungo v0.0.0-20201215054313-f635de23c5e0
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v20.10.1+incompatible
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/evanphx/json-patch v4.9.0+incompatible
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/moby/term v0.0.0-20201216013528-df9cb8a40635 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.1 // indirect
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.7.1
	github.com/sirupsen/logrus v1.7.0
	github.com/stretchr/testify v1.7.0
	github.com/vishvananda/netlink v1.1.1-0.20201206203632-88079d98e65d
	github.com/vishvananda/netns v0.0.0-20200728191858-db3c7e526aae
	golang.org/x/net v0.0.0-20201209123823-ac852fbbde11
	golang.org/x/sys v0.0.0-20201221093633-bc327ba9c2f0
	google.golang.org/grpc v1.34.0
	google.golang.org/protobuf v1.25.0
	gopkg.in/yaml.v2 v2.4.0
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
	gotest.tools/v3 v3.0.3 // indirect
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/client-go v0.20.0
	k8s.io/kubelet v0.20.0
)

replace (
	k8s.io/api => k8s.io/api v0.16.9
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.15
	k8s.io/client-go => k8s.io/client-go v0.16.9
	k8s.io/kubelet => k8s.io/kubelet v0.17.15
)
