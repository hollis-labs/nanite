package store

// FU-30 Phase 3 Stage 3.c — store accessors for agent_reflexes and
// pending_reflexes. Reflexes absorb the FU-21 drift detectors and extend
// to general predicate-AND/OR + event + interval triggers; see
// internal/agent/reflexes for the evaluator + executor.

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrAgentReflexNotFound is returned when an agent_reflexes row cannot
// be located.
var ErrAgentReflexNotFound = errors.New("agent reflex not found")

// ErrPendingReflexNotFound is returned when a pending_reflexes row
// cannot be located.
var ErrPendingReflexNotFound = errors.New("pending reflex not found")

// Reflex trigger / action / status constants. The CHECK constraints on
// the underlying tables keep DB rows aligned with these values; the Go
// layer mirrors them so callers can reference constants instead of
// stringly-typed literals.
const (
	ReflexTriggerPredicate = "predicate"
	ReflexTriggerEvent     = "event"
	ReflexTriggerInterval  = "interval"

	ReflexActionInjectReminder  = "inject_reminder"
	ReflexActionForceToolChoice = "force_tool_choice"
	ReflexActionSendMessage     = "send_message"
	ReflexActionHaltSession     = "halt_session"
	ReflexActionAddSchedule     = "add_schedule"
	// ReflexActionDispatchToAgent (Phase 4 item 02,
	// TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md)
	// routes the current turn to a different agent profile — the reflex
	// absorption of the retired agent-broker's real intent (architecture
	// doc 03-steering.md, "Reflexes are the single steering primitive").
	// action_spec shape: {"agent_slug": string (required), "confidence":
	// number (optional, [0,1]), "reason": string (optional, defaults to
	// "reflex:"+name)}. See internal/service/chat_reflex_dispatch.go for
	// the executor call site and internal/agent/reflexes/evaluator.go's
	// scope_tier/execution_pattern predicate kinds for the trigger shape
	// the migrated Rule 5 seed uses.
	ReflexActionDispatchToAgent = "dispatch_to_agent"
	// ReflexActionResumeLoopRun (TASKS/loops/11-loop-event-predicate-trigger.md)
	// resumes a specific, WAIT-parked LoopRun (internal/loop) when its
	// event/predicate trigger fires — the "Event/predicate" trigger
	// surface docs/engineering/architecture/21-loops.md names, realized
	// as a real, named action kind rather than a generic `callback`
	// (docs/engineering/architecture/10-reflex-action-taxonomy.md:120
	// explicitly rejects that shape). action_spec shape: {"loop_run_id":
	// string (required)} — a reflex row scoped to resuming exactly one
	// LoopRun, created by the loop runtime (not authored ad hoc by a
	// human the way most reflexes are) when that LoopRun transitions to
	// loop_runs.status = 'waiting_on_escalation' with an event/predicate
	// resume condition. Its own trigger_kind/trigger_spec columns reuse
	// the existing predicate/event/interval AST unchanged
	// (internal/agent/reflexes/evaluator.go's EvaluateTrigger). See
	// internal/agent/reflexes/executor.go's Apply (documented no-op, same
	// shape as ReflexActionDispatchToAgent — the real effect needs
	// internal/loop, which this package deliberately does not depend on)
	// and internal/service/loop_resume_reflex.go's
	// EvaluateLoopRunResumeReflexes for the real handler + evaluation
	// entry point.
	ReflexActionResumeLoopRun = "resume_loop_run"

	ReflexStatusActive  = "active"
	ReflexStatusPaused  = "paused"
	ReflexStatusExpired = "expired"

	PendingReflexStatusPending  = "pending"
	PendingReflexStatusApproved = "approved"
	PendingReflexStatusRejected = "rejected"
)

// AgentReflex is one row in the agent_reflexes table. agent_id may be
// empty: when so, ClassTag binds the reflex to every agent of that
// class (base reflex seeds). Reflexes targeted at a specific agent set
// agent_id non-empty and leave ClassTag empty.
//
// OptOutAllowed (Phase 1 item 07,
// TASKS/phase-1/07-add-reflex-opt-out-field.md) distinguishes
// "required, cannot opt out" (false) from "default-on, agent may opt
// out" (true, the default for every pre-existing row). It is consulted
// by ListAgentReflexesForAgent alongside the agent_reflex_opt_outs
// table: false means the reflex applies unconditionally no matter what
// agent_reflex_opt_outs contains; true means an opt-out row for
// (agent_id, this reflex's id) suppresses it for that agent.
//
// ProvenanceTier and RecurrenceOverrideSeconds (Reflex Action Taxonomy
// Facets 3/4, docs/engineering/architecture/10-reflex-action-taxonomy.md,
// TASKS/reflex-taxonomy/01-taxonomy-schema-foundation.md) were added by
// migration 124_reflex_action_taxonomy.sql. ProvenanceTier is immutable
// after creation (like CreatedBy, which it's derived from) — a real
// FK-backed authority tier (system/operator/plugin, reflex_provenance_tiers)
// intended for a future per-kind allow-list gating which tiers may declare
// which action kinds; that gate is not built by this task.
// RecurrenceOverrideSeconds is the most-specific level of the recurrence
// cascade (system default -> reflex_action_kinds.default_recurrence_seconds
// -> this field); nil means "inherit the kind-level default." Reading and
// applying that cascade is TASKS/reflex-taxonomy/02-recurrence-cascade.md's
// job, not this one — this task only adds the column and exposes it here.
//
// WorkflowRunID (TASKS/teams/05-agent-reflexes-run-scoping.md, migration
// 131_agent_reflexes_workflow_run_scoping.sql) is the third scoping
// dimension docs/engineering/architecture/15-teams.md's "Routing: real
// reuse, and one real gap" section names: AgentID/ClassTag alone can only
// express "global/class-bound" or "bound to one specific agent" — there
// was no way to say "this reflex exists only for the lifetime of this
// TeamRun." Empty (the value of every pre-existing row, and every row
// created by any path that doesn't set it) means global/class-bound,
// exactly as before this column existed. A non-empty value scopes the row
// to one specific workflow_runs.id (a TeamRun — the design doc's "TeamRun
// IS a WorkflowRun" decision, docs/engineering/architecture/15-teams.md's
// "Decision 2"). Immutable after creation, same rationale as AgentID/
// ClassTag above: it is a scope binding, not a tunable knob — deliberately
// absent from UpdateAgentReflex's SET clause. See
// ListAgentReflexesForWorkflowRun (below) for the read-side query this
// column feeds, and internal/service/chat_reflex_dispatch.go's
// attemptReflexDispatch / internal/selftools/self_tools_dispatch.go's
// matchDispatchToAgentReflex for the two call sites that combine a run's
// scoped candidates with ListAgentReflexesForAgent's existing global set.
type AgentReflex struct {
	ID                        string `json:"id"`
	AgentID                   string `json:"agent_id"`
	ClassTag                  string `json:"class_tag"`
	Name                      string `json:"name"`
	TriggerKind               string `json:"trigger_kind"`
	TriggerSpec               string `json:"trigger_spec"`
	ActionKind                string `json:"action_kind"`
	ActionSpec                string `json:"action_spec"`
	Status                    string `json:"status"`
	Priority                  int64  `json:"priority"`
	FiredCount                int64  `json:"fired_count"`
	LastFiredAt               string `json:"last_fired_at"`
	CreatedAt                 string `json:"created_at"`
	CreatedBy                 string `json:"created_by"`
	OptOutAllowed             bool   `json:"opt_out_allowed"`
	ProvenanceTier            string `json:"provenance_tier"`
	RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds"`
	WorkflowRunID             string `json:"workflow_run_id"`
}

// PendingReflex is one row in the pending_reflexes table. The
// agent_reflex_propose self-tool inserts these; the operator review
// API approves to agent_reflexes or rejects with a reason.
type PendingReflex struct {
	ID            string `json:"id"`
	ProposedBy    string `json:"proposed_by"`
	ProposedAt    string `json:"proposed_at"`
	TargetAgentID string `json:"target_agent_id"`
	Name          string `json:"name"`
	TriggerKind   string `json:"trigger_kind"`
	TriggerSpec   string `json:"trigger_spec"`
	ActionKind    string `json:"action_kind"`
	ActionSpec    string `json:"action_spec"`
	Rationale     string `json:"rationale"`
	Status        string `json:"status"`
	ReviewedAt    string `json:"reviewed_at"`
	ReviewedBy    string `json:"reviewed_by"`
}

const agentReflexColumns = `id, COALESCE(agent_id,''), COALESCE(class_tag,''), name,
       trigger_kind, trigger_spec, action_kind, action_spec,
       status, priority, fired_count, COALESCE(last_fired_at,''),
       created_at, created_by, opt_out_allowed,
       provenance_tier, recurrence_override_seconds,
       COALESCE(workflow_run_id,'')`

func scanAgentReflex(scanner interface{ Scan(...any) error }, r *AgentReflex) error {
	var recurrenceOverride sql.NullInt64
	if err := scanner.Scan(
		&r.ID, &r.AgentID, &r.ClassTag, &r.Name,
		&r.TriggerKind, &r.TriggerSpec, &r.ActionKind, &r.ActionSpec,
		&r.Status, &r.Priority, &r.FiredCount, &r.LastFiredAt,
		&r.CreatedAt, &r.CreatedBy, &r.OptOutAllowed,
		&r.ProvenanceTier, &recurrenceOverride,
		&r.WorkflowRunID,
	); err != nil {
		return err
	}
	r.RecurrenceOverrideSeconds = nil
	if recurrenceOverride.Valid {
		v := recurrenceOverride.Int64
		r.RecurrenceOverrideSeconds = &v
	}
	return nil
}

// nullIfNilInt64 returns nil (binds SQL NULL) if v is nil, otherwise the
// dereferenced value. The *int64-nullable-column counterpart to
// nullIfEmpty (sessions.go), used for recurrence_override_seconds.
func nullIfNilInt64(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

const pendingReflexColumns = `id, proposed_by, proposed_at,
       COALESCE(target_agent_id,''), name,
       trigger_kind, trigger_spec, action_kind, action_spec,
       rationale, status, COALESCE(reviewed_at,''), COALESCE(reviewed_by,'')`

func scanPendingReflex(scanner interface{ Scan(...any) error }, r *PendingReflex) error {
	return scanner.Scan(
		&r.ID, &r.ProposedBy, &r.ProposedAt,
		&r.TargetAgentID, &r.Name,
		&r.TriggerKind, &r.TriggerSpec, &r.ActionKind, &r.ActionSpec,
		&r.Rationale, &r.Status, &r.ReviewedAt, &r.ReviewedBy,
	)
}

// InsertAgentReflex inserts a new agent_reflexes row. If row.ID is
// empty, a ULID is generated. The row replaces an existing one on PK
// conflict (idempotent re-seed semantics).
func (s *Store) InsertAgentReflex(ctx context.Context, row AgentReflex) (string, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return "", ErrImmutableAgentProfile
}

// InsertAgentReflexIfAbsent inserts a caller-keyed reflex without replacing an
// existing row. Replays preserve fired_count/last_fired_at; a deterministic-ID
// collision with different immutable routing material fails closed.
func (s *Store) InsertAgentReflexIfAbsent(ctx context.Context, row AgentReflex) (bool, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return false, ErrImmutableAgentProfile
}

// GetAgentReflex returns the row by ID, or ErrAgentReflexNotFound.
func (s *Store) GetAgentReflex(ctx context.Context, id string) (*AgentReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// ListAgentReflexesForAgent returns the combined set of:
//   - class-bound base reflexes whose class_tag matches classTag and
//     agent_id IS NULL (the seeded defaults) — EXCEPT those the agent
//     has opted out of via agent_reflex_opt_outs, and then only when
//     the reflex's own opt_out_allowed is true. A reflex with
//     opt_out_allowed=false always applies, regardless of any opt-out
//     row (Phase 1 item 07's "required, cannot opt out" contract).
//   - agent-specific overrides whose agent_id matches agentID
//
// Both filtered to status='active'. Results ordered by priority DESC
// then created_at ASC so the evaluator processes higher-priority
// reflexes first.
func (s *Store) ListAgentReflexesForAgent(ctx context.Context, agentID, classTag string) ([]AgentReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// ListAgentReflexesForWorkflowRun returns the run-scoped counterpart of
// ListAgentReflexesForAgent — TASKS/teams/05-agent-reflexes-run-scoping.md,
// the "third scoping dimension" docs/engineering/architecture/
// 15-teams.md's "Routing: real reuse, and one real gap" section names.
// Same class-bound/opt-out/agent-specific filtering as
// ListAgentReflexesForAgent, with the additional constraint
// workflow_run_id = runID — i.e. rows deliberately scoped to one specific
// TeamRun (a workflow_runs.id), not the global (workflow_run_id IS NULL)
// set ListAgentReflexesForAgent already returns.
//
// Deliberately a separate query rather than a parameter added to
// ListAgentReflexesForAgent: ListAgentReflexesForAgent's existing callers
// (including Engine.EvaluateState's generic per-turn pass,
// internal/agent/reflexes/engine.go) have no run-context of their own and
// must keep seeing exactly the global set they always have — narrowing
// that shared query would be a behavior change for every caller, not just
// the two dispatch_to_agent call sites that actually need run-scoping
// (internal/service/chat_reflex_dispatch.go's attemptReflexDispatch,
// internal/selftools/self_tools_dispatch.go's matchDispatchToAgentReflex).
// Both of those combine this method's result with
// ListAgentReflexesForAgent's own (global rules still apply inside a
// run; run-scoped rules layer on top, they do not replace the global
// set) — see each call site's own comment for the merge and the
// documented global-vs-run-scoped priority-ordering call.
//
// Returns an empty slice, no error, when runID is empty (no run context
// to scope against — the common case for a non-Team session).
func (s *Store) ListAgentReflexesForWorkflowRun(ctx context.Context, runID, agentID, classTag string) ([]AgentReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// ListAgentReflexesForLoopRun returns the active resume_loop_run reflex
// row(s) scoped to loopRunID — TASKS/loops/11-loop-event-predicate-trigger.md.
// Unlike ListAgentReflexesForWorkflowRun's workflow_run_id column
// (migration 131), a resume_loop_run reflex is scoped via its own
// action_spec JSON ({"loop_run_id": "<id>"}), not a dedicated column —
// LoopRun is a peer entity to WorkflowRun (docs/engineering/architecture/
// 21-loops.md's Decision 1), so workflow_run_id's FK to workflow_runs(id)
// does not fit a loop_runs.id value, and this task's own scope adds no
// new agent_reflexes column for it (only the new action kind — see
// ReflexActionResumeLoopRun's own doc comment). Filters to
// action_kind = 'resume_loop_run' unconditionally — a caller resuming one
// specific LoopRun only ever cares about that one kind, unlike the
// broader agent/class-bound candidate sets ListAgentReflexesForAgent/
// ListAgentReflexesForWorkflowRun return. Relies on SQLite's built-in
// json_extract (core since SQLite 3.38, well below modernc.org/sqlite's
// bundled version) — no other query in this file needed it before this
// task.
func (s *Store) ListAgentReflexesForLoopRun(ctx context.Context, loopRunID string) ([]AgentReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// ListAllAgentReflexes returns every row in agent_reflexes ordered by
// priority DESC, created_at ASC. Used by the operator surface.
func (s *Store) ListAllAgentReflexes(ctx context.Context, agentID string) ([]AgentReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// UpdateAgentReflex updates an existing row's editable fields. Scope
// bindings (agent_id/class_tag), created_at, and created_by are immutable.
// ProvenanceTier is likewise immutable — same rationale as CreatedBy, which
// it's derived from: it is a security-relevant authority marker set at
// creation, not an operator-tunable knob (Facet 3,
// docs/engineering/architecture/10-reflex-action-taxonomy.md). It is
// deliberately absent from this SET clause. RecurrenceOverrideSeconds is
// included, unlike ProvenanceTier, since it's a tunable cascade knob (Facet
// 4) rather than a security marker; no caller currently sets it to a
// non-nil value on update (no API request field exposes it yet —
// TASKS/reflex-taxonomy/02-recurrence-cascade.md's job), so today every
// call preserves whatever value GetAgentReflex populated row from.
func (s *Store) UpdateAgentReflex(ctx context.Context, row AgentReflex) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}

// BumpAgentReflexFired increments fired_count and stamps last_fired_at.
// Called by the executor after a reflex's action has been staged.
func (s *Store) BumpAgentReflexFired(ctx context.Context, id string, now time.Time) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}

// DeleteAgentReflex removes a row by id. Any agent_reflex_opt_outs rows
// referencing it are removed automatically via ON DELETE CASCADE
// (migration 115_agent_reflex_opt_out.sql) — no manual cleanup needed
// here, unlike agent_reflexes.agent_id's own FK to agent_profiles,
// which DeleteAgent still cleans up explicitly.
func (s *Store) DeleteAgentReflex(ctx context.Context, id string) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}

// SetAgentReflexOptOut records that agentID has opted out of reflexID —
// the per-agent override mechanism Phase 1 item 07 built for
// opt_out_allowed=true reflexes (TASKS/phase-1/07-add-reflex-opt-out-field.md).
// Idempotent: re-opting-out an already-opted-out (agentID, reflexID)
// pair is a no-op. Has no effect on a reflex whose opt_out_allowed is
// false — ListAgentReflexesForAgent ignores this table entirely for
// those rows, by design.
func (s *Store) SetAgentReflexOptOut(ctx context.Context, agentID, reflexID string) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}

// ClearAgentReflexOptOut removes an opt-out marker, re-enabling the
// reflex for that agent. A no-op (not an error) if no such marker
// exists.
func (s *Store) ClearAgentReflexOptOut(ctx context.Context, agentID, reflexID string) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}

// ListAgentReflexOptOuts returns the reflex ids agentID has opted out
// of.
func (s *Store) ListAgentReflexOptOuts(ctx context.Context, agentID string) ([]string, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// CountClassBaseReflexByName returns the count of class-base rows (no
// agent_id) with the given class_tag and name. Used by the seeder to
// idempotently re-insert only when missing.
func (s *Store) CountClassBaseReflexByName(ctx context.Context, classTag, name string) (int, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return 0, ErrImmutableAgentProfile
}

// CountAgentReflexByName returns the count of agent-scoped rows (a
// specific agent_id, no class_tag) with the given agent_id and name.
// The AgentID-scoped mirror of CountClassBaseReflexByName — used by
// seeders that idempotently attach reflexes to one resolved agent
// profile (e.g. CW-20260816-0023's Loom Curator/Weaver pilot pair)
// rather than to a whole class.
func (s *Store) CountAgentReflexByName(ctx context.Context, agentID, name string) (int, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return 0, ErrImmutableAgentProfile
}

// InsertPendingReflex inserts a new pending_reflexes row. If row.ID is
// empty, a ULID is generated.
func (s *Store) InsertPendingReflex(ctx context.Context, row PendingReflex) (string, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return "", ErrImmutableAgentProfile
}

// GetPendingReflex returns a row by id, or ErrPendingReflexNotFound.
func (s *Store) GetPendingReflex(ctx context.Context, id string) (*PendingReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// ListPendingReflexes returns all rows with the given status, or all
// statuses if status is empty. Ordered by proposed_at DESC.
func (s *Store) ListPendingReflexes(ctx context.Context, status string) ([]PendingReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// ApprovePendingReflex flips the pending row to status='approved' and
// inserts a new agent_reflexes row carrying the trigger/action spec.
// Returns the newly inserted agent_reflexes row. opt_out_allowed is not
// in this INSERT's column list, so it takes the column's own DEFAULT
// TRUE — correct here since agent-proposed reflexes approved through
// this path are never the hand-picked safety-critical seeds Phase 1
// item 07 marks non-opt-outable in seeds.go.
func (s *Store) ApprovePendingReflex(ctx context.Context, id, reviewedBy string) (*AgentReflex, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// RejectPendingReflex flips the row to status='rejected' with the
// reason recorded in reviewed_by (e.g. "alice: too aggressive").
func (s *Store) RejectPendingReflex(ctx context.Context, id, reviewedBy, reason string) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}
