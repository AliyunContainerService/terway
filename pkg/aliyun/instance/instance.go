package instance

import (
	"sync"

	"k8s.io/klog/v2"
)

var defaultIns *Instance
var once sync.Once

type PopulateFunc func() *Instance

var populate PopulateFunc

type Instance struct {
	RegionID   string
	ZoneID     string
	VPCID      string
	VSwitchID  string
	PrimaryMAC string

	InstanceID   string
	InstanceType string
}

func init() {
	populate = DefaultPopulate
}

func SetPopulateFunc(fn PopulateFunc) {
	populate = fn
}

func GetInstanceMeta() *Instance {
	once.Do(func() {
		defaultIns = populate()

		klog.Infof("load instance metadata %#v", defaultIns)
	})

	return defaultIns
}
