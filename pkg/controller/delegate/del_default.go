//go:build default_build

package delegate

import (
	"context"

	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/node"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func (d *Delegate) DeleteNetworkInterface(ctx context.Context, eniID string) error {
	nodeName := common.NodeNameFromCtx(ctx)
	l := log.FromContext(ctx)
	l.Info("DeleteNetworkInterface", "nodeName", nodeName)

	nodeClient, err := node.GetPoolManager(nodeName)
	if nodeName == "" || err != nil {
		realClient, _, err := common.Became(ctx, d.defaultClient)
		if err != nil {
			return err
		}
		return realClient.DeleteNetworkInterface(ctx, eniID)
	}

	c, err := nodeClient.GetClient()
	if err != nil {
		return err
	}
	return c.DeleteNetworkInterface(ctx, eniID)
}
