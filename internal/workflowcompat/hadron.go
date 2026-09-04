// Package workflowcompat projects Nanite's durable shared workflow host onto
// the pre-existing Agent Workflows HTTP and SSE contract. It is a migration
// adapter, not another workflow engine.
package workflowcompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
	workflowapi "github.com/hollis-labs/nanite/internal/workflowapi"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

const (
	defaultRunLimit   = 50
	defaultEventLimit = 128
)

var (
	// ErrNotFound identifies a run outside the requested durable projection.
	ErrNotFound = errors.New("durable workflow run not found")
	// ErrInvalidSource identifies source rejected before a durable run exists.
	ErrInvalidSource = errors.New("invalid durable workflow source")
	// ErrUnauthorized identifies a response that failed the persisted wait
	// capability or responder-authority checks.
	ErrUnauthorized = errors.New("workflow response is not authorized")
	// ErrConflict identifies a response that lost to an existing terminal wait.
	ErrConflict = errors.New("workflow response conflicts with durable state")
	// ErrCutoverNotReady prevents new shared launches until the fenced cutover
	// state reaches shared_only.
	ErrCutoverNotReady = errors.New("shared workflow cutover is not complete")
)

// CursorEvent carries one existing Agent Workflows event plus the durable
// SQLite cursor used for SSE reconnects.
type CursorEvent struct {
	Cursor int64
	Event  workflowapi.Event
}

// ExternalResponse is the authenticated provenance and idempotent payload
// supplied by the HTTP boundary for a callback or human approval.
type ExternalResponse struct {
	RunID          string
	StepID         string
	ResumeToken    string
	Payload        any
	Principal      string
	IdempotencyKey string
	ReceivedAt     time.Time
}

// Surface is the shared-engine migration seam consumed by the existing
// /api/workflows handlers.
type Surface interface {
	List(context.Context, string, string) ([]*workflowapi.RunRecord, error)
	Get(context.Context, string) (*workflowapi.RunRecord, error)
	ListLegacyTerminal(context.Context, string, string) ([]*workflowapi.RunRecord, error)
	GetLegacyTerminal(context.Context, string) (*workflowapi.RunRecord, error)
	RunSource(context.Context, string, []byte) (*workflowapi.RunRecord, error)
	Cancel(context.Context, string) (*workflowapi.RunRecord, error)
	ResumeCallback(context.Context, ExternalResponse) (*workflowapi.RunRecord, error)
	ResumeApproval(context.Context, ExternalResponse) (*workflowapi.RunRecord, error)
	CurrentEventCursor(context.Context, string) (int64, error)
	EventsAfter(context.Context, string, int64, int) ([]CursorEvent, int64, error)
}

// SharedSurface reads only committed durable state. Engine performs all
// workflow transitions; this adapter only starts/cancels it and translates its
// existing Nanite product projections.
type SharedSurface struct {
	product  *store.Store
	state    *workflowhost.WorkflowStateStore
	engine   *workflowhost.Engine
	executor agentworkflow.StepExecutor
}

// NewSharedSurface constructs the production compatibility adapter.
func NewSharedSurface(product *store.Store, state *workflowhost.WorkflowStateStore, engine *workflowhost.Engine, executor agentworkflow.StepExecutor) (*SharedSurface, error) {
	if product == nil || product.DB == nil || state == nil || engine == nil || executor == nil {
		return nil, errors.New("durable workflow compatibility surface requires store, state, engine, and executor")
	}
	return &SharedSurface{product: product, state: state, engine: engine, executor: executor}, nil
}

// HadronSurface is retained as a source-compatible alias for the pilot wiring.
// Deprecated: use SharedSurface.
type HadronSurface = SharedSurface

// NewHadronSurface retains source compatibility while cmd/nanite composition
// migrates to the extracted shared engine terminology.
// Deprecated: use NewSharedSurface.
func NewHadronSurface(product *store.Store, state *workflowhost.WorkflowStateStore, engine *workflowhost.Engine, executor agentworkflow.StepExecutor) (*SharedSurface, error) {
	return NewSharedSurface(product, state, engine, executor)
}

// List returns the newest durable shared and exact pilot runs through the
// existing response shape. The pilot identity remains readable during
// extraction; mixed or floating identity pairs fail closed. Waiting states
// intentionally project as running because that is the
// established closed RunStatus vocabulary.
func (h *SharedSurface) List(ctx context.Context, pipelineID, status string) ([]*workflowapi.RunRecord, error) {
	rows, err := h.product.DB.QueryContext(ctx, `
SELECT r.id
FROM workflow_runs r
JOIN workflow_plan_refs p ON p.digest = r.plan_digest
WHERE ((r.engine_kind = ? AND r.engine_contract_version = ?) OR
       (r.engine_kind = ? AND r.engine_contract_version = ?))
  AND (? = '' OR p.plan_id = ?)
  AND (? = '' OR CASE
      WHEN r.status IN ('waiting_on_gate','waiting_on_flex','waiting_on_loop') THEN 'running'
      ELSE r.status END = ?)
ORDER BY r.started_at DESC, r.id DESC
LIMIT ?`, store.WorkflowEngineIdentityShared.Kind, store.WorkflowEngineIdentityShared.ContractVersion,
		store.WorkflowEngineIdentityPilot.Kind, store.WorkflowEngineIdentityPilot.ContractVersion,
		pipelineID, pipelineID, status, status, defaultRunLimit)
	if err != nil {
		return nil, fmt.Errorf("list durable workflow runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan durable workflow run: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable workflow runs: %w", err)
	}

	result := make([]*workflowapi.RunRecord, 0, len(ids))
	for _, id := range ids {
		record, err := h.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

// Get projects one durable run, its product step rows, and its persisted
// lifecycle events into the existing Agent Workflows JSON types.
func (h *SharedSurface) Get(ctx context.Context, runID string) (*workflowapi.RunRecord, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, ErrNotFound
	}
	var identity store.WorkflowEngineIdentity
	if err := h.product.DB.QueryRowContext(ctx, `SELECT engine_kind, engine_contract_version FROM workflow_runs WHERE id = ?`, runID).Scan(&identity.Kind, &identity.ContractVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load durable workflow engine identity: %w", err)
	}
	if !store.SupportedEmbeddedWorkflowEngineIdentity(identity) {
		return nil, ErrNotFound
	}

	productRun, err := h.product.GetWorkflowRun(ctx, runID)
	if err != nil {
		if errors.Is(err, store.ErrWorkflowRunNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	runtimeRun, err := h.state.LoadRun(ctx, workflowruntime.RunID(runID))
	if err != nil {
		if errors.Is(err, workflowruntime.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	productSteps, err := h.product.ListWorkflowRunSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	attempts, err := h.attemptCounts(ctx, runID)
	if err != nil {
		return nil, err
	}

	stepStates := make(map[string]*workflowapi.StepState, len(productSteps))
	for _, step := range productSteps {
		stepStates[step.StepID] = productStepState(step, attempts[step.StepID])
	}

	events, err := h.runEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	return &workflowapi.RunRecord{
		Pipeline: workflowapi.PipelineInfo{
			ID: runtimeRun.Plan.ID, Name: productRun.DefinitionName,
			StepCount: len(productSteps),
		},
		Run: &workflowapi.RunState{
			PipelineID:  runtimeRun.Plan.ID,
			RunID:       runID,
			Status:      compatibilityRunStatus(productRun.Status),
			StepStates:  stepStates,
			StartedAt:   productRun.StartedAt,
			CompletedAt: productRun.CompletedAt,
			Error:       productRun.Error,
		},
		Events: events,
	}, nil
}

// RunSource executes graph-native source through the already-composed durable
// engine. A runtime failure with a persisted run remains an ordinary run
// response, matching the legacy endpoint's behavior.
func (h *SharedSurface) RunSource(ctx context.Context, locator string, source []byte) (*workflowapi.RunRecord, error) {
	if err := h.ensureSharedLaunchAllowed(ctx); err != nil {
		return nil, err
	}
	result, runErr := h.engine.RunSource(ctx, locator, source, agentworkflow.WorkflowInput{}, h.executor)
	if result.RunID == "" {
		if runErr == nil {
			runErr = errors.New("engine returned no durable run identity")
		}
		return nil, errors.Join(ErrInvalidSource, runErr)
	}
	record, err := h.Get(context.WithoutCancel(ctx), result.RunID)
	if err != nil {
		if runErr != nil {
			return nil, errors.Join(runErr, err)
		}
		return nil, err
	}
	return record, nil
}

// Cancel delegates lifecycle ownership to the durable engine and then reads
// the committed compatibility projection.
func (h *SharedSurface) Cancel(ctx context.Context, runID string) (*workflowapi.RunRecord, error) {
	_, err := h.engine.Cancel(ctx, runID, "canceled through Agent Workflows API")
	if err != nil {
		if errors.Is(err, workflowruntime.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return h.Get(context.WithoutCancel(ctx), runID)
}

// ListLegacyTerminal keeps historical Nanite-sequencer runs queryable without
// making the removed engine executable. Active legacy rows are intentionally
// omitted because they require an explicit audited cutover disposition.
func (h *SharedSurface) ListLegacyTerminal(ctx context.Context, pipelineID, status string) ([]*workflowapi.RunRecord, error) {
	rows, err := h.product.DB.QueryContext(ctx, `
SELECT id
FROM workflow_runs
WHERE engine_kind = ? AND engine_contract_version = ''
  AND status IN ('completed','failed','canceled')
  AND (? = '' OR definition_name = ?)
  AND (? = '' OR status = ?)
ORDER BY started_at DESC, id DESC
LIMIT ?`, store.WorkflowEngineIdentityLegacy.Kind, pipelineID, pipelineID, status, status, defaultRunLimit)
	if err != nil {
		return nil, fmt.Errorf("list terminal legacy workflow runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("scan terminal legacy workflow run: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate terminal legacy workflow runs: %w", rowsErr)
	}
	result := make([]*workflowapi.RunRecord, 0, len(ids))
	for _, id := range ids {
		record, getErr := h.GetLegacyTerminal(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		result = append(result, record)
	}
	return result, nil
}

// GetLegacyTerminal projects the surviving product rows only. It never
// manufactures runtime plan/event material for a legacy run.
func (h *SharedSurface) GetLegacyTerminal(ctx context.Context, runID string) (*workflowapi.RunRecord, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, ErrNotFound
	}
	var engineKind, engineVersion, status string
	if err := h.product.DB.QueryRowContext(ctx, `
SELECT engine_kind, engine_contract_version, status
FROM workflow_runs WHERE id = ?`, runID).Scan(&engineKind, &engineVersion, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load legacy workflow identity: %w", err)
	}
	if engineKind != store.WorkflowEngineIdentityLegacy.Kind || engineVersion != "" || !legacyTerminalStatus(status) {
		return nil, ErrNotFound
	}
	productRun, err := h.product.GetWorkflowRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	productSteps, err := h.product.ListWorkflowRunSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	stepStates := make(map[string]*workflowapi.StepState, len(productSteps))
	for _, step := range productSteps {
		stepStates[step.StepID] = productStepState(step, 0)
	}
	return &workflowapi.RunRecord{
		Pipeline: workflowapi.PipelineInfo{
			ID: productRun.DefinitionName, Name: productRun.DefinitionName,
			StepCount: len(productSteps),
		},
		Run: &workflowapi.RunState{
			PipelineID: productRun.DefinitionName, RunID: productRun.ID,
			Status: compatibilityRunStatus(productRun.Status), StepStates: stepStates,
			StartedAt: productRun.StartedAt, CompletedAt: productRun.CompletedAt,
			Error: productRun.Error,
		},
		Events: []workflowapi.Event{},
	}, nil
}

// ResumeCallback delegates the authenticated principal and exact idempotency
// identity to the host's callback-specific authorization path.
func (h *SharedSurface) ResumeCallback(ctx context.Context, response ExternalResponse) (*workflowapi.RunRecord, error) {
	result, err := h.engine.ResumeCallback(ctx, workflowhost.CallbackResumeRequest{
		RunID: response.RunID, StepID: response.StepID, Token: response.ResumeToken,
		Payload: response.Payload, AuthenticatedKind: "callback",
		AuthenticatedPrincipal: response.Principal, IdempotencyKey: response.IdempotencyKey,
		ReceivedAt: response.ReceivedAt,
	}, h.executor)
	if err != nil {
		return nil, classifyExternalResponseError(err)
	}
	return h.Get(context.WithoutCancel(ctx), result.RunID)
}

// ResumeApproval resolves a gate wait. The HTTP boundary authenticates the
// Basic Auth principal; this adapter finds the exact persisted wait rather
// than resolving mutable definition state.
func (h *SharedSurface) ResumeApproval(ctx context.Context, response ExternalResponse) (*workflowapi.RunRecord, error) {
	var waitID string
	err := h.product.DB.QueryRowContext(ctx, `
SELECT w.wait_id
FROM workflow_waits w
JOIN workflow_runs r ON r.id = w.run_id
JOIN workflow_plan_node_projections p
  ON p.plan_digest = r.plan_digest AND p.node_id = w.node_id
WHERE w.run_id = ? AND p.product_step_id = ?
  AND json_extract(w.record_json, '$.kind') = 'gate'
  AND ((r.engine_kind = ? AND r.engine_contract_version = ?) OR
       (r.engine_kind = ? AND r.engine_contract_version = ?))
ORDER BY w.created_at DESC, w.wait_id DESC
LIMIT 1`, response.RunID, response.StepID,
		store.WorkflowEngineIdentityShared.Kind, store.WorkflowEngineIdentityShared.ContractVersion,
		store.WorkflowEngineIdentityPilot.Kind, store.WorkflowEngineIdentityPilot.ContractVersion).Scan(&waitID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load durable approval wait: %w", err)
	}
	result, err := h.engine.ResumeWait(ctx, workflowhost.ResumeWaitRequest{
		WaitID: waitID, Token: response.ResumeToken, Payload: response.Payload,
		ResponderKind: "operator", ResponderReference: response.Principal,
		IdempotencyKey: response.IdempotencyKey, ReceivedAt: response.ReceivedAt,
	}, h.executor)
	if err != nil {
		return nil, classifyExternalResponseError(err)
	}
	return h.Get(context.WithoutCancel(ctx), result.RunID)
}

func (h *SharedSurface) ensureSharedLaunchAllowed(ctx context.Context) error {
	state, err := h.product.LoadWorkflowCutoverState(ctx)
	if errors.Is(err, store.ErrWorkflowCutoverNotFound) {
		return ErrCutoverNotReady
	}
	if err != nil {
		return fmt.Errorf("load shared workflow cutover: %w", err)
	}
	if !state.AllowsSharedLaunch() {
		return fmt.Errorf("%w: phase=%s generation=%d", ErrCutoverNotReady, state.Phase, state.Generation)
	}
	return nil
}

func classifyExternalResponseError(err error) error {
	switch {
	case errors.Is(err, workflowhost.ErrWorkflowWaitUnauthorized), errors.Is(err, workflowruntime.ErrInvalidResumeToken):
		return fmt.Errorf("%w: %w", ErrUnauthorized, err)
	case errors.Is(err, workflowruntime.ErrNotFound), errors.Is(err, store.ErrWorkflowRunNotFound):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case errors.Is(err, workflowruntime.ErrWaitClosed):
		return fmt.Errorf("%w: %w", ErrConflict, err)
	default:
		return err
	}
}

// CurrentEventCursor returns the committed SQLite rowid high-water mark. The
// SSE handler captures it before opening a future-only live stream.
func (h *SharedSurface) CurrentEventCursor(ctx context.Context, runID string) (int64, error) {
	var cursor int64
	err := h.product.DB.QueryRowContext(ctx, `
SELECT COALESCE(MAX(e.rowid), 0)
FROM workflow_events e
JOIN workflow_runs r ON r.id = e.run_id
WHERE ((r.engine_kind = ? AND r.engine_contract_version = ?) OR
       (r.engine_kind = ? AND r.engine_contract_version = ?))
  AND (? = '' OR e.run_id = ?)`,
		store.WorkflowEngineIdentityShared.Kind, store.WorkflowEngineIdentityShared.ContractVersion,
		store.WorkflowEngineIdentityPilot.Kind, store.WorkflowEngineIdentityPilot.ContractVersion,
		runID, runID).Scan(&cursor)
	if err != nil {
		return 0, fmt.Errorf("load durable workflow event cursor: %w", err)
	}
	return cursor, nil
}

// EventsAfter tails committed append-only workflow events. nextCursor always
// advances across unprojected engine-internal events so polling cannot spin on
// a record that intentionally has no legacy equivalent.
func (h *SharedSurface) EventsAfter(ctx context.Context, runID string, after int64, limit int) ([]CursorEvent, int64, error) {
	if after < 0 {
		return nil, after, errors.New("durable workflow event cursor must be non-negative")
	}
	if limit <= 0 {
		limit = defaultEventLimit
	}
	rows, err := h.product.DB.QueryContext(ctx, `
SELECT e.rowid, e.sequence, p.plan_id, e.run_id,
       COALESCE(np.product_step_id,
                json_extract(e.invocation_json, '$.node_id'),
                json_extract(e.attempt_json, '$.invocation.node_id'), ''),
       e.event_type, e.occurred_at, COALESCE(e.attributes_json, '{}')
FROM workflow_events e
JOIN workflow_runs r ON r.id = e.run_id
JOIN workflow_plan_refs p ON p.digest = r.plan_digest
LEFT JOIN workflow_plan_node_projections np
  ON np.plan_digest = r.plan_digest
 AND np.node_id = COALESCE(json_extract(e.invocation_json, '$.node_id'),
                           json_extract(e.attempt_json, '$.invocation.node_id'))
WHERE ((r.engine_kind = ? AND r.engine_contract_version = ?) OR
       (r.engine_kind = ? AND r.engine_contract_version = ?))
  AND e.rowid > ? AND (? = '' OR e.run_id = ?)
ORDER BY e.rowid
LIMIT ?`, store.WorkflowEngineIdentityShared.Kind, store.WorkflowEngineIdentityShared.ContractVersion,
		store.WorkflowEngineIdentityPilot.Kind, store.WorkflowEngineIdentityPilot.ContractVersion,
		after, runID, runID, limit)
	if err != nil {
		return nil, after, fmt.Errorf("tail durable workflow events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	next := after
	result := make([]CursorEvent, 0)
	for rows.Next() {
		var (
			cursor, sequence             int64
			pipelineID, eventRun, stepID string
			eventType, occurredAt        string
			attributesJSON               string
		)
		if err := rows.Scan(&cursor, &sequence, &pipelineID, &eventRun, &stepID, &eventType, &occurredAt, &attributesJSON); err != nil {
			return nil, next, fmt.Errorf("scan durable workflow event: %w", err)
		}
		next = cursor
		attributes := make(map[string]string)
		if err := json.Unmarshal([]byte(attributesJSON), &attributes); err != nil {
			return nil, next, fmt.Errorf("decode durable workflow event attributes: %w", err)
		}
		timestamp, err := time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return nil, next, fmt.Errorf("decode durable workflow event timestamp: %w", err)
		}
		projectedType, ok := compatibilityEventType(eventType, attributes)
		if !ok {
			continue
		}
		data := make(map[string]any, len(attributes)+2)
		for key, value := range attributes {
			data[key] = value
		}
		data["durable_event_type"] = eventType
		data["sequence"] = sequence
		result = append(result, CursorEvent{Cursor: cursor, Event: workflowapi.Event{
			Type: projectedType, PipelineID: pipelineID, RunID: eventRun,
			StepID: stepID, Data: data, Timestamp: timestamp,
		}})
	}
	if err := rows.Err(); err != nil {
		return nil, next, fmt.Errorf("iterate durable workflow events: %w", err)
	}
	return result, next, nil
}

func (h *SharedSurface) attemptCounts(ctx context.Context, runID string) (map[string]int, error) {
	rows, err := h.product.DB.QueryContext(ctx, `
SELECT COALESCE(p.product_step_id, n.node_id), MAX(n.latest_attempt)
FROM workflow_node_invocations n
JOIN workflow_runs r ON r.id = n.run_id
LEFT JOIN workflow_plan_node_projections p
  ON p.plan_digest = r.plan_digest AND p.node_id = n.node_id
WHERE n.run_id = ? AND n.iteration = '' AND n.phase = ''
GROUP BY COALESCE(p.product_step_id, n.node_id)`, runID)
	if err != nil {
		return nil, fmt.Errorf("load durable workflow attempt counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]int)
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, fmt.Errorf("scan durable workflow attempt count: %w", err)
		}
		result[id] = count
	}
	return result, rows.Err()
}

func (h *SharedSurface) runEvents(ctx context.Context, runID string) ([]workflowapi.Event, error) {
	cursor := int64(0)
	result := make([]workflowapi.Event, 0)
	for {
		events, next, err := h.EventsAfter(ctx, runID, cursor, defaultEventLimit)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			result = append(result, event.Event)
		}
		if next == cursor {
			return result, nil
		}
		cursor = next
	}
}

func compatibilityRunStatus(status string) workflowapi.RunStatus {
	switch status {
	case "waiting_on_gate", "waiting_on_flex", "waiting_on_loop":
		return workflowapi.RunRunning
	case "completed":
		return workflowapi.RunCompleted
	case "failed":
		return workflowapi.RunFailed
	case "canceled":
		return workflowapi.RunCanceled
	case "pending":
		return workflowapi.RunPending
	default:
		return workflowapi.RunRunning
	}
}

func compatibilityStepStatus(status string) workflowapi.StepStatus {
	switch status {
	case "waiting_on_gate", "waiting_on_flex", "waiting_on_loop":
		return workflowapi.StepRunning
	case "completed":
		return workflowapi.StepCompleted
	case "failed":
		return workflowapi.StepFailed
	case "skipped":
		return workflowapi.StepSkipped
	case "canceled":
		return workflowapi.StepCanceled
	case "running":
		return workflowapi.StepRunning
	default:
		return workflowapi.StepPending
	}
}

func legacyTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "canceled":
		return true
	default:
		return false
	}
}

func productStepState(step *store.WorkflowRunStepRow, attempts int) *workflowapi.StepState {
	state := &workflowapi.StepState{
		StepID: step.StepID, Status: compatibilityStepStatus(step.Status), Attempts: attempts,
		StartedAt: step.StartedAt, CompletedAt: step.CompletedAt, Error: step.Error,
	}
	if state.Status == workflowapi.StepSkipped {
		state.SkipReason = step.Error
	}
	if step.Output != "" || step.Error != "" || step.IsError {
		state.Output = compatibilityStepOutput(step)
	}
	return state
}

func compatibilityStepOutput(step *store.WorkflowRunStepRow) *workflowapi.StepOutput {
	data := map[string]any{"output": step.Output, "is_error": step.IsError}
	if step.ToolCallsJSON != "" {
		var calls any
		if json.Unmarshal([]byte(step.ToolCallsJSON), &calls) == nil {
			data["tool_calls"] = calls
		}
	}
	if step.VerifyJSON != "" {
		var verify any
		if json.Unmarshal([]byte(step.VerifyJSON), &verify) == nil {
			data["verify"] = verify
		}
	}
	output := &workflowapi.StepOutput{Data: data, Stdout: step.Output}
	if step.IsError || step.Error != "" {
		output.Stderr = step.Error
		output.ExitCode = 1
	}
	return output
}

func compatibilityEventType(eventType string, attributes map[string]string) (string, bool) {
	switch eventType {
	case workflowruntime.EventRunStatusChanged:
		switch attributes["to_status"] {
		case string(workflowruntime.RunRunning):
			return "pipeline.started", true
		case string(workflowruntime.RunSucceeded):
			return "pipeline.completed", true
		case string(workflowruntime.RunFailed), string(workflowruntime.RunTimedOut), string(workflowruntime.RunCrashed):
			return "pipeline.failed", true
		case string(workflowruntime.RunCanceled):
			return "pipeline.canceled", true
		}
	case workflowruntime.EventNodeStatusChanged:
		switch attributes["to_status"] {
		case string(workflowruntime.NodeRunning):
			return "step.started", true
		case string(workflowruntime.NodeSucceeded):
			return "step.completed", true
		case string(workflowruntime.NodeFailed), string(workflowruntime.NodeTimedOut), string(workflowruntime.NodeCrashed):
			return "step.failed", true
		case string(workflowruntime.NodeSkipped):
			return "step.skipped", true
		case string(workflowruntime.NodeCanceled):
			return "step.canceled", true
		}
	case workflowruntime.EventNodeAttemptStarted:
		return "step.started", true
	case workflowruntime.EventNodeAttemptFinished:
		switch attributes["attempt_status"] {
		case string(workflowruntime.NodeSucceeded):
			return "step.completed", true
		case string(workflowruntime.NodeFailed), string(workflowruntime.NodeTimedOut), string(workflowruntime.NodeCrashed):
			return "step.failed", true
		case string(workflowruntime.NodeCanceled):
			return "step.canceled", true
		}
	}
	return "", false
}

// ParseEventCursor validates the decimal cursor syntax shared by the query
// and Last-Event-ID HTTP seams.
func ParseEventCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, fmt.Errorf("invalid durable workflow event cursor %q", value)
	}
	return cursor, nil
}
