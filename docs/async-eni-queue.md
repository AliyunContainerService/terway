# Async ENI Task Queue Design

## Overview

The Async ENI Task Queue is designed to decouple the blocking ENI attachment operations from the main Node reconciliation loop in the Terway controller. This improves the responsiveness of the controller and prevents blocking the reconciliation worker threads during slow Aliyun API calls.

## Key Components

### 1. ENITaskQueue (`pkg/controller/multi-ip/node/eni_task_queue.go`)

The core component that manages the lifecycle of async ENI operations.

- **In-Memory Queue**: Stores task state (`ENITaskRecord`) keyed by ENI ID.
- **Async Processing**: Uses goroutines to handle individual attach tasks.
- **Notification**: Signals the controller via a channel when a task completes.

### 2. Executor (`pkg/eni/ops/executor.go`)

Provides the low-level ENI operations.

- **AttachAsync**: Initiates the attach operation via Aliyun API (non-blocking).
- **CheckStatus**: Checks the current status of an ENI.
- **Wait Logic**: Handles backoff and polling for status changes.

## Workflow

### 1. Submission

When the controller determines a new ENI is needed:

1. It creates the ENI via OpenAPI (blocking, as it's fast).
2. It calls `SubmitAttach` to queue the attach operation.
3. The task is added to the map with `Pending` status.
4. A background goroutine is started for the task.
5. The Node CR status is optimistically updated to `Attaching`.

### 2. Processing (`processAttachTask`)

The background goroutine performs the following steps:

1. **Status Check**: Verifies the current ENI status. If already `InUse`, marks as `Completed` (handles controller restarts).
2. **Initiate Attach**: Calls `AttachAsync` if needed.
3. **Wait**: Sleeps for an initial delay (based on ENI type).
4. **Poll**: Polls the API until the status becomes `InUse` or timeout.
5. **Completion**: Updates the task status to `Completed` or `Failed` and notifies the controller.

### 3. Reconciliation (`syncTaskQueueStatus`)

In the main `Reconcile` loop:

1. The controller calls `GetCompletedTasks`.
2. Completed tasks are **removed** from the queue.
3. The Node CR is updated with the result (e.g., `InUse` status, IP details).

## State Machine

- **Pending**: Task submitted, waiting to start.
- **Running**: Goroutine started, operation in progress.
- **Completed**: Operation successful.
- **Failed**: Operation failed (API error).
- **Timeout**: Operation timed out.

## Reliability & Idempotency

- **Duplicate Submission**: `SubmitAttach` ignores tasks that are already `Pending` or `Running`.
- **Controller Restarts**: The `processAttachTask` first checks the actual ENI status. If the ENI was attached during a previous run (but status wasn't updated), it detects this and completes immediately.
- **Reconciliation Loop**: The controller periodically reconciles and checks for completed tasks, ensuring the CR state eventually matches the actual state.
