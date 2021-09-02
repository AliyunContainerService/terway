package daemon

import (
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/types"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

type networkContext struct {
	context.Context
	resources  []types.ResourceItem
	pod        *types.PodInfo
	k8sService Kubernetes
}

func (networkContext *networkContext) Log() *logrus.Entry {
	return logger.DefaultLogger.
		WithField("podName", networkContext.pod.Name).
		WithField("podNs", networkContext.pod.Namespace).
		WithField("resources", networkContext.resources)
}
