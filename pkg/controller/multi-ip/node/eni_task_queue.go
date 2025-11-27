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
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/eni/ops"
)

// ENIOperation represents an ENI operation type
type ENIOperation string

const (
	OpAttach ENIOperation = "Attach"
	OpDetach ENIOperation = "Detach"
	OpDelete ENIOperation = "Delete"
)

// ENITaskStatus represents the status of an ENI task
type ENITaskStatus string

const (
	TaskStatusPending   ENITaskStatus = "Pending"
	TaskStatusRunning   ENITaskStatus = "Running"
	TaskStatusCompleted ENITaskStatus = "Completed"
	TaskStatusFailed    ENITaskStatus = "Failed"
	TaskStatusTimeout   ENITaskStatus = "Timeout"
)

// ENITaskRecord stores the state of an ENI operation task
type ENITaskRecord struct {
	ENIID      string
	Operation  ENIOperation
	InstanceID string
	TrunkENIID string
	NodeName   string

	// BackendAPI stores the backend type (ECS or EFLO) for this task.
	// This is needed because the task runs asynchronously and needs to use
	// the correct backend API for attach/query operations.
	BackendAPI aliyunClient.BackendAPI

	Status      ENITaskStatus
	CreatedAt   time.Time
	CompletedAt *time.Time

	// Record the number of IPs requested when creating ENI, used for quota calculation
	RequestedIPv4Count int
	RequestedIPv6Count int

	// Result after completion
	ENIInfo *aliyunClient.NetworkInterface
	Error   error
}

// ENITaskQueue manages async ENI operations
type ENITaskQueue struct {
	ctx   context.Context
	mu    sync.RWMutex
	tasks map[string]*ENITaskRecord // key: ENIID

	executor *ops.Executor
	notifyCh chan string // node name to notify

	log logr.Logger
}

// NewENITaskQueue creates a new task queue
func NewENITaskQueue(ctx context.Context, executor *ops.Executor, notifyCh chan string) *ENITaskQueue {
	return &ENITaskQueue{
		ctx:      ctx,
		tasks:    make(map[string]*ENITaskRecord),
		executor: executor,
		notifyCh: notifyCh,
		log:      logf.Log.WithName("eni-task-queue"),
	}
}

// SubmitAttach submits an async attach task with requested IP counts
// This method never fails - it only adds a task to in-memory queue
func (q *ENITaskQueue) SubmitAttach(ctx context.Context, eniID, instanceID, trunkENIID, nodeName string,
	requestedIPv4, requestedIPv6 int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Check if task already exists
	if existing, ok := q.tasks[eniID]; ok {
		if existing.Status == TaskStatusPending || existing.Status == TaskStatusRunning {
			q.log.Info("task already exists and in progress", "eni", eniID, "status", existing.Status)
			return // Task already in progress
		}
		// Remove completed/failed task to allow re-submission
		delete(q.tasks, eniID)
	}

	// Capture the backend API from ctx for later use in async processing
	backendAPI := aliyunClient.GetBackendAPI(ctx)

	task := &ENITaskRecord{
		ENIID:              eniID,
		Operation:          OpAttach,
		InstanceID:         instanceID,
		TrunkENIID:         trunkENIID,
		NodeName:           nodeName,
		BackendAPI:         backendAPI,
		Status:             TaskStatusPending,
		CreatedAt:          time.Now(),
		RequestedIPv4Count: requestedIPv4,
		RequestedIPv6Count: requestedIPv6,
	}

	q.tasks[eniID] = task
	q.log.Info("submitted attach task", "eni", eniID, "node", nodeName,
		"backendAPI", backendAPI, "requestedIPv4", requestedIPv4, "requestedIPv6", requestedIPv6)

	// Start processing in background
	go q.processAttachTask(q.ctx, task)
}

// processAttachTask handles an attach task
func (q *ENITaskQueue) processAttachTask(ctx context.Context, task *ENITaskRecord) {
	log := q.log.WithValues("eni", task.ENIID, "node", task.NodeName, "backendAPI", task.BackendAPI)
	log.Info("starting attach task")

	q.updateTaskStatus(task.ENIID, TaskStatusRunning, nil)

	// Set the backend API in context for API calls
	// This ensures attach/query operations use the correct backend (ECS or EFLO)
	ctx = aliyunClient.SetBackendAPI(ctx, task.BackendAPI)

	// Get timeout based on ENI type
	timeout := q.executor.GetTimeout(task.ENIID)
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if !isEFLORes(task.ENIID) {
		// ECS: Attach first (lazy check)
		err := q.executor.AttachAsync(taskCtx, task.ENIID, task.InstanceID, task.TrunkENIID)
		if err != nil {
			log.Error(err, "failed to initiate attach")
			q.completeTask(task.ENIID, TaskStatusFailed, nil, err)
			q.notifyNode(task.NodeName)
			return
		}
	}

	// Wait initial delay
	initialDelay := q.executor.GetInitialDelay(task.ENIID)
	select {
	case <-taskCtx.Done():
		q.completeTask(task.ENIID, TaskStatusTimeout, nil, taskCtx.Err())
		q.notifyNode(task.NodeName)
		return
	case <-time.After(initialDelay):
	}

	// Poll for completion using BackoffManager
	result, err := q.executor.WaitForStatus(taskCtx, task.ENIID, aliyunClient.ENIStatusInUse)
	now := time.Now()

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
			log.Error(err, "attach task timeout")
			q.completeTask(task.ENIID, TaskStatusTimeout, nil, fmt.Errorf("attach timeout after %v", timeout))
		} else {
			log.Error(err, "attach task failed")
			q.completeTask(task.ENIID, TaskStatusFailed, nil, err)
		}
	} else {
		log.Info("attach task completed successfully", "duration", now.Sub(task.CreatedAt))
		q.completeTask(task.ENIID, TaskStatusCompleted, result, nil)
	}

	q.notifyNode(task.NodeName)
}

// completeTask marks a task as completed with result
func (q *ENITaskQueue) completeTask(eniID string, status ENITaskStatus, eniInfo *aliyunClient.NetworkInterface, err error) {
	q.mu.Lock()

	task, ok := q.tasks[eniID]
	if !ok {
		q.mu.Unlock()
		return
	}

	now := time.Now()
	task.Status = status
	task.CompletedAt = &now
	task.ENIInfo = eniInfo
	task.Error = err
	q.mu.Unlock()

	// Record metrics
	q.recordAttachDuration(task)
	q.updateQueueMetrics()
}

// updateTaskStatus updates the status of a task
func (q *ENITaskQueue) updateTaskStatus(eniID string, status ENITaskStatus, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if task, ok := q.tasks[eniID]; ok {
		task.Status = status
		task.Error = err
	}
}

// GetTaskStatus returns the current status of a task
func (q *ENITaskQueue) GetTaskStatus(eniID string) (*ENITaskRecord, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	task, ok := q.tasks[eniID]
	if !ok {
		return nil, false
	}

	// Return a copy to avoid race conditions
	taskCopy := *task
	return &taskCopy, true
}

// PeekCompletedTasks returns all completed/failed tasks for a node without removing them
func (q *ENITaskQueue) PeekCompletedTasks(nodeName string) []*ENITaskRecord {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var completedTasks []*ENITaskRecord

	for _, task := range q.tasks {
		if task.NodeName != nodeName {
			continue
		}

		if task.Status == TaskStatusCompleted ||
			task.Status == TaskStatusFailed ||
			task.Status == TaskStatusTimeout {
			// Return a copy
			taskCopy := *task
			completedTasks = append(completedTasks, &taskCopy)
		}
	}

	return completedTasks
}

// DeleteTasks removes specific tasks from the queue
func (q *ENITaskQueue) DeleteTasks(eniIDs []string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, eniID := range eniIDs {
		delete(q.tasks, eniID)
		q.log.V(4).Info("removed task from queue", "eni", eniID)
	}
}

// GetPendingENIs returns ENI IDs that are still pending/running for a node
func (q *ENITaskQueue) GetPendingENIs(nodeName string) []string {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var eniIDs []string
	for eniID, task := range q.tasks {
		if task.NodeName == nodeName &&
			(task.Status == TaskStatusPending || task.Status == TaskStatusRunning) {
			eniIDs = append(eniIDs, eniID)
		}
	}
	return eniIDs
}

// GetAttachingCount returns the number of ENIs currently being attached for a node.
// This includes both in-queue tasks (Pending/Running) to help enforce concurrent attach limits.
func (q *ENITaskQueue) GetAttachingCount(nodeName string) int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	count := 0
	for _, task := range q.tasks {
		if task.NodeName == nodeName &&
			(task.Status == TaskStatusPending || task.Status == TaskStatusRunning) {
			count++
		}
	}
	return count
}

// HasPendingTasks checks if there are any pending tasks for a node
func (q *ENITaskQueue) HasPendingTasks(nodeName string) bool {
	q.mu.RLock()
	defer q.mu.RUnlock()

	for _, task := range q.tasks {
		if task.NodeName == nodeName &&
			(task.Status == TaskStatusPending || task.Status == TaskStatusRunning) {
			return true
		}
	}
	return false
}

// RemoveTask removes a task from the queue
func (q *ENITaskQueue) RemoveTask(eniID string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.tasks, eniID)
	q.log.V(4).Info("removed task from queue", "eni", eniID)
}

// RemoveTasks removes all tasks for a specific node
func (q *ENITaskQueue) RemoveTasks(nodeName string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var toRemove []string
	for eniID, task := range q.tasks {
		if task.NodeName == nodeName {
			toRemove = append(toRemove, eniID)
		}
	}

	for _, eniID := range toRemove {
		delete(q.tasks, eniID)
	}

	if len(toRemove) > 0 {
		q.log.Info("removed tasks for node", "node", nodeName, "count", len(toRemove))
	}
}

// notifyNode sends a notification to reconcile a node
func (q *ENITaskQueue) notifyNode(nodeName string) {
	select {
	case q.notifyCh <- nodeName:
		q.log.V(4).Info("notified node", "node", nodeName)
	default:
		// Channel full, node will be reconciled eventually
		q.log.V(4).Info("notify channel full, skipping", "node", nodeName)
	}
}

// GetQueueStats returns queue statistics for metrics
func (q *ENITaskQueue) GetQueueStats() map[ENITaskStatus]int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	stats := make(map[ENITaskStatus]int)
	for _, task := range q.tasks {
		stats[task.Status]++
	}
	return stats
}

// updateQueueMetrics updates Prometheus metrics for queue size
func (q *ENITaskQueue) updateQueueMetrics() {
	stats := q.GetQueueStats()
	for status, count := range stats {
		ENITaskQueueSize.WithLabelValues(string(status)).Set(float64(count))
	}
}

// recordAttachDuration records the duration of an attach operation
func (q *ENITaskQueue) recordAttachDuration(task *ENITaskRecord) {
	if task.CompletedAt == nil {
		return
	}

	duration := task.CompletedAt.Sub(task.CreatedAt).Seconds()
	result := "success"

	switch task.Status {
	case TaskStatusFailed:
		result = "failed"
	case TaskStatusTimeout:
		result = "timeout"
	}

	eniType := "ecs"
	if strings.HasPrefix(task.ENIID, "leni-") {
		eniType = "eflo"
	} else if strings.HasPrefix(task.ENIID, "hdeni-") {
		eniType = "hdeni"
	}

	ENIAttachDuration.WithLabelValues(result, eniType).Observe(duration)
}

// CleanupStaleTasks removes tasks that are orphaned or stale
// - Tasks for ENIs not in validENIIDs are considered orphaned
// - Completed tasks older than staleThreshold are considered stale
func (q *ENITaskQueue) CleanupStaleTasks(nodeName string, validENIIDs map[string]struct{}, staleThreshold time.Duration) []string {
	q.mu.Lock()
	defer q.mu.Unlock()

	var removed []string
	now := time.Now()

	for eniID, task := range q.tasks {
		if task.NodeName != nodeName {
			continue
		}

		shouldRemove := false

		// Check if ENI no longer exists in CR (orphaned task)
		if _, exists := validENIIDs[eniID]; !exists {
			q.log.Info("removing orphaned task (ENI not in CR)", "eni", eniID, "status", task.Status)
			shouldRemove = true
		}

		// Check if task is completed but stale (not consumed for too long)
		if !shouldRemove && task.CompletedAt != nil {
			if task.Status == TaskStatusCompleted ||
				task.Status == TaskStatusFailed ||
				task.Status == TaskStatusTimeout {
				if now.Sub(*task.CompletedAt) > staleThreshold {
					q.log.Info("removing stale completed task", "eni", eniID, "status", task.Status,
						"completedAt", task.CompletedAt, "staleDuration", now.Sub(*task.CompletedAt))
					shouldRemove = true
				}
			}
		}

		if shouldRemove {
			delete(q.tasks, eniID)
			removed = append(removed, eniID)
		}
	}

	return removed
}

func isEFLORes(in string) bool {
	return strings.HasPrefix(in, "leni-") || strings.HasPrefix(in, "hdeni-")
}
