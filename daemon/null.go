package daemon

import (
	"fmt"
	"github.com/AliyunContainerService/terway/types"
)

type nullResourceManager struct {
}

func (nullResourceManager) Allocate(context *networkContext, prefer string) (types.NetworkResource, error) {
	return nil, fmt.Errorf("unsupported")
}

func (nullResourceManager) Release(context *networkContext, resID string) error {
	return fmt.Errorf("unsupported")
}

func (nullResourceManager) GarbageCollection(inUseResList []string, expireResList []string) error {
	return fmt.Errorf("unsupported")
}
