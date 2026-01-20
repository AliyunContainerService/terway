/*
Copyright 2025 Terway Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/trace/noop"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
)

func TestExecutor_GetTimeout(t *testing.T) {
	exec := NewExecutor(nil, nil)

	// ECS ENI
	timeout := exec.GetTimeout("eni-12345")
	assert.Equal(t, 2*time.Minute, timeout)

	// LENI (EFLO)
	timeout = exec.GetTimeout("leni-12345")
	assert.Equal(t, 5*time.Minute, timeout)

	// HDENI (EFLO)
	timeout = exec.GetTimeout("hdeni-12345")
	assert.Equal(t, 5*time.Minute, timeout)
}

func TestExecutor_GetInitialDelay(t *testing.T) {
	exec := NewExecutor(nil, nil)

	// ECS ENI should have smaller initial delay
	ecsDelay := exec.GetInitialDelay("eni-12345")

	// LENI should have larger initial delay
	leniDelay := exec.GetInitialDelay("leni-12345")

	// EFLO should have larger delay than ECS
	assert.Less(t, ecsDelay, leniDelay)
}

func Test_toPtr(t *testing.T) {
	// Empty string should return nil
	result := toPtr("")
	assert.Nil(t, result)

	// Non-empty string should return pointer
	result = toPtr("test")
	assert.NotNil(t, result)
	assert.Equal(t, "test", *result)
}

// ==============================================================================
// Executor Tests using mocks.OpenAPI
// ==============================================================================

func TestExecutor_AttachAsync_Success(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil)

	err := exec.AttachAsync(context.Background(), "eni-123", "i-instance", "eni-trunk")
	assert.NoError(t, err)
}

func TestExecutor_AttachAsync_Error(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return(errors.New("attach failed"))

	err := exec.AttachAsync(context.Background(), "eni-123", "i-instance", "eni-trunk")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "attach ENI failed")
}

func TestExecutor_DetachAsync_Success(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("DetachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil)

	err := exec.DetachAsync(context.Background(), "eni-123", "i-instance", "eni-trunk")
	assert.NoError(t, err)
}

func TestExecutor_DetachAsync_Error(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("DetachNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return(errors.New("detach failed"))

	err := exec.DetachAsync(context.Background(), "eni-123", "i-instance", "eni-trunk")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "detach ENI failed")
}

func TestExecutor_Delete_Success(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("DeleteNetworkInterfaceV2", mock.Anything, "eni-123").Return(nil)

	err := exec.Delete(context.Background(), "eni-123")
	assert.NoError(t, err)
}

func TestExecutor_Delete_Error(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("DeleteNetworkInterfaceV2", mock.Anything, "eni-123").
		Return(errors.New("delete failed"))

	err := exec.Delete(context.Background(), "eni-123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete ENI failed")
}

func TestExecutor_CheckStatus_Success(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	expectedENI := &aliyunClient.NetworkInterface{
		NetworkInterfaceID: "eni-123",
		Status:             aliyunClient.ENIStatusInUse,
	}
	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return([]*aliyunClient.NetworkInterface{expectedENI}, nil)

	eni, err := exec.CheckStatus(context.Background(), "eni-123")
	assert.NoError(t, err)
	assert.NotNil(t, eni)
	assert.Equal(t, "eni-123", eni.NetworkInterfaceID)
	assert.Equal(t, aliyunClient.ENIStatusInUse, eni.Status)
}

func TestExecutor_CheckStatus_NotFound(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return([]*aliyunClient.NetworkInterface{}, nil)

	eni, err := exec.CheckStatus(context.Background(), "eni-123")
	assert.Error(t, err)
	assert.Equal(t, apiErr.ErrNotFound, err)
	assert.Nil(t, eni)
}

func TestExecutor_CheckStatus_Error(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return(nil, errors.New("describe failed"))

	eni, err := exec.CheckStatus(context.Background(), "eni-123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "describe ENI failed")
	assert.Nil(t, eni)
}

func TestExecutor_WaitForStatus_Success(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	expectedENI := &aliyunClient.NetworkInterface{
		NetworkInterfaceID: "eni-123",
		Status:             aliyunClient.ENIStatusInUse,
	}

	// First call returns Attaching status
	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return([]*aliyunClient.NetworkInterface{expectedENI}, nil).Once()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eni, err := exec.WaitForStatus(ctx, "eni-123", aliyunClient.ENIStatusInUse)
	assert.NoError(t, err)
	assert.NotNil(t, eni)
	assert.Equal(t, aliyunClient.ENIStatusInUse, eni.Status)
}

func TestExecutor_WaitForStatus_Timeout(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	// Always return Attaching status to cause timeout
	attachingENI := &aliyunClient.NetworkInterface{
		NetworkInterfaceID: "eni-123",
		Status:             aliyunClient.ENIStatusAttaching,
	}
	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return([]*aliyunClient.NetworkInterface{attachingENI}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	eni, err := exec.WaitForStatus(ctx, "eni-123", aliyunClient.ENIStatusInUse)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout waiting for ENI")
	assert.Nil(t, eni)
}

func TestExecutor_AttachAndWait_AttachError(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return(errors.New("attach failed"))

	eni, err := exec.AttachAndWait(context.Background(), "eni-123", "i-instance", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "attach ENI failed")
	assert.Nil(t, eni)
}

func TestExecutor_DetachAndWait_DetachError(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	mockAPI.On("DetachNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return(errors.New("detach failed"))

	err := exec.DetachAndWait(context.Background(), "eni-123", "i-instance", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "detach ENI failed")
}

func TestExecutor_DetachAndWait_Success(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	// Mock successful detach
	mockAPI.On("DetachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil)

	// Mock describe returning Available status immediately
	availableENI := &aliyunClient.NetworkInterface{
		NetworkInterfaceID: "eni-123",
		Status:             aliyunClient.ENIStatusAvailable,
	}
	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return([]*aliyunClient.NetworkInterface{availableENI}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := exec.DetachAndWait(ctx, "eni-123", "i-instance", "")
	assert.NoError(t, err)
}

func TestExecutor_AttachAndWait_Success(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tp := noop.NewTracerProvider()
	exec := NewExecutor(mockAPI, tp.Tracer("none"))

	// Mock successful attach
	mockAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil)

	// Mock describe returning InUse status immediately
	inUseENI := &aliyunClient.NetworkInterface{
		NetworkInterfaceID: "eni-123",
		Status:             aliyunClient.ENIStatusInUse,
	}
	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).
		Return([]*aliyunClient.NetworkInterface{inUseENI}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eni, err := exec.AttachAndWait(ctx, "eni-123", "i-instance", "")
	assert.NoError(t, err)
	assert.NotNil(t, eni)
	assert.Equal(t, aliyunClient.ENIStatusInUse, eni.Status)
}

func TestExecutor_getBackoff(t *testing.T) {
	exec := NewExecutor(nil, nil)

	// ECS ENI
	bo := exec.getBackoff("eni-12345")
	assert.NotNil(t, bo)

	// LENI
	bo = exec.getBackoff("leni-12345")
	assert.NotNil(t, bo)

	// HDENI
	bo = exec.getBackoff("hdeni-12345")
	assert.NotNil(t, bo)
}

func TestNewExecutor(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	exec := NewExecutor(mockAPI, nil)

	assert.NotNil(t, exec)
	assert.NotNil(t, exec.aliyun)
	assert.Nil(t, exec.tracer)
}
