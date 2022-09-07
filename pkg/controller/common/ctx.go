package common

import (
	"context"
)

type nodeNameCtxKey struct{}

func NodeNameWithCtx(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, nodeNameCtxKey{}, name)
}

func NodeNameFromCtx(ctx context.Context) string {
	n, ok := ctx.Value(nodeNameCtxKey{}).(string)
	if !ok {
		return ""
	}
	return n
}
