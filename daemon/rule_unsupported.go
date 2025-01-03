//go:build !linux

package daemon

import (
	"context"

	"github.com/AliyunContainerService/terway/types/daemon"
)

func ruleSync(ctx context.Context, res daemon.PodResources) error {
	return nil
}
