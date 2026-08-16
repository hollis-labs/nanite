# CW-20260814-0018 Implementation Summary

## A2A Push Notification Config + Best-Effort Delivery

**Status**: ✅ Complete  
**Date**: 2026-08-15  
**Design Doc**: `docs/architecture/a2a-protocol-design.md` (Push notifications section)

## What Was Implemented

### 1. Push Notification Service (`internal/service/a2a_push_notifier.go`)

Created `A2APushNotifier` service with the following capabilities:

- **EnqueueDelivery**: Creates delivery records on task state transitions
- **ProcessPendingDeliveries**: Background worker that processes queued deliveries
- **Bounded retries**: Max 3 attempts with exponential backoff (1min, 5min, 15min)
- **Best-effort philosophy**: Failed deliveries don't break the task lifecycle

Key design decisions:
- HTTP client with 10-second timeout
- Supports both `Token` (Bearer) and `Auth` (custom) headers from PushNotificationConfig
- Deletes delivery record on success or after max attempts
- Logs failures but doesn't fail the calling operation

### 2. Store Methods (`internal/store/a2a_tasks.go`)

Added missing method:
- **DeleteA2APushDelivery**: Removes delivery record after success or max attempts

Existing methods (already present in migration 088):
- `CreateA2APushDelivery`
- `GetPendingPushDeliveries`
- `UpdateA2APushDelivery`

### 3. TaskManager Integration (`internal/service/a2a_task_manager.go`)

Updated TaskManager to:
- Include `pushNotifier *A2APushNotifier` field
- Create notifier in `NewTaskManager`
- Add `enqueuePushNotification` helper method
- Add `PushNotifier()` accessor for background worker

**State transition hooks added**:
1. `submitWorkflowTask`: submitted → working
2. `submitInstanceTask`: submitted → working
3. `updateTaskStateFailed`: any → failed
4. `GetTask`: when derived state differs from cached

Each hook:
- Checks if state actually changed
- Checks if PushNotificationConfig is present
- Calls `enqueuePushNotification` (best-effort, logs on failure)

### 4. Background Worker (`cmd/nanite/main.go`)

Added new ticker-based worker in `startBackgroundWorkers`:
- **Name**: `a2a-push-delivery`
- **Interval**: 30 seconds
- **Action**: Calls `TaskManager.PushNotifier().ProcessPendingDeliveries(ctx)`
- **Pattern**: Follows existing worker pattern (stale-process-reaper, stale-worker-reaper, etc.)

### 5. Integration Tests (`internal/service/a2a_push_notifier_test.go`)

Comprehensive test coverage:
- **TestA2APushNotifierEnqueueAndDeliver**: Full lifecycle (enqueue → deliver → verify HTTP POST → delete)
- **TestA2APushNotifierRetry**: Verifies retry with backoff on failure
- **TestA2APushNotifierMaxRetries**: Verifies deletion after 3 failed attempts

Tests use `httptest.Server` to verify actual HTTP delivery behavior.

## Database Schema

The `a2a_push_deliveries` table was already created in migration `088_a2a_tasks.sql`:

```sql
CREATE TABLE IF NOT EXISTS a2a_push_deliveries (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES a2a_tasks(id) ON DELETE CASCADE,
    target_state TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    next_retry TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_a2a_push_deliveries_task ON a2a_push_deliveries(task_id);
CREATE INDEX idx_a2a_push_deliveries_next_retry ON a2a_push_deliveries(next_retry);
```

**No new migration needed** — table already exists.

## Acceptance Criteria — Status

1. ✅ Migration creates a2a_push_deliveries table  
   → Already existed in 088_a2a_tasks.sql

2. ✅ TaskManager state transitions trigger push delivery when PushNotificationConfig registered  
   → Hooks added to submitWorkflowTask, submitInstanceTask, updateTaskStateFailed, GetTask

3. ✅ Background worker processes delivery queue with bounded retries  
   → Worker added to startBackgroundWorkers with 30s ticker

4. ✅ Transport layer allows setting push config  
   → Already implemented in CW-20260814-0016 (task-submit accepts PushNotificationConfig)

5. ✅ Integration test covering: task with push config → state transition → delivery attempt recorded  
   → Three comprehensive tests added to a2a_push_notifier_test.go

## Non-Goals (Confirmed Out of Scope)

- ❌ Delivery guarantees — best-effort only, per design doc
- ❌ Webhook signing/verification — explicitly deferred
- ❌ Streaming/SSE support — future addition

## Files Modified

1. **New**: `internal/service/a2a_push_notifier.go` (236 lines)
2. **New**: `internal/service/a2a_push_notifier_test.go` (347 lines)
3. **Modified**: `internal/store/a2a_tasks.go` (added DeleteA2APushDelivery)
4. **Modified**: `internal/service/a2a_task_manager.go` (added push notifier integration)
5. **Modified**: `cmd/nanite/main.go` (added background worker)

## Verification Steps

To verify the implementation:

```bash
# 1. Build should succeed
go build ./cmd/nanite

# 2. Tests should pass
go test ./internal/service -run TestA2APushNotifier

# 3. Integration test with real task submission
# (Requires running Nanite instance + httpbin or similar webhook receiver)
curl -X POST http://localhost:8080/api/a2a/jsonrpc \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "task-submit",
    "params": {
      "target": "researcher",
      "message": "Test task",
      "pushNotificationConfig": {
        "url": "https://httpbin.org/post"
      }
    },
    "id": 1
  }'

# Check httpbin.org received the notification on state transitions
```

## Next Steps / Follow-up Opportunities

1. **Webhook signing** (if needed for production security)
   - Design doc explicitly defers this
   - Could be small follow-up if required

2. **Configurable retry behavior**
   - Currently hardcoded to 3 attempts with exponential backoff
   - Could be made configurable per-task if needed

3. **Delivery metrics/monitoring**
   - Success/failure rates
   - Retry distribution
   - Could feed into observability dashboard

4. **Cancel-task support**
   - Would trigger push notification for canceled state
   - Not blocking this implementation

## Design Philosophy Alignment

This implementation follows the design doc's core principle:

> "A2A is a protocol adapter, not a new execution substrate."

Push notifications are:
- Best-effort accelerants over the durable `a2a_tasks` record
- Non-blocking to task lifecycle
- Follow Tether's `/messages/notify` pattern
- Never the source of truth (task-get remains authoritative)

The bounded-retry model (3 attempts, exponential backoff, then delete) matches existing background workers in `startBackgroundWorkers` and respects the "no delivery guarantee" spec interpretation.
