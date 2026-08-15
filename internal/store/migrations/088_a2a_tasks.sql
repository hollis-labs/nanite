-- A2A Task Tracking Tables
-- Per docs/architecture/a2a-protocol-design.md
-- Bookkeeping for Agent-to-Agent protocol Task lifecycle. These records point
-- at the real execution (workflow_runs or durable_agent_instances), never
-- duplicate their state. TaskState is derived on read, not maintained separately.

CREATE TABLE IF NOT EXISTS a2a_tasks (
    id TEXT PRIMARY KEY,
    
    -- Target kind: "workflow" or "instance"
    target_kind TEXT NOT NULL CHECK(target_kind IN ('workflow', 'instance')),
    
    -- Target reference: workflow skill name or instance msg:// URN
    target_ref TEXT NOT NULL,
    
    -- Caller-supplied message content
    message TEXT NOT NULL,
    
    -- DurableAgentInstanceID is set once routing resolves
    durable_agent_instance_id TEXT REFERENCES durable_agent_instances(id),
    
    -- WorkflowRunID is set when the task is backed by a workflow run
    workflow_run_id TEXT REFERENCES workflow_runs(id),
    
    -- Derived TaskState — cached, refreshed on read and on push-delivery trigger.
    -- Vocabulary matches internal/a2a/types.go's TaskState constants exactly
    -- (A2A v1.0 spec wire values), not an invented set.
    state TEXT NOT NULL CHECK(state IN ('submitted', 'working', 'input-required', 'completed', 'failed', 'canceled', 'rejected', 'auth-required')),
    
    -- Result and error fields from task execution
    result TEXT,
    error TEXT,
    
    -- Push notification config (JSON-encoded PushNotificationConfig)
    push_notification_config TEXT,
    
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_a2a_tasks_instance ON a2a_tasks(durable_agent_instance_id);
CREATE INDEX IF NOT EXISTS idx_a2a_tasks_workflow_run ON a2a_tasks(workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_a2a_tasks_state ON a2a_tasks(state);
CREATE INDEX IF NOT EXISTS idx_a2a_tasks_created ON a2a_tasks(created_at);

-- A2APushDelivery tracks push notification delivery retry state. Column
-- shape matches internal/store/a2a_tasks.go's Create/Get/UpdateA2APushDelivery
-- queries exactly (retry-with-backoff model: attempt_count + next_retry),
-- not a separately-imagined per-attempt-detail shape.
CREATE TABLE IF NOT EXISTS a2a_push_deliveries (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES a2a_tasks(id) ON DELETE CASCADE,

    -- Which TaskState transition triggered this push
    target_state TEXT NOT NULL,

    -- Retry bookkeeping
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    next_retry TIMESTAMP,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_a2a_push_deliveries_task ON a2a_push_deliveries(task_id);
CREATE INDEX IF NOT EXISTS idx_a2a_push_deliveries_next_retry ON a2a_push_deliveries(next_retry);
