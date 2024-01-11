package daemon

import (
	"github.com/AliyunContainerService/terway/types/daemon"
)

const (
	resDBPath = "/var/lib/cni/terway/ResRelation.db"
	resDBName = "relation"
)

type resourceManagerInitItem struct {
	item    daemon.ResourceItem
	podInfo *daemon.PodInfo
}
