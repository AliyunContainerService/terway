//go:build default_build

package common

import (
	"context"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SetPodAnnotation(ctx context.Context, m client.Client, podENI *v1beta1.PodENI) error {
	return nil
}
