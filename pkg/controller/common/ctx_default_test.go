package common

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/stretchr/testify/assert"
)

func TestWithCtx(t *testing.T) {
	ctx := context.Background()
	alloc := &v1beta1.Allocation{}

	// Test that WithCtx returns a context
	resultCtx := WithCtx(ctx, alloc)
	assert.NotNil(t, resultCtx)

	// Test that it returns the same context (current implementation)
	assert.Equal(t, ctx, resultCtx)

	// Test with nil allocation
	resultCtx2 := WithCtx(ctx, nil)
	assert.NotNil(t, resultCtx2)
}

func TestPodENIPreStartDone(t *testing.T) {
	// Test that PodENIPreStartDone is initialized
	assert.NotNil(t, PodENIPreStartDone)

	// Test that it's a channel
	select {
	case <-PodENIPreStartDone:
		// Channel is open, which is expected
	default:
		// Channel is not ready, which is also fine
	}
}
