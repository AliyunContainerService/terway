//go:build !windows

package daemon

import (
	"github.com/AliyunContainerService/terway/pkg/k8s"
)

func preStartResourceManager(daemonMode string, k8s k8s.Kubernetes) error {
	return nil
}
