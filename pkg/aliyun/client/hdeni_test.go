package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEFLOService_CreateHDENI(t *testing.T) {
	service := &EFLOService{}
	ctx := context.Background()

	ni, err := service.CreateHDENI(ctx)

	assert.Error(t, err)
	assert.Nil(t, ni)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestEFLOService_DeleteHDENI(t *testing.T) {
	service := &EFLOService{}
	ctx := context.Background()
	eniID := "eni-test"

	err := service.DeleteHDENI(ctx, eniID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestEFLOService_DescribeHDENI(t *testing.T) {
	service := &EFLOService{}
	ctx := context.Background()

	nis, err := service.DescribeHDENI(ctx)

	assert.Error(t, err)
	assert.Nil(t, nis)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestEFLOService_AttachHDENI(t *testing.T) {
	service := &EFLOService{}
	ctx := context.Background()

	err := service.AttachHDENI(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestEFLOService_DetachHDENI(t *testing.T) {
	service := &EFLOService{}
	ctx := context.Background()

	err := service.DetachHDENI(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}