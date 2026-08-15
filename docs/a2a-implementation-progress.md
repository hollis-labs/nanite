# A2A Protocol Implementation Progress

## Completed (Foundation Layer)

### 1. ✅ internal/a2a/ Package (Zero-Import Discipline)
- **types.go**: Complete wire types for AgentCard, Skill, Task, TaskState, PushNotificationConfig, TaskSubmitRequest/Response
- **jsonrpc.go**: Full JSON-RPC 2.0 envelope types (request, response, error)
- **address.go**: Canonical msg:// address derivation wrapping go-messaging types
  - GenerateAgentURN(), SlugAliasURN(), AgentAddress(), etc.
  - Closes the hand-rolled duplicate constant gap (internal/store/agents.go:18)

### 2. ✅ Service Layer Implementation
- **internal/service/a2a_agent_card.go**: AgentCardGenerator
  - Derives skills from WorkflowDefinition registry
  - Derives skills from boot profile catalog
  - One card for Nanite host
- **internal/service/a2a_task.go**: A2ATaskService
  - SubmitTask: Routes to workflow launch or durable-agent wake
  - GetTask: Retrieves and refreshes task state
  - CancelTask: Marks task as canceled
  - State derivation from real execution (workflow runs / instances)

### 3. ✅ Store Types
- **internal/store/a2a_tasks.go**: A2ATask and A2APushDelivery types
  - Bookkeeping records, not duplicate state
  - Points at workflow_runs or durable_agent_instances

### 4. ✅ MCP Self-Tool: whoami
- **internal/mcp/self_tools_whoami.go**: Self-discovery tool
  - Returns canonical msg:// address
  - Instance-aware (durable agent vs ephemeral session)
  - Closes CW-20260519-0069
  - Integrated into selfToolDefinitions()

### 5. ✅ HTTP API Endpoints
- **internal/api/a2a.go**: HTTP handlers
  - GET /.well-known/agent-card.json (Agent Card discovery)
  - POST /a2a/tasks (JSON-RPC 2.0 task endpoint)
  - GET /api/whoami (HTTP variant of self-discovery)

## Remaining Work (Wire-Up & Database)

### 6. 🔲 Database Schema & Migrations
**Status**: Schema types created, but migration SQL needed
**Files**: Need to create migration files in `migrations/`

```sql
-- a2a_tasks table
CREATE TABLE a2a_tasks (
  id TEXT PRIMARY KEY,
  target_kind TEXT NOT NULL, -- 'workflow' or 'instance'
  target_ref TEXT NOT NULL,  -- workflow name or instance URN
  message TEXT NOT NULL,
  durable_agent_instance_id TEXT,
  workflow_run_id TEXT,
  state TEXT NOT NULL,
  result TEXT,
  error TEXT,
  push_notification_config TEXT,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  FOREIGN KEY (durable_agent_instance_id) REFERENCES durable_agent_instances(id),
  FOREIGN KEY (workflow_run_id) REFERENCES workflow_runs(id)
);

CREATE INDEX idx_a2a_tasks_instance ON a2a_tasks(durable_agent_instance_id);
CREATE INDEX idx_a2a_tasks_workflow_run ON a2a_tasks(workflow_run_id);
CREATE INDEX idx_a2a_tasks_state ON a2a_tasks(state);

-- a2a_push_deliveries table
CREATE TABLE a2a_push_deliveries (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  target_state TEXT NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  next_retry TIMESTAMP,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  FOREIGN KEY (task_id) REFERENCES a2a_tasks(id)
);

CREATE INDEX idx_a2a_push_deliveries_task ON a2a_push_deliveries(task_id);
CREATE INDEX idx_a2a_push_deliveries_retry ON a2a_push_deliveries(next_retry) WHERE next_retry IS NOT NULL;
```

### 7. 🔲 Store Implementation
**Status**: Types defined, CRUD methods needed
**File**: `internal/store/a2a_tasks.go` (extend with methods)

Need to add:
- CreateA2ATask(task *A2ATask) error
- GetA2ATask(id string) (*A2ATask, error)
- UpdateA2ATask(task *A2ATask) error
- ListA2ATasks(filter ...) ([]*A2ATask, error)
- CreateA2APushDelivery(delivery *A2APushDelivery) error
- GetPendingPushDeliveries(now time.Time) ([]*A2APushDelivery, error)
- UpdateA2APushDelivery(delivery *A2APushDelivery) error

### 8. 🔲 Main Application Wire-Up
**Status**: Services created, need integration in cmd/nanite/main.go
**File**: `cmd/nanite/main.go`

Need to:
1. Instantiate AgentCardGenerator with workflow registry + boot profile registry
2. Instantiate A2ATaskService with workflow launcher + durable wake service
3. Register HTTP routes via api.RegisterA2ARoutes()
4. Add to container/dependency injection if using one

### 9. 🔲 Push Notification Worker
**Status**: Design complete, implementation needed
**File**: Create `internal/background/a2a_push_worker.go`

- Background ticker (similar to existing workers in main.go:1006-1073)
- Query pending deliveries from a2a_push_deliveries
- HTTP POST to PushNotificationConfig.URL on state transitions
- Bounded retries (e.g. 3 attempts with backoff)
- Best-effort delivery (matches Tether's /messages/notify)

### 10. 🔲 DurableAgentWakeExternalMessage Integration
**Status**: Constant exists, needs to be wired as real trigger
**File**: `internal/service/durable_wake.go`

Currently the enum value exists but is never constructed. A2ATaskService.routeToInstance() now uses it, making it real for the first time.

### 11. 🔲 WakePayload.Prompt Injection Fix
**Status**: Critical fix needed, not yet implemented
**File**: `internal/service/durable_wake.go` + session launch path

**Problem**: WakePayload.Prompt is captured but never becomes the launched session's first turn. It only feeds template placeholders in recipe UI.

**Fix needed**:
1. Trace where the launched/resumed session gets driven after Wake() returns
2. Inject WakePayload.Prompt as that session's first real user-turn message when present
3. This is a general wake-mechanism fix (benefits manual wakes too)

### 12. 🔲 Workflow Gate ↔ input-required Mapping
**Status**: Needs investigation & implementation
**Files**: `internal/service/workflow_engine.go`, task state refresh logic

- Map gate step pauses to TaskStateInputRequired
- Find/define gate resolution API (how to un-pause a gate)
- Wire A2A "continue task with input" to gate resolution

### 13. 🔲 Update internal/store/agents.go to Use a2a.GenerateAgentURN()
**Status**: Replacement functions created, old code not yet migrated
**File**: `internal/store/agents.go`

Replace:
- `generateAgentURN()` → `a2a.GenerateAgentURN()`
- `slugAliasURN(slug)` → `a2a.SlugAliasURN(slug)`
- Remove duplicate `urnPrefix` constant

This closes the import cycle workaround (FU-28).

## Testing Needs

### Unit Tests
- [ ] internal/a2a/address_test.go (URN generation, parsing)
- [ ] internal/a2a/jsonrpc_test.go (envelope construction)
- [ ] internal/service/a2a_agent_card_test.go (skill derivation)
- [ ] internal/service/a2a_task_test.go (routing, state derivation)
- [ ] internal/mcp/self_tools_whoami_test.go (address resolution)

### Integration Tests
- [ ] Agent Card generation end-to-end
- [ ] Task submission → workflow launch → state transitions
- [ ] Task submission → instance wake → completion
- [ ] Push notification delivery
- [ ] whoami MCP tool in live session

## Documentation Needs

### ADR Updates
- [ ] Document A2A adoption decision in docs/architecture/decisions/
- [ ] Reference zero-import discipline from ADR-0034 (Tether ACP precedent)

### Architecture Docs
- [ ] Update docs/architecture/a2a-protocol-design.md with final implementation notes
- [ ] Document any deviations from initial design

## Known Limitations (Explicit Non-Goals for This Pass)

1. **No outbound A2A client**: Nanite calling another agent's A2A endpoint (tracked separately)
2. **No extraction to libs/go-a2a yet**: Validate in Nanite first
3. **No go-messaging version bump**: Still on v0.2.1
4. **No streaming (SSE) support**: Matches current Hadron gap
5. **No auth-required TaskState producer**: Wire type exists, no code path produces it
6. **Coarse completion semantics for non-workflow tasks**: Only workflow-backed tasks get input-required state

## Next Immediate Steps

1. **Create database migrations** (step 6)
2. **Implement store CRUD methods** (step 7)
3. **Wire up in main.go** (step 8)
4. **Fix WakePayload.Prompt injection** (step 11) — critical for any of this to work
5. **Test Agent Card generation** manually via HTTP
6. **Test whoami tool** in a live session
7. **Implement push notification worker** (step 9)

## Estimated Remaining Work

- **Database + Store**: 1-2 hours
- **Main wire-up**: 30 minutes
- **WakePayload.Prompt fix**: 1-2 hours (requires code reading + testing)
- **Push worker**: 1 hour
- **Testing + polish**: 2-3 hours

**Total**: ~6-9 hours to full working implementation
