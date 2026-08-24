// Package scheduler is Nanite's own adapter around
// github.com/hollis-labs/go-scheduler — see
// docs/engineering/architecture/12-scheduling.md ("The Runner adapter and
// job taxonomy") for the design this package implements, and
// apps/hadron/internal/scheduler/adapter.go for the directly-transferable
// runnerAdapter/isDuplicateRun pattern this file follows.
//
// This file (runner_adapter.go) implements gosched.Runner: the single
// dispatch seam a fired go-scheduler schedule calls through. The matching
// gosched.Store adapter (TASKS/scheduling/02-store-adapter.md, mapping
// agent_schedules rows to the library's neutral Schedule type) is a
// separate, independently-dispatched task and lives in this same package
// under a different file — this file does not depend on it, and can be
// built/tested in isolation against fake gosched.Job values.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/loop"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// Job type tags. These are the exact gosched.Job.JobType strings
// RunnerAdapter.Enqueue switches on, and the exact strings 02's Store
// adapter must stamp onto gosched.Schedule.JobType per agent_schedules
// row so the two sides agree without either importing the other.
//
// JobTypeLoopRunTick is the fifth value, added by TASKS/loops/
// 12-loop-run-tick-scheduled-trigger.md -- docs/engineering/architecture/
// 21-loops.md's "Trigger surface" section: "a new loop_run_tick JobType...
// for a 'durable'-preset loop or a WAIT-status loop polling an external
// condition." internal/store's own ScheduleJobTypeLoopRunTick
// (agent_schedules.go) carries the identical string, independently
// declared -- see that constant's own doc comment for why this is a
// deliberate duplication, not a shared alias.
const (
	JobTypeDurableAgentWake = "durable_agent_wake"
	JobTypeAgentWorkflowRun = "agent_workflow_run"
	JobTypeCommandRun       = "command_run"
	JobTypeReflexDispatch   = "reflex_dispatch"
	JobTypeLoopRunTick      = "loop_run_tick"
)

// --- Payload shapes -------------------------------------------------------
//
// Each of the four gosched.Job.Payload JSON shapes below is the fixed
// contract 02-store-adapter.md's Store adapter must encode agent_schedules
// (plus whatever producer-specific fields a given row's own JSON blob
// carries) into when it builds a gosched.Schedule. See this task's Work
// Log (TASKS/scheduling/03-runner-adapter-and-job-taxonomy.md) for the
// per-field rationale — summarized here as doc comments so the shape and
// its justification stay next to the code that decodes it.

// DurableAgentWakePayload is job.Payload's JSON shape for
// JobTypeDurableAgentWake.
//
// InstanceID is a durable_agent_instances.id, NOT the agent_schedules.id
// or the agent_schedules.agent_id (which FKs to agent_profiles, not
// durable_agent_instances — see 071_agent_schedules.sql). Resolving which
// instance a given profile's schedule row wakes is a Store-adapter-time
// concern (02's job, mirroring how durable_wake.go's own RunDue/ListDue
// walk instances first and pull each one's profile-scoped schedules) —
// this Runner does no such resolution itself, it only trusts an
// already-resolved instance_id, matching Hadron's storeAdapter/
// runnerAdapter split (storeAdapter fully resolves domain data at
// Schedule-conversion time; runnerAdapter only decodes and dispatches).
type DurableAgentWakePayload struct {
	InstanceID string            `json:"instance_id"`
	ProjectID  string            `json:"project_id,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	Prompt     string            `json:"prompt,omitempty"`
	Facts      map[string]string `json:"facts,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// AgentWorkflowRunPayload is job.Payload's JSON shape for
// JobTypeAgentWorkflowRun — carries exactly what
// service.WorkflowLaunchRequest needs (workflow_launch.go:26-48).
type AgentWorkflowRunPayload struct {
	WorkflowName    string         `json:"workflow_name"`
	Params          map[string]any `json:"params,omitempty"`
	ProjectID       string         `json:"project_id,omitempty"`
	AgentProfileID  string         `json:"agent_profile_id"`
	ParentSessionID string         `json:"parent_session_id,omitempty"`
	TimeoutSeconds  int            `json:"timeout_seconds,omitempty"`
}

// CommandRunPayload is job.Payload's JSON shape for JobTypeCommandRun.
//
// Command names a registered tool (self-tool or MCP-backed — both route
// through the same service.ToolService.Execute call, this codebase's only
// existing generic "run a named command with arguments" entry point; see
// this file's package-level Work Log reference for why no separate raw-
// shell-command executor is used here). AgentID scopes which agent's tool
// registry/permission set Execute evaluates against, mirroring Execute's
// own required agentID parameter.
type CommandRunPayload struct {
	AgentID string         `json:"agent_id"`
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

// ReflexDispatchPayload is job.Payload's JSON shape for
// JobTypeReflexDispatch.
//
// ReflexID is an agent_reflexes.id — the same id-keyed identifier
// store.GetAgentReflex and internal/api/reflexes.go's CRUD handlers key on
// today (reflexes have no separate globally-unique name column). SessionID
// is optional: a reflex fired on a timer is not necessarily scoped to a
// live chat session, so this is left empty unless the producer that wrote
// the agent_schedules row had one to attach (e.g. a session-scoped
// self-schedule).
type ReflexDispatchPayload struct {
	ReflexID  string `json:"reflex_id"`
	SessionID string `json:"session_id,omitempty"`
}

// LoopRunTickPayload is job.Payload's JSON shape for JobTypeLoopRunTick --
// this planning session's decision (TASKS/loops/
// 12-loop-run-tick-scheduled-trigger.md's own Context), matching the other
// four payload structs' exact convention: one required identifier field, no
// omitempty fields needed since a tick has exactly one job -- re-checking
// one specific LoopRun's resume condition.
type LoopRunTickPayload struct {
	LoopRunID string `json:"loop_run_id"`
}

// --- Narrow dependency interfaces -----------------------------------------
//
// Every dependency RunnerAdapter needs is accepted as a narrow interface
// (or, for reflex_dispatch, a lookup func plus the existing
// *reflexes.Executor), never a full concrete service type — matching this
// codebase's own narrowing convention: internal/agent/reflexes/
// telemetry.go's TraceStore, and internal/agent/reflexes/resolve.go's
// ActionKindLookup/CooldownFunc.

// DurableAgentWaker is the narrow surface RunnerAdapter needs from
// service.DurableAgentWakeService for durable_agent_wake dispatch.
//
// Wake is the narrow single-instance entry point this Runner calls — NOT
// RunDue, which loops every due agent_schedules row across every managed
// instance in one pass (durable_wake.go's RunDue/ListDue). RunDue itself
// calls this exact same Wake method per due item today
// (durable_wake.go:186); go-scheduler's Engine now owns the "which
// schedule is due, claim it, dispatch it" loop RunDue used to run
// manually, so this Runner only ever needs to wake the one instance a
// single already-fired schedule names.
type DurableAgentWaker interface {
	Wake(ctx context.Context, instanceID string, req service.DurableAgentWakeRequest) (*service.DurableAgentWakeResult, error)
}

// WorkflowLauncher is the narrow surface RunnerAdapter needs from
// *service.WorkflowLauncher for agent_workflow_run dispatch.
type WorkflowLauncher interface {
	Launch(ctx context.Context, req service.WorkflowLaunchRequest) (*service.WorkflowLaunchResult, error)
}

// CommandExecutor is the narrow surface RunnerAdapter needs from
// service.ToolService for command_run dispatch.
type CommandExecutor interface {
	Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*service.ToolResult, error)
}

// ReflexLookup resolves one agent_reflexes row by ID — the narrow
// persistence surface reflex_dispatch needs to turn a
// ReflexDispatchPayload.ReflexID into the store.AgentReflex
// reflexes.Executor.Apply expects. store.Store.GetAgentReflex satisfies
// this today. A func type, not an interface, matching resolve.go's
// ActionKindLookup/CooldownFunc convention for a single-method narrowing.
type ReflexLookup func(ctx context.Context, id string) (*store.AgentReflex, error)

// LoopResumer is the narrow surface RunnerAdapter needs from
// *loop.LoopEngine for loop_run_tick dispatch -- just Resume, per this job
// type's own task instruction not to require the full LoopEngine surface
// when only Resume is called here. Signature matches
// *loop.LoopEngine.Resume exactly (internal/loop/engine.go, task 08,
// confirmed directly against the real code before writing this file, not
// assumed from the design doc's bare `Resume(ctx, loopRunID) error`
// paraphrase).
type LoopResumer interface {
	Resume(ctx context.Context, loopRunID string) (loop.LoopResult, error)
}

// LoopRunLookup resolves one loop_runs row by ID -- the narrow persistence
// surface loop_run_tick needs to check a LoopRun's current status *before*
// calling Resume (see enqueueLoopRunTick's own doc comment for the no-op
// guard this makes possible). A func type, not an interface, matching
// ReflexLookup's own convention immediately above for a single-method
// narrowing. store.Store.GetLoopRun satisfies this today.
type LoopRunLookup func(ctx context.Context, id string) (*store.LoopRun, error)

// RunnerAdapter implements gosched.Runner over Nanite's own dispatch
// targets, following Hadron's runnerAdapter pattern (adapter.go): decode
// Job.Payload per Job.JobType, dispatch to the matching narrow
// dependency, and translate any duplicate-run signal the dispatch target
// reports into gosched.ErrDuplicateJob.
//
// Every field is independently optional at construction: a nil dependency
// makes its job type return a clear "not configured" error rather than
// panicking on a nil interface call. This lets a caller wire up only the
// job types it actually needs live (e.g. a test wiring only Commands) and
// lets tests fake at the same field.
type RunnerAdapter struct {
	Wake           DurableAgentWaker
	Workflows      WorkflowLauncher
	Commands       CommandExecutor
	ReflexLookup   ReflexLookup
	ReflexExecutor *reflexes.Executor
	Loops          LoopResumer
	LoopRunLookup  LoopRunLookup
}

var _ gosched.Runner = (*RunnerAdapter)(nil)

// Enqueue dispatches a fired schedule's job to the matching target. See
// this file's package doc comment and TASKS/scheduling/
// 03-runner-adapter-and-job-taxonomy.md's Work Log for the per-job-type
// duplicate-run-signal finding: none of the four job types currently
// expose an "already running" signal on the dispatch-target side (see
// each enqueueX function's doc comment for why), so no gosched.
// ErrDuplicateJob translation happens in this version — there is
// currently nothing for such a translation to guard against. This is a
// documented finding, not an oversight; add the translation at the
// specific call site the day a real duplicate-run signal exists.
func (r *RunnerAdapter) Enqueue(ctx context.Context, job gosched.Job) error {
	switch job.JobType {
	case JobTypeDurableAgentWake:
		return r.enqueueDurableAgentWake(ctx, job)
	case JobTypeAgentWorkflowRun:
		return r.enqueueAgentWorkflowRun(ctx, job)
	case JobTypeCommandRun:
		return r.enqueueCommandRun(ctx, job)
	case JobTypeReflexDispatch:
		return r.enqueueReflexDispatch(ctx, job)
	case JobTypeLoopRunTick:
		return r.enqueueLoopRunTick(ctx, job)
	default:
		return fmt.Errorf("scheduler: unknown job type %q (run %s)", job.JobType, job.RunID)
	}
}

// enqueueDurableAgentWake decodes a DurableAgentWakePayload and calls
// DurableAgentWaker.Wake directly.
//
// Duplicate-run signal: none exists today. Wake's own "already active"
// case (wakeSkipReason, durable_wake.go:414-433) is not surfaced as an
// error at all — it returns (&DurableAgentWakeResult{Skipped: true,
// SkipReason: ...}, nil), a successful nil-error result. Treating that
// Skipped result as gosched.ErrDuplicateJob would be inventing a
// duplicate-run check where the real signal is a legitimate business skip
// (instance paused/archived/already-active-and-non-concurrent) that Wake
// itself already resolved to "nothing to do, not an error" — this
// function's nil-error return on a Skipped result matches Wake's own
// verdict rather than reinterpreting it.
func (r *RunnerAdapter) enqueueDurableAgentWake(ctx context.Context, job gosched.Job) error {
	if r.Wake == nil {
		return fmt.Errorf("scheduler: durable_agent_wake dispatch not configured (run %s)", job.RunID)
	}
	var payload DurableAgentWakePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("scheduler: decode durable_agent_wake payload (run %s): %w", job.RunID, err)
	}
	if payload.InstanceID == "" {
		return fmt.Errorf("scheduler: durable_agent_wake payload missing instance_id (run %s)", job.RunID)
	}
	_, err := r.Wake.Wake(ctx, payload.InstanceID, service.DurableAgentWakeRequest{
		ProjectID: payload.ProjectID,
		WakePayload: service.DurableAgentWakePayload{
			Reason:   payload.Reason,
			Prompt:   payload.Prompt,
			Facts:    payload.Facts,
			Metadata: payload.Metadata,
		},
	})
	if err != nil {
		return fmt.Errorf("scheduler: durable_agent_wake dispatch (run %s): %w", job.RunID, err)
	}
	return nil
}

// enqueueAgentWorkflowRun decodes an AgentWorkflowRunPayload and calls
// WorkflowLauncher.Launch directly.
//
// Duplicate-run signal: none exists today. Launch (workflow_launch.go:122)
// always creates a brand new durable_agent_instance with a fresh,
// ULID-suffixed slug (workflowInstanceSlug) on every call — there is no
// unique-constraint or "already running" condition it can hit for the
// same logical run, so there is nothing for an isDuplicateRun-style check
// to detect.
func (r *RunnerAdapter) enqueueAgentWorkflowRun(ctx context.Context, job gosched.Job) error {
	if r.Workflows == nil {
		return fmt.Errorf("scheduler: agent_workflow_run dispatch not configured (run %s)", job.RunID)
	}
	var payload AgentWorkflowRunPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("scheduler: decode agent_workflow_run payload (run %s): %w", job.RunID, err)
	}
	if payload.WorkflowName == "" {
		return fmt.Errorf("scheduler: agent_workflow_run payload missing workflow_name (run %s)", job.RunID)
	}
	if payload.AgentProfileID == "" {
		return fmt.Errorf("scheduler: agent_workflow_run payload missing agent_profile_id (run %s)", job.RunID)
	}
	_, err := r.Workflows.Launch(ctx, service.WorkflowLaunchRequest{
		WorkflowName:    payload.WorkflowName,
		Params:          payload.Params,
		ProjectID:       payload.ProjectID,
		AgentProfileID:  payload.AgentProfileID,
		ParentSessionID: payload.ParentSessionID,
		TimeoutSeconds:  payload.TimeoutSeconds,
	})
	if err != nil {
		return fmt.Errorf("scheduler: agent_workflow_run dispatch (run %s): %w", job.RunID, err)
	}
	return nil
}

// enqueueCommandRun decodes a CommandRunPayload and calls
// CommandExecutor.Execute directly.
//
// Duplicate-run signal: none exists today. ToolService.Execute (tool.go)
// is a single stateless call-and-return — it has no notion of "this
// command is already running" to detect or report.
//
// A tool-level failure (ToolResult.IsError, e.g. an unknown tool name or a
// permission denial) is surfaced here as a genuine Enqueue error rather
// than swallowed — Execute itself never returns a Go error for that case
// (it encodes failure into the returned ToolResult so a chat-turn caller
// can hand it back to the LLM), but a scheduled command_run has no LLM
// turn to hand a failure envelope to, so folding it into the Enqueue error
// is what makes the failure visible to go-scheduler's retry path (and,
// once TASKS/scheduling/04 lands, to the schedule_runs retry/backoff
// bookkeeping) instead of being silently discarded.
func (r *RunnerAdapter) enqueueCommandRun(ctx context.Context, job gosched.Job) error {
	if r.Commands == nil {
		return fmt.Errorf("scheduler: command_run dispatch not configured (run %s)", job.RunID)
	}
	var payload CommandRunPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("scheduler: decode command_run payload (run %s): %w", job.RunID, err)
	}
	if payload.Command == "" {
		return fmt.Errorf("scheduler: command_run payload missing command (run %s)", job.RunID)
	}
	result, err := r.Commands.Execute(ctx, payload.AgentID, payload.Command, payload.Args)
	if err != nil {
		return fmt.Errorf("scheduler: command_run %q dispatch (run %s): %w", payload.Command, job.RunID, err)
	}
	if result != nil && result.IsError {
		return fmt.Errorf("scheduler: command_run %q failed (run %s): %s", payload.Command, job.RunID, result.Output)
	}
	return nil
}

// enqueueReflexDispatch decodes a ReflexDispatchPayload, resolves the
// named reflex, and applies it directly via reflexes.Executor.Apply —
// the interim call documented in TASKS/scheduling/
// 03-runner-adapter-and-job-taxonomy.md's Work Log. This deliberately
// bypasses reflexes.Resolve/EvaluateTrigger entirely: a scheduled
// reflex_dispatch's trigger IS the schedule firing, so there is no
// predicate/event/interval condition left to re-evaluate against a
// State built from chat-turn signals the way the generic per-turn pass
// does. Whether this should instead call into the shared decision engine
// (10-reflex-action-taxonomy.md's Resolve()) once reflex_dispatch's own
// eventual trigger semantics are decided is explicitly undecided — see
// this file's Work Log reference and 12-scheduling.md's "What this
// session did not decide."
//
// A reflex whose status is not 'active' (paused/expired, e.g. paused
// after the agent_schedules row that names it was created) is treated as
// "nothing to apply" and returns nil, not an error — the same
// status='active' filter every other real caller of a reflex
// (ListAgentReflexesForAgent) already applies before considering a
// candidate at all.
//
// Duplicate-run signal: none exists today. Executor.Apply performs no
// row-claiming or uniqueness check of its own; go-scheduler's own CAS
// claim on the agent_schedules row is what already prevents two
// concurrent ticks from firing the same schedule twice.
func (r *RunnerAdapter) enqueueReflexDispatch(ctx context.Context, job gosched.Job) error {
	if r.ReflexLookup == nil || r.ReflexExecutor == nil {
		return fmt.Errorf("scheduler: reflex_dispatch not configured (run %s)", job.RunID)
	}
	var payload ReflexDispatchPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("scheduler: decode reflex_dispatch payload (run %s): %w", job.RunID, err)
	}
	if payload.ReflexID == "" {
		return fmt.Errorf("scheduler: reflex_dispatch payload missing reflex_id (run %s)", job.RunID)
	}
	reflex, err := r.ReflexLookup(ctx, payload.ReflexID)
	if err != nil {
		return fmt.Errorf("scheduler: reflex_dispatch lookup reflex %s (run %s): %w", payload.ReflexID, job.RunID, err)
	}
	if reflex == nil {
		return fmt.Errorf("scheduler: reflex_dispatch reflex %s not found (run %s)", payload.ReflexID, job.RunID)
	}
	if reflex.Status != store.ReflexStatusActive {
		return nil
	}
	state := reflexes.State{
		SessionID:  payload.SessionID,
		AgentID:    reflex.AgentID,
		AgentClass: reflex.ClassTag,
	}
	if _, err := r.ReflexExecutor.Apply(ctx, *reflex, state); err != nil {
		return fmt.Errorf("scheduler: reflex_dispatch apply reflex %s (run %s): %w", payload.ReflexID, job.RunID, err)
	}
	return nil
}

// enqueueLoopRunTick decodes a LoopRunTickPayload and calls
// LoopResumer.Resume directly -- TASKS/loops/
// 12-loop-run-tick-scheduled-trigger.md.
//
// Duplicate-run / stale-tick guard: unlike the other four job types (none
// of which have a real "already running" signal to guard against, per this
// file's own package doc comment), loop_run_tick's dispatch target
// (*loop.LoopEngine.Resume) is NOT safe to call unconditionally -- Resume's
// own switch (engine.go) returns a hard error for any LoopRun status other
// than waiting_on_gate/waiting_on_escalation. A tick firing against a
// LoopRun that has since completed on its own (e.g. a reflex or an operator
// already resumed it before this tick's own next_run arrived), or one still
// mid-iteration (running), would otherwise surface as a spurious dispatch
// error with nothing actually wrong. This task's own recommendation:
// "a duplicate tick against an already-non-WAITing LoopRun should be a
// cheap no-op, not an error" -- isResumableLoopRunStatus below is that
// guard, checked via LoopRunLookup before Resume is ever called. This is
// deliberately NOT the same thing as the go-scheduler-level
// ErrDuplicateJob translation this file's package doc comment says none of
// the five job types perform (go-scheduler's own CAS claim on the
// agent_schedules row already prevents two concurrent ticks from firing
// the same schedule row twice) -- this guard is a LoopRun-domain business
// rule (an already-resolved LoopRun has nothing left to tick), not a
// schedule-firing concurrency concern.
func (r *RunnerAdapter) enqueueLoopRunTick(ctx context.Context, job gosched.Job) error {
	if r.Loops == nil || r.LoopRunLookup == nil {
		return fmt.Errorf("scheduler: loop_run_tick dispatch not configured (run %s)", job.RunID)
	}
	var payload LoopRunTickPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("scheduler: decode loop_run_tick payload (run %s): %w", job.RunID, err)
	}
	if payload.LoopRunID == "" {
		return fmt.Errorf("scheduler: loop_run_tick payload missing loop_run_id (run %s)", job.RunID)
	}

	lr, err := r.LoopRunLookup(ctx, payload.LoopRunID)
	if err != nil {
		return fmt.Errorf("scheduler: loop_run_tick lookup loop_run %s (run %s): %w", payload.LoopRunID, job.RunID, err)
	}
	if lr == nil || !isResumableLoopRunStatus(lr.Status) {
		// Cheap no-op, not an error -- see this function's own doc comment.
		return nil
	}

	if _, err := r.Loops.Resume(ctx, payload.LoopRunID); err != nil {
		return fmt.Errorf("scheduler: loop_run_tick resume loop_run %s (run %s): %w", payload.LoopRunID, job.RunID, err)
	}
	return nil
}

// isResumableLoopRunStatus reports whether status is one of the two
// loop_runs.status values *loop.LoopEngine.Resume actually knows how to
// resume (engine.go's own switch) -- everything else (running, completed,
// failed, canceled) is "already terminal or not actually WAITing" per this
// task's own no-op guard wording, including LoopRunStatusRunning: a tick
// landing while another Resume/Run call is already mid-iteration for this
// same LoopRun has nothing useful to do either.
func isResumableLoopRunStatus(status string) bool {
	switch status {
	case store.LoopRunStatusWaitingOnGate, store.LoopRunStatusWaitingOnEscalation:
		return true
	default:
		return false
	}
}
