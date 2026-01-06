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
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/util/wait"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/backoff"
)

// Executor provides shared ENI operation primitives for both
// Pool controller (shared ENI) and ENI controller (exclusive ENI)
type Executor struct {
	aliyun aliyunClient.OpenAPI
	tracer trace.Tracer
}

// NewExecutor creates a new ENI operation executor
func NewExecutor(aliyun aliyunClient.OpenAPI, tracer trace.Tracer) *Executor {
	return &Executor{
		aliyun: aliyun,
		tracer: tracer,
	}
}

// AttachAsync initiates attach and returns immediately (non-blocking)
// Used by Pool controller for async ENI attach
func (e *Executor) AttachAsync(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	l := logf.FromContext(ctx).WithValues("eni", eniID, "instance", instanceID)

	if e.tracer != nil {
		var span trace.Span
		ctx, span = e.tracer.Start(ctx, "AttachAsync")
		defer span.End()
	}

	l.Info("initiating async ENI attach")

	err := e.aliyun.AttachNetworkInterfaceV2(ctx, &aliyunClient.AttachNetworkInterfaceOptions{
		NetworkInterfaceID:     toPtr(eniID),
		InstanceID:             toPtr(instanceID),
		TrunkNetworkInstanceID: toPtr(trunkENIID),
	})
	if err != nil {
		l.Error(err, "failed to initiate ENI attach")
		return fmt.Errorf("attach ENI failed: %w", err)
	}

	l.Info("ENI attach initiated successfully")
	return nil
}

// AttachAndWait attaches ENI and waits for it to be ready (blocking)
// Used by ENI controller for synchronous ENI attach
func (e *Executor) AttachAndWait(ctx context.Context, eniID, instanceID, trunkENIID string) (*aliyunClient.NetworkInterface, error) {
	l := logf.FromContext(ctx).WithValues("eni", eniID, "instance", instanceID)

	if e.tracer != nil {
		var span trace.Span
		ctx, span = e.tracer.Start(ctx, "AttachAndWait")
		defer span.End()
	}

	// 1. Initiate attach
	err := e.AttachAsync(ctx, eniID, instanceID, trunkENIID)
	if err != nil {
		return nil, err
	}

	// 2. Wait for ready
	bo := e.getBackoff(eniID)
	time.Sleep(bo.InitialDelay)

	eni, err := e.waitForStatus(ctx, eniID, aliyunClient.ENIStatusInUse, bo.Backoff)
	if err != nil {
		return nil, fmt.Errorf("wait ENI ready failed: %w", err)
	}

	l.Info("ENI attach completed", "status", eni.Status)
	return eni, nil
}

// CheckStatus checks the current status of an ENI
func (e *Executor) CheckStatus(ctx context.Context, eniID string) (*aliyunClient.NetworkInterface, error) {
	if e.tracer != nil {
		var span trace.Span
		ctx, span = e.tracer.Start(ctx, "CheckStatus")
		defer span.End()
	}

	enis, err := e.aliyun.DescribeNetworkInterfaceV2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
		NetworkInterfaceIDs: &[]string{eniID},
	})
	if err != nil {
		return nil, fmt.Errorf("describe ENI failed: %w", err)
	}
	if len(enis) == 0 {
		return nil, apiErr.ErrNotFound
	}
	return enis[0], nil
}

// DetachAsync initiates detach and returns immediately (non-blocking)
func (e *Executor) DetachAsync(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	l := logf.FromContext(ctx).WithValues("eni", eniID, "instance", instanceID)

	if e.tracer != nil {
		var span trace.Span
		ctx, span = e.tracer.Start(ctx, "DetachAsync")
		defer span.End()
	}

	l.Info("initiating async ENI detach")

	err := e.aliyun.DetachNetworkInterfaceV2(ctx, &aliyunClient.DetachNetworkInterfaceOptions{
		NetworkInterfaceID: toPtr(eniID),
		InstanceID:         toPtr(instanceID),
		TrunkID:            toPtr(trunkENIID),
	})
	if err != nil {
		l.Error(err, "failed to initiate ENI detach")
		return fmt.Errorf("detach ENI failed: %w", err)
	}

	l.Info("ENI detach initiated successfully")
	return nil
}

// DetachAndWait detaches ENI and waits for it to be available (blocking)
func (e *Executor) DetachAndWait(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	l := logf.FromContext(ctx).WithValues("eni", eniID, "instance", instanceID)

	if e.tracer != nil {
		var span trace.Span
		ctx, span = e.tracer.Start(ctx, "DetachAndWait")
		defer span.End()
	}

	// 1. Initiate detach
	err := e.DetachAsync(ctx, eniID, instanceID, trunkENIID)
	if err != nil {
		return err
	}

	// 2. Wait for available
	bo := e.getBackoff(eniID)
	time.Sleep(bo.InitialDelay)

	_, err = e.waitForStatus(ctx, eniID, aliyunClient.ENIStatusAvailable, bo.Backoff)
	if err != nil {
		return fmt.Errorf("wait ENI available failed: %w", err)
	}

	l.Info("ENI detach completed")
	return nil
}

// Delete deletes an ENI
func (e *Executor) Delete(ctx context.Context, eniID string) error {
	l := logf.FromContext(ctx).WithValues("eni", eniID)

	if e.tracer != nil {
		var span trace.Span
		ctx, span = e.tracer.Start(ctx, "Delete")
		defer span.End()
	}

	l.Info("deleting ENI")

	err := e.aliyun.DeleteNetworkInterfaceV2(ctx, eniID)
	if err != nil {
		l.Error(err, "failed to delete ENI")
		return fmt.Errorf("delete ENI failed: %w", err)
	}

	l.Info("ENI deleted successfully")
	return nil
}

// WaitForStatus waits for ENI to reach the specified status
func (e *Executor) WaitForStatus(ctx context.Context, eniID, status string) (*aliyunClient.NetworkInterface, error) {
	bo := e.getBackoff(eniID)
	return e.waitForStatus(ctx, eniID, status, bo.Backoff)
}

// waitForStatus internal helper to wait for ENI status with custom backoff
func (e *Executor) waitForStatus(ctx context.Context, eniID, status string, bo wait.Backoff) (*aliyunClient.NetworkInterface, error) {
	l := logf.FromContext(ctx).WithValues("eni", eniID, "targetStatus", status)

	var result *aliyunClient.NetworkInterface
	err := wait.ExponentialBackoffWithContext(ctx, bo, func(ctx context.Context) (bool, error) {
		eni, err := e.CheckStatus(ctx, eniID)
		if err != nil {
			l.V(4).Info("check status failed, retrying", "error", err)
			return false, nil // retry
		}

		l.V(4).Info("current ENI status", "status", eni.Status)

		if eni.Status == status {
			result = eni
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("timeout waiting for ENI %s to reach status %s: %w", eniID, status, err)
	}

	return result, nil
}

// getBackoff returns the appropriate backoff configuration based on ENI type
func (e *Executor) getBackoff(eniID string) backoff.ExtendedBackoff {
	if strings.HasPrefix(eniID, "leni-") {
		return backoff.Backoff(backoff.WaitLENIStatus)
	}
	if strings.HasPrefix(eniID, "hdeni-") {
		return backoff.Backoff(backoff.WaitHDENIStatus)
	}
	return backoff.Backoff(backoff.WaitENIStatus)
}

// GetTimeout returns the attach timeout based on ENI type
func (e *Executor) GetTimeout(eniID string) time.Duration {
	if strings.HasPrefix(eniID, "leni-") || strings.HasPrefix(eniID, "hdeni-") {
		return 5 * time.Minute // EFLO timeout
	}
	return 2 * time.Minute // ECS timeout
}

// GetInitialDelay returns the initial delay before checking status based on ENI type
func (e *Executor) GetInitialDelay(eniID string) time.Duration {
	bo := e.getBackoff(eniID)
	return bo.InitialDelay
}

// toPtr converts string to pointer, returns nil for empty string
func toPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
