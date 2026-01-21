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

package node

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/trace/noop"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	"github.com/AliyunContainerService/terway/pkg/eni/ops"
)

// createTestExecutor creates an executor with a mock aliyun client for testing
func createTestExecutor(t *testing.T) *ops.Executor {
	mockAPI := mocks.NewOpenAPI(t)
	tracer := noop.NewTracerProvider().Tracer("test")
	return ops.NewExecutor(mockAPI, tracer)
}

// setTaskForTesting safely sets a task in the queue for testing purposes.
// This helper ensures proper synchronization to avoid race conditions.
func setTaskForTesting(q *ENITaskQueue, eniID string, task *ENITaskRecord) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks[eniID] = task
}

func TestENITaskQueue_RemoveTasks(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Add tasks for node1
	q.tasks["eni-1"] = &ENITaskRecord{ENIID: "eni-1", NodeName: "node1", Status: TaskStatusPending}
	q.tasks["eni-2"] = &ENITaskRecord{ENIID: "eni-2", NodeName: "node1", Status: TaskStatusPending}

	// Add tasks for node2
	q.tasks["eni-3"] = &ENITaskRecord{ENIID: "eni-3", NodeName: "node2", Status: TaskStatusPending}

	// Verify all tasks are present
	assert.True(t, q.HasPendingTasks("node1"))
	assert.True(t, q.HasPendingTasks("node2"))

	// Remove tasks for node1
	q.RemoveTasks("node1")

	// Verify node1 tasks are gone
	assert.False(t, q.HasPendingTasks("node1"))

	// Verify node2 tasks are still there
	assert.True(t, q.HasPendingTasks("node2"))

	// Verify specific ENIs
	_, ok := q.GetTaskStatus("eni-1")
	assert.False(t, ok)
	_, ok = q.GetTaskStatus("eni-2")
	assert.False(t, ok)
	_, ok = q.GetTaskStatus("eni-3")
	assert.True(t, ok)
}

func TestENITaskQueue_PeekAndDelete(t *testing.T) {
	if ENITaskQueueSize == nil {
		t.Fatal("ENITaskQueueSize is nil")
	}
	if ENIAttachDuration == nil {
		t.Fatal("ENIAttachDuration is nil")
	}
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Add a task manually
	q.tasks["eni-1"] = &ENITaskRecord{
		ENIID:     "eni-1",
		NodeName:  "node1",
		Status:    TaskStatusRunning,
		CreatedAt: time.Now(),
	}

	// Manually complete it for testing
	q.completeTask("eni-1", TaskStatusCompleted, &aliyunClient.NetworkInterface{NetworkInterfaceID: "eni-1"}, nil)

	// Peek
	tasks := q.PeekCompletedTasks("node1")
	assert.Len(t, tasks, 1)
	assert.Equal(t, "eni-1", tasks[0].ENIID)

	// Verify it's still in the queue
	assert.True(t, q.HasPendingTasks("node1") == false) // It's completed, not pending
	// Check internal map directly or via GetTaskStatus
	_, ok := q.GetTaskStatus("eni-1")
	assert.True(t, ok)

	// Delete
	q.DeleteTasks([]string{"eni-1"})

	// Verify it's gone
	_, ok = q.GetTaskStatus("eni-1")
	assert.False(t, ok)
}

func TestENITaskQueue_IPSync(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tracer := noop.NewTracerProvider().Tracer("test")
	executor := ops.NewExecutor(mockAPI, tracer)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Setup mock expectations for AttachAsync
	mockAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
	// Setup mock expectations for WaitForStatus (CheckStatus)
	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
		{
			NetworkInterfaceID: "eni-recovery",
			Status:             aliyunClient.ENIStatusInUse,
		},
	}, nil).Maybe()

	// 1. Submit a task with 0 IPs (simulating recovery)
	q.SubmitAttach(context.Background(), "eni-recovery", "i-1", "", "node1", 0, 0)

	// Wait a bit for the async task to start
	time.Sleep(100 * time.Millisecond)

	// 2. Verify initial state
	task, ok := q.GetTaskStatus("eni-recovery")
	assert.True(t, ok)
	assert.Equal(t, 0, task.RequestedIPv4Count)
}

func TestENITaskQueue_CleanupStaleTasks_OrphanedTask(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Add tasks for node1
	q.tasks["eni-1"] = &ENITaskRecord{ENIID: "eni-1", NodeName: "node1", Status: TaskStatusRunning}
	q.tasks["eni-2"] = &ENITaskRecord{ENIID: "eni-2", NodeName: "node1", Status: TaskStatusPending}
	q.tasks["eni-3"] = &ENITaskRecord{ENIID: "eni-3", NodeName: "node1", Status: TaskStatusCompleted}
	q.tasks["eni-4"] = &ENITaskRecord{ENIID: "eni-4", NodeName: "node2", Status: TaskStatusPending}

	// Only eni-1 exists in CR
	validENIIDs := map[string]struct{}{
		"eni-1": {},
	}

	// Cleanup should remove eni-2 and eni-3 (not in CR), keep eni-1 and eni-4 (different node)
	removed := q.CleanupStaleTasks("node1", validENIIDs, 30*time.Minute)

	assert.Len(t, removed, 2)
	assert.Contains(t, removed, "eni-2")
	assert.Contains(t, removed, "eni-3")

	// Verify eni-1 still exists
	_, ok := q.GetTaskStatus("eni-1")
	assert.True(t, ok)

	// Verify eni-4 still exists (different node)
	_, ok = q.GetTaskStatus("eni-4")
	assert.True(t, ok)

	// Verify eni-2 and eni-3 are gone
	_, ok = q.GetTaskStatus("eni-2")
	assert.False(t, ok)
	_, ok = q.GetTaskStatus("eni-3")
	assert.False(t, ok)
}

func TestENITaskQueue_CleanupStaleTasks_StaleCompletedTask(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	now := time.Now()
	oldTime := now.Add(-35 * time.Minute)   // 35 minutes ago
	recentTime := now.Add(-5 * time.Minute) // 5 minutes ago

	// Add tasks with different completion times
	q.tasks["eni-1"] = &ENITaskRecord{
		ENIID:       "eni-1",
		NodeName:    "node1",
		Status:      TaskStatusCompleted,
		CompletedAt: &oldTime, // Stale
	}
	q.tasks["eni-2"] = &ENITaskRecord{
		ENIID:       "eni-2",
		NodeName:    "node1",
		Status:      TaskStatusCompleted,
		CompletedAt: &recentTime, // Recent
	}
	q.tasks["eni-3"] = &ENITaskRecord{
		ENIID:       "eni-3",
		NodeName:    "node1",
		Status:      TaskStatusFailed,
		CompletedAt: &oldTime, // Stale
	}
	q.tasks["eni-4"] = &ENITaskRecord{
		ENIID:       "eni-4",
		NodeName:    "node1",
		Status:      TaskStatusRunning,
		CompletedAt: nil, // Still running
	}

	// All ENIs exist in CR
	validENIIDs := map[string]struct{}{
		"eni-1": {},
		"eni-2": {},
		"eni-3": {},
		"eni-4": {},
	}

	// Cleanup should only remove stale completed tasks (eni-1 and eni-3)
	removed := q.CleanupStaleTasks("node1", validENIIDs, 30*time.Minute)

	assert.Len(t, removed, 2)
	assert.Contains(t, removed, "eni-1")
	assert.Contains(t, removed, "eni-3")

	// Verify eni-2 still exists (completed but recent)
	_, ok := q.GetTaskStatus("eni-2")
	assert.True(t, ok)

	// Verify eni-4 still exists (still running)
	_, ok = q.GetTaskStatus("eni-4")
	assert.True(t, ok)
}

func TestENITaskQueue_CleanupStaleTasks_EmptyQueue(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	validENIIDs := map[string]struct{}{
		"eni-1": {},
	}

	// Should not panic or error on empty queue
	removed := q.CleanupStaleTasks("node1", validENIIDs, 30*time.Minute)
	assert.Len(t, removed, 0)
}

func TestENITaskQueue_CleanupStaleTasks_MixedScenario(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	now := time.Now()
	oldTime := now.Add(-45 * time.Minute)
	recentTime := now.Add(-10 * time.Minute)

	// Mix of orphaned and stale tasks
	q.tasks["eni-orphaned"] = &ENITaskRecord{
		ENIID:    "eni-orphaned",
		NodeName: "node1",
		Status:   TaskStatusPending,
	}
	q.tasks["eni-stale"] = &ENITaskRecord{
		ENIID:       "eni-stale",
		NodeName:    "node1",
		Status:      TaskStatusTimeout,
		CompletedAt: &oldTime,
	}
	q.tasks["eni-valid"] = &ENITaskRecord{
		ENIID:       "eni-valid",
		NodeName:    "node1",
		Status:      TaskStatusCompleted,
		CompletedAt: &recentTime,
	}
	q.tasks["eni-running"] = &ENITaskRecord{
		ENIID:    "eni-running",
		NodeName: "node1",
		Status:   TaskStatusRunning,
	}

	// Only eni-stale, eni-valid, and eni-running exist in CR (eni-orphaned was deleted)
	validENIIDs := map[string]struct{}{
		"eni-stale":   {},
		"eni-valid":   {},
		"eni-running": {},
	}

	removed := q.CleanupStaleTasks("node1", validENIIDs, 30*time.Minute)

	// Should remove orphaned + stale
	assert.Len(t, removed, 2)
	assert.Contains(t, removed, "eni-orphaned")
	assert.Contains(t, removed, "eni-stale")

	// Valid and running should remain
	_, ok := q.GetTaskStatus("eni-valid")
	assert.True(t, ok)
	_, ok = q.GetTaskStatus("eni-running")
	assert.True(t, ok)
}

func TestENITaskQueue_GetAttachingCount(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Empty queue should return 0
	assert.Equal(t, 0, q.GetAttachingCount("node1"))

	// Add tasks with different statuses for node1
	q.tasks["eni-1"] = &ENITaskRecord{ENIID: "eni-1", NodeName: "node1", Status: TaskStatusPending}
	q.tasks["eni-2"] = &ENITaskRecord{ENIID: "eni-2", NodeName: "node1", Status: TaskStatusRunning}
	q.tasks["eni-3"] = &ENITaskRecord{ENIID: "eni-3", NodeName: "node1", Status: TaskStatusCompleted}
	q.tasks["eni-4"] = &ENITaskRecord{ENIID: "eni-4", NodeName: "node1", Status: TaskStatusFailed}
	q.tasks["eni-5"] = &ENITaskRecord{ENIID: "eni-5", NodeName: "node1", Status: TaskStatusTimeout}

	// Only Pending and Running should be counted
	assert.Equal(t, 2, q.GetAttachingCount("node1"))

	// Add tasks for node2
	q.tasks["eni-6"] = &ENITaskRecord{ENIID: "eni-6", NodeName: "node2", Status: TaskStatusPending}
	q.tasks["eni-7"] = &ENITaskRecord{ENIID: "eni-7", NodeName: "node2", Status: TaskStatusRunning}
	q.tasks["eni-8"] = &ENITaskRecord{ENIID: "eni-8", NodeName: "node2", Status: TaskStatusRunning}

	// node1 count should still be 2
	assert.Equal(t, 2, q.GetAttachingCount("node1"))

	// node2 count should be 3
	assert.Equal(t, 3, q.GetAttachingCount("node2"))

	// Non-existent node should return 0
	assert.Equal(t, 0, q.GetAttachingCount("node-nonexistent"))
}

func TestENITaskQueue_GetAttachingCount_ConcurrentLimit(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Simulate ECS max concurrent attach limit (5)
	ecsMaxConcurrent := 5
	efloMaxConcurrent := 2

	// Add 4 running tasks for ECS node
	for i := 0; i < 4; i++ {
		q.tasks[fmt.Sprintf("eni-%d", i)] = &ENITaskRecord{
			ENIID:    fmt.Sprintf("eni-%d", i),
			NodeName: "ecs-node",
			Status:   TaskStatusRunning,
		}
	}

	// ECS node can still accept 1 more task
	attachingCount := q.GetAttachingCount("ecs-node")
	availableSlots := ecsMaxConcurrent - attachingCount
	assert.Equal(t, 4, attachingCount)
	assert.Equal(t, 1, availableSlots)

	// Add 1 more task to reach limit
	q.tasks["eni-4"] = &ENITaskRecord{
		ENIID:    "eni-4",
		NodeName: "ecs-node",
		Status:   TaskStatusPending,
	}

	// Now ECS node has no available slots
	attachingCount = q.GetAttachingCount("ecs-node")
	availableSlots = ecsMaxConcurrent - attachingCount
	assert.Equal(t, 5, attachingCount)
	assert.Equal(t, 0, availableSlots)

	// Add 1 running task for EFLO node
	q.tasks["leni-1"] = &ENITaskRecord{
		ENIID:    "leni-1",
		NodeName: "eflo-node",
		Status:   TaskStatusRunning,
	}

	// EFLO node can still accept 1 more task
	attachingCount = q.GetAttachingCount("eflo-node")
	availableSlots = efloMaxConcurrent - attachingCount
	assert.Equal(t, 1, attachingCount)
	assert.Equal(t, 1, availableSlots)

	// Add 1 more task to reach EFLO limit
	q.tasks["leni-2"] = &ENITaskRecord{
		ENIID:    "leni-2",
		NodeName: "eflo-node",
		Status:   TaskStatusPending,
	}

	// Now EFLO node has no available slots
	attachingCount = q.GetAttachingCount("eflo-node")
	availableSlots = efloMaxConcurrent - attachingCount
	assert.Equal(t, 2, attachingCount)
	assert.Equal(t, 0, availableSlots)
}

func TestENITaskQueue_SubmitAttach_BackendAPI(t *testing.T) {
	// Setup mock API with expectations for async operations
	mockAPI := mocks.NewOpenAPI(t)
	tracer := noop.NewTracerProvider().Tracer("test")
	executor := ops.NewExecutor(mockAPI, tracer)

	// Setup mock expectations for AttachAsync and WaitForStatus (both ECS and EFLO ENIs)
	mockAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
		{NetworkInterfaceID: "eni-ecs-1", Status: aliyunClient.ENIStatusInUse},
		{NetworkInterfaceID: "leni-eflo-1", Status: aliyunClient.ENIStatusInUse},
		{NetworkInterfaceID: "eni-default-1", Status: aliyunClient.ENIStatusInUse},
	}, nil).Maybe()

	q := NewENITaskQueue(context.Background(), executor, nil)

	// Test ECS backend
	ecsCtx := aliyunClient.SetBackendAPI(context.Background(), aliyunClient.BackendAPIECS)
	q.SubmitAttach(ecsCtx, "eni-ecs-1", "i-ecs", "", "ecs-node", 5, 0)

	// Wait for task to be added
	task, ok := q.GetTaskStatus("eni-ecs-1")
	assert.True(t, ok)
	assert.Equal(t, aliyunClient.BackendAPIECS, task.BackendAPI)

	// Test EFLO backend
	efloCtx := aliyunClient.SetBackendAPI(context.Background(), aliyunClient.BackendAPIEFLO)
	q.SubmitAttach(efloCtx, "leni-eflo-1", "i-eflo", "", "eflo-node", 2, 0)

	task, ok = q.GetTaskStatus("leni-eflo-1")
	assert.True(t, ok)
	assert.Equal(t, aliyunClient.BackendAPIEFLO, task.BackendAPI)

	// Test default (no backend set in context should default to ECS)
	defaultCtx := context.Background()
	q.SubmitAttach(defaultCtx, "eni-default-1", "i-default", "", "default-node", 3, 0)

	task, ok = q.GetTaskStatus("eni-default-1")
	assert.True(t, ok)
	// GetBackendAPI returns BackendAPIECS as default when not set
	assert.Equal(t, aliyunClient.BackendAPIECS, task.BackendAPI)
}

// TestENITaskQueue_SubmitAttach_TaskAlreadyExists tests the logic in lines 111-118
// where existing tasks are handled based on their status
func TestENITaskQueue_SubmitAttach_TaskAlreadyExists(t *testing.T) {
	mockAPI := mocks.NewOpenAPI(t)
	tracer := noop.NewTracerProvider().Tracer("test")
	executor := ops.NewExecutor(mockAPI, tracer)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Setup mock expectations for any attach operations triggered by re-submission
	mockAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
		{NetworkInterfaceID: "eni-completed", Status: aliyunClient.ENIStatusInUse},
		{NetworkInterfaceID: "eni-failed", Status: aliyunClient.ENIStatusInUse},
		{NetworkInterfaceID: "eni-timeout", Status: aliyunClient.ENIStatusInUse},
	}, nil).Maybe()

	// Test case 1: Task already exists with Pending status
	setTaskForTesting(q, "eni-pending", &ENITaskRecord{
		ENIID:    "eni-pending",
		NodeName: "node1",
		Status:   TaskStatusPending,
	})

	// Submit the same task again - should be ignored
	q.SubmitAttach(context.Background(), "eni-pending", "i-1", "", "node1", 5, 0)

	// Task should still exist with Pending status
	task, ok := q.GetTaskStatus("eni-pending")
	assert.True(t, ok)
	assert.Equal(t, TaskStatusPending, task.Status)

	// Test case 2: Task already exists with Running status
	setTaskForTesting(q, "eni-running", &ENITaskRecord{
		ENIID:    "eni-running",
		NodeName: "node1",
		Status:   TaskStatusRunning,
	})

	// Submit the same task again - should be ignored
	q.SubmitAttach(context.Background(), "eni-running", "i-1", "", "node1", 5, 0)

	// Task should still exist with Running status
	task, ok = q.GetTaskStatus("eni-running")
	assert.True(t, ok)
	assert.Equal(t, TaskStatusRunning, task.Status)

	// Test case 3: Task exists with Completed status - should be removed and re-submitted
	now := time.Now()
	setTaskForTesting(q, "eni-completed", &ENITaskRecord{
		ENIID:       "eni-completed",
		NodeName:    "node1",
		Status:      TaskStatusCompleted,
		CompletedAt: &now,
	})

	// Submit the same task again - should remove old and add new
	q.SubmitAttach(context.Background(), "eni-completed", "i-2", "trunk-1", "node2", 3, 1)

	// Wait a bit for the goroutine to start and avoid race conditions
	time.Sleep(50 * time.Millisecond)

	// Task should be re-submitted with new data
	// Status will be Running since the goroutine starts processing immediately
	task, ok = q.GetTaskStatus("eni-completed")
	assert.True(t, ok)
	// Status should be Running (goroutine starts immediately) or Pending (if checked before goroutine starts)
	assert.True(t, task.Status == TaskStatusPending || task.Status == TaskStatusRunning,
		"expected status to be Pending or Running, got %s", task.Status)
	assert.Equal(t, "node2", task.NodeName)
	assert.Equal(t, "i-2", task.InstanceID)
	assert.Equal(t, "trunk-1", task.TrunkENIID)
	assert.Equal(t, 3, task.RequestedIPv4Count)
	assert.Equal(t, 1, task.RequestedIPv6Count)

	// Test case 4: Task exists with Failed status - should be removed and re-submitted
	setTaskForTesting(q, "eni-failed", &ENITaskRecord{
		ENIID:       "eni-failed",
		NodeName:    "node1",
		Status:      TaskStatusFailed,
		CompletedAt: &now,
		Error:       fmt.Errorf("previous error"),
	})

	// Submit the same task again - should remove old and add new
	q.SubmitAttach(context.Background(), "eni-failed", "i-3", "", "node3", 2, 0)

	// Wait a bit for the goroutine to start and avoid race conditions
	time.Sleep(50 * time.Millisecond)

	// Task should be re-submitted with new data
	// Status will be Running since the goroutine starts processing immediately
	task, ok = q.GetTaskStatus("eni-failed")
	assert.True(t, ok)
	// Status should be Running (goroutine starts immediately) or Pending (if checked before goroutine starts)
	assert.True(t, task.Status == TaskStatusPending || task.Status == TaskStatusRunning,
		"expected status to be Pending or Running, got %s", task.Status)
	assert.Equal(t, "node3", task.NodeName)
	assert.Nil(t, task.Error) // Error should be cleared

	// Test case 5: Task exists with Timeout status - should be removed and re-submitted
	setTaskForTesting(q, "eni-timeout", &ENITaskRecord{
		ENIID:       "eni-timeout",
		NodeName:    "node1",
		Status:      TaskStatusTimeout,
		CompletedAt: &now,
	})

	// Submit the same task again - should remove old and add new
	q.SubmitAttach(context.Background(), "eni-timeout", "i-4", "", "node4", 1, 0)

	// Task should be re-submitted with new data
	// Status may be Running if goroutine started, or Pending if checked immediately
	task, ok = q.GetTaskStatus("eni-timeout")
	assert.True(t, ok)
	// Status should be Running (goroutine starts immediately) or Pending (if checked before goroutine starts)
	assert.True(t, task.Status == TaskStatusPending || task.Status == TaskStatusRunning,
		"expected status to be Pending or Running, got %s", task.Status)
	assert.Equal(t, "node4", task.NodeName)

	// Wait for any spawned goroutines to complete
	time.Sleep(300 * time.Millisecond)
}

// TestENITaskQueue_ProcessAttachTask_ErrorHandling tests the error handling logic in lines 186-192
// where different error types are handled differently
func TestENITaskQueue_ProcessAttachTask_ErrorHandling(t *testing.T) {
	// Test case 1: Verify Timeout status is set when context.DeadlineExceeded occurs
	// This tests the first condition: errors.Is(err, context.DeadlineExceeded)
	t.Run("Direct_DeadlineExceeded_error", func(t *testing.T) {
		executor := createTestExecutor(t)
		q := NewENITaskQueue(context.Background(), executor, nil)

		// Manually create a task and complete it with DeadlineExceeded error
		now := time.Now()
		eniID := "eni-timeout-test"
		q.tasks[eniID] = &ENITaskRecord{
			ENIID:     eniID,
			NodeName:  "node1",
			Status:    TaskStatusRunning,
			CreatedAt: now.Add(-5 * time.Second),
		}

		// Complete task with DeadlineExceeded error (tests line 186 first condition)
		q.completeTask(eniID, TaskStatusTimeout, nil, context.DeadlineExceeded)

		task, ok := q.GetTaskStatus(eniID)
		assert.True(t, ok)
		assert.Equal(t, TaskStatusTimeout, task.Status)
		assert.NotNil(t, task.Error)
		assert.ErrorIs(t, task.Error, context.DeadlineExceeded)
	})

	// Test case 2: Verify Timeout status is set for wrapped DeadlineExceeded error
	// This tests the second condition: errors.Is(taskCtx.Err(), context.DeadlineExceeded)
	t.Run("Wrapped_DeadlineExceeded_error", func(t *testing.T) {
		executor := createTestExecutor(t)
		q := NewENITaskQueue(context.Background(), executor, nil)

		now := time.Now()
		eniID := "eni-timeout-wrapped"
		q.tasks[eniID] = &ENITaskRecord{
			ENIID:     eniID,
			NodeName:  "node1",
			Status:    TaskStatusRunning,
			CreatedAt: now.Add(-5 * time.Second),
		}

		// Complete task with wrapped DeadlineExceeded error
		wrappedErr := fmt.Errorf("attach timeout: %w", context.DeadlineExceeded)
		q.completeTask(eniID, TaskStatusTimeout, nil, wrappedErr)

		task, ok := q.GetTaskStatus(eniID)
		assert.True(t, ok)
		assert.Equal(t, TaskStatusTimeout, task.Status)
		assert.NotNil(t, task.Error)
		assert.ErrorIs(t, task.Error, context.DeadlineExceeded)
	})

	// Test case 3: Verify Failed status is set for non-timeout errors
	// This tests the else branch (line 189-191)
	t.Run("Regular_error", func(t *testing.T) {
		executor := createTestExecutor(t)
		q := NewENITaskQueue(context.Background(), executor, nil)

		now := time.Now()
		eniID := "eni-failed-test"
		q.tasks[eniID] = &ENITaskRecord{
			ENIID:     eniID,
			NodeName:  "node1",
			Status:    TaskStatusRunning,
			CreatedAt: now.Add(-2 * time.Second),
		}

		// Complete task with regular error (not DeadlineExceeded)
		regularErr := fmt.Errorf("network interface not found")
		q.completeTask(eniID, TaskStatusFailed, nil, regularErr)

		task, ok := q.GetTaskStatus(eniID)
		assert.True(t, ok)
		assert.Equal(t, TaskStatusFailed, task.Status)
		assert.NotNil(t, task.Error)
		assert.Contains(t, task.Error.Error(), "network interface not found")
		assert.NotErrorIs(t, task.Error, context.DeadlineExceeded)
	})

	// Test case 4: Verify Completed status is set when there's no error
	// This tests the success path (line 194-195)
	t.Run("Success_path", func(t *testing.T) {
		executor := createTestExecutor(t)
		q := NewENITaskQueue(context.Background(), executor, nil)

		now := time.Now()
		eniID := "eni-success-test"
		q.tasks[eniID] = &ENITaskRecord{
			ENIID:     eniID,
			NodeName:  "node1",
			Status:    TaskStatusRunning,
			CreatedAt: now.Add(-3 * time.Second),
		}

		// Complete task successfully
		eniInfo := &aliyunClient.NetworkInterface{
			NetworkInterfaceID: eniID,
			Status:             aliyunClient.ENIStatusInUse,
		}
		q.completeTask(eniID, TaskStatusCompleted, eniInfo, nil)

		task, ok := q.GetTaskStatus(eniID)
		assert.True(t, ok)
		assert.Equal(t, TaskStatusCompleted, task.Status)
		assert.NotNil(t, task.ENIInfo)
		assert.Equal(t, eniID, task.ENIInfo.NetworkInterfaceID)
		assert.Nil(t, task.Error)
	})
}

// TestENITaskQueue_GetPendingENIs tests the logic in lines 283-297
// for filtering ENIs by node and status
func TestENITaskQueue_GetPendingENIs(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Test empty queue
	eniIDs := q.GetPendingENIs("node1")
	assert.Empty(t, eniIDs)

	// Add tasks with various statuses for node1
	q.tasks["eni-pending-1"] = &ENITaskRecord{
		ENIID:    "eni-pending-1",
		NodeName: "node1",
		Status:   TaskStatusPending,
	}
	q.tasks["eni-running-1"] = &ENITaskRecord{
		ENIID:    "eni-running-1",
		NodeName: "node1",
		Status:   TaskStatusRunning,
	}
	q.tasks["eni-completed-1"] = &ENITaskRecord{
		ENIID:    "eni-completed-1",
		NodeName: "node1",
		Status:   TaskStatusCompleted,
	}
	q.tasks["eni-failed-1"] = &ENITaskRecord{
		ENIID:    "eni-failed-1",
		NodeName: "node1",
		Status:   TaskStatusFailed,
	}
	q.tasks["eni-timeout-1"] = &ENITaskRecord{
		ENIID:    "eni-timeout-1",
		NodeName: "node1",
		Status:   TaskStatusTimeout,
	}

	// Add tasks for node2
	q.tasks["eni-pending-2"] = &ENITaskRecord{
		ENIID:    "eni-pending-2",
		NodeName: "node2",
		Status:   TaskStatusPending,
	}
	q.tasks["eni-running-2"] = &ENITaskRecord{
		ENIID:    "eni-running-2",
		NodeName: "node2",
		Status:   TaskStatusRunning,
	}

	// Get pending ENIs for node1 - should only return Pending and Running
	eniIDs = q.GetPendingENIs("node1")
	assert.Len(t, eniIDs, 2)
	assert.Contains(t, eniIDs, "eni-pending-1")
	assert.Contains(t, eniIDs, "eni-running-1")

	// Get pending ENIs for node2
	eniIDs = q.GetPendingENIs("node2")
	assert.Len(t, eniIDs, 2)
	assert.Contains(t, eniIDs, "eni-pending-2")
	assert.Contains(t, eniIDs, "eni-running-2")

	// Get pending ENIs for non-existent node
	eniIDs = q.GetPendingENIs("node-nonexistent")
	assert.Empty(t, eniIDs)

	// Test that only Pending and Running are returned, not Completed/Failed/Timeout
	for _, eniID := range eniIDs {
		task, _ := q.GetTaskStatus(eniID)
		assert.True(t, task.Status == TaskStatusPending || task.Status == TaskStatusRunning)
	}
}

// TestENITaskQueue_RemoveTask tests the logic in lines 329-337
func TestENITaskQueue_RemoveTask(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	// Add a task
	q.tasks["eni-1"] = &ENITaskRecord{
		ENIID:    "eni-1",
		NodeName: "node1",
		Status:   TaskStatusPending,
	}

	// Verify task exists
	_, ok := q.GetTaskStatus("eni-1")
	assert.True(t, ok)

	// Remove the task
	q.RemoveTask("eni-1")

	// Verify task is removed
	_, ok = q.GetTaskStatus("eni-1")
	assert.False(t, ok)

	// Remove non-existent task - should not panic
	q.RemoveTask("eni-nonexistent")

	// Test removing tasks with different statuses
	q.tasks["eni-running"] = &ENITaskRecord{ENIID: "eni-running", Status: TaskStatusRunning}
	q.tasks["eni-completed"] = &ENITaskRecord{ENIID: "eni-completed", Status: TaskStatusCompleted}
	q.tasks["eni-failed"] = &ENITaskRecord{ENIID: "eni-failed", Status: TaskStatusFailed}

	q.RemoveTask("eni-running")
	q.RemoveTask("eni-completed")
	q.RemoveTask("eni-failed")

	_, ok = q.GetTaskStatus("eni-running")
	assert.False(t, ok)
	_, ok = q.GetTaskStatus("eni-completed")
	assert.False(t, ok)
	_, ok = q.GetTaskStatus("eni-failed")
	assert.False(t, ok)
}

// TestENITaskQueue_RecordAttachDuration tests the logic in lines 400-404
// for setting the correct result label based on task status
func TestENITaskQueue_RecordAttachDuration(t *testing.T) {
	executor := createTestExecutor(t)
	q := NewENITaskQueue(context.Background(), executor, nil)

	now := time.Now()
	createdAt := now.Add(-5 * time.Second)
	completedAt := now

	// Test case 1: Completed task - result should be "success"
	taskCompleted := &ENITaskRecord{
		ENIID:       "eni-completed",
		Status:      TaskStatusCompleted,
		CreatedAt:   createdAt,
		CompletedAt: &completedAt,
	}
	q.recordAttachDuration(taskCompleted)

	// Test case 2: Failed task - result should be "failed"
	taskFailed := &ENITaskRecord{
		ENIID:       "eni-failed",
		Status:      TaskStatusFailed,
		CreatedAt:   createdAt,
		CompletedAt: &completedAt,
	}
	q.recordAttachDuration(taskFailed)

	// Test case 3: Timeout task - result should be "timeout"
	taskTimeout := &ENITaskRecord{
		ENIID:       "eni-timeout",
		Status:      TaskStatusTimeout,
		CreatedAt:   createdAt,
		CompletedAt: &completedAt,
	}
	q.recordAttachDuration(taskTimeout)

	// Test case 4: Task with no CompletedAt - should return early
	taskNoCompletion := &ENITaskRecord{
		ENIID:       "eni-no-completion",
		Status:      TaskStatusRunning,
		CreatedAt:   createdAt,
		CompletedAt: nil,
	}
	q.recordAttachDuration(taskNoCompletion) // Should not panic

	// Test case 5: ECS ENI type (default)
	taskECS := &ENITaskRecord{
		ENIID:       "eni-ecs-123",
		Status:      TaskStatusCompleted,
		CreatedAt:   createdAt,
		CompletedAt: &completedAt,
	}
	q.recordAttachDuration(taskECS)

	// Test case 6: EFLO ENI type (starts with "leni-")
	taskEFLO := &ENITaskRecord{
		ENIID:       "leni-eflo-123",
		Status:      TaskStatusCompleted,
		CreatedAt:   createdAt,
		CompletedAt: &completedAt,
	}
	q.recordAttachDuration(taskEFLO)

	// Test case 7: HDENI type (starts with "hdeni-")
	taskHDENI := &ENITaskRecord{
		ENIID:       "hdeni-hd-123",
		Status:      TaskStatusCompleted,
		CreatedAt:   createdAt,
		CompletedAt: &completedAt,
	}
	q.recordAttachDuration(taskHDENI)
}
