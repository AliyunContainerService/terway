//go:build default_build

package common

import (
	"context"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
)

// WithCtx extract fields from v1beta1.Allocation and set to context.Context
func WithCtx(ctx context.Context, alloc *v1beta1.Allocation) context.Context {
	return ctx
}

func Became(ctx context.Context, aliyun register.Interface) (register.Interface, bool, error) {
	return aliyun, false, nil
}
