package store

// FU-30 Phase 3 Stage 3.c — store accessors for agent_reflexes and
// pending_reflexes. Reflexes absorb the FU-21 drift detectors and extend
// to general predicate-AND/OR + event + interval triggers; see
// internal/agent/reflexes for the evaluator + executor.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
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
type AgentReflex struct {
	ID            string `json:"id"`
	AgentID       string `json:"agent_id"`
	ClassTag      string `json:"class_tag"`
	Name          string `json:"name"`
	TriggerKind   string `json:"trigger_kind"`
	TriggerSpec   string `json:"trigger_spec"`
	ActionKind    string `json:"action_kind"`
	ActionSpec    string `json:"action_spec"`
	Status        string `json:"status"`
	Priority      int64  `json:"priority"`
	FiredCount    int64  `json:"fired_count"`
	LastFiredAt   string `json:"last_fired_at"`
	CreatedAt     string `json:"created_at"`
	CreatedBy     string `json:"created_by"`
	OptOutAllowed bool   `json:"opt_out_allowed"`
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
       created_at, created_by, opt_out_allowed`

func scanAgentReflex(scanner interface{ Scan(...any) error }, r *AgentReflex) error {
	return scanner.Scan(
		&r.ID, &r.AgentID, &r.ClassTag, &r.Name,
		&r.TriggerKind, &r.TriggerSpec, &r.ActionKind, &r.ActionSpec,
		&r.Status, &r.Priority, &r.FiredCount, &r.LastFiredAt,
		&r.CreatedAt, &r.CreatedBy, &r.OptOutAllowed,
	)
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
	if row.Name == "" {
		return "", fmt.Errorf("insert agent_reflexes: name is required")
	}
	if row.TriggerKind == "" {
		return "", fmt.Errorf("insert agent_reflexes: trigger_kind is required")
	}
	if row.TriggerSpec == "" {
		return "", fmt.Errorf("insert agent_reflexes: trigger_spec is required")
	}
	if row.ActionKind == "" {
		return "", fmt.Errorf("insert agent_reflexes: action_kind is required")
	}
	if row.ActionSpec == "" {
		return "", fmt.Errorf("insert agent_reflexes: action_spec is required")
	}
	if row.Status == "" {
		row.Status = ReflexStatusActive
	}
	if row.CreatedBy == "" {
		row.CreatedBy = "operator"
	}
	if row.ID == "" {
		row.ID = "rfx-" + ulid.Make().String()
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO agent_reflexes
		    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
		     action_kind, action_spec, status, priority, fired_count,
		     last_fired_at, created_at, created_by, opt_out_allowed)
		 VALUES (?, ?, ?, ?, ?, ?,
		         ?, ?, ?, ?, ?,
		         ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?, ?)`,
		row.ID, nullIfEmpty(row.AgentID), nullIfEmpty(row.ClassTag),
		row.Name, row.TriggerKind, row.TriggerSpec,
		row.ActionKind, row.ActionSpec, row.Status, row.Priority, row.FiredCount,
		nullIfEmpty(row.LastFiredAt),
		row.CreatedAt,
		row.CreatedBy, row.OptOutAllowed,
	)
	if err != nil {
		return "", fmt.Errorf("insert agent_reflexes: %w", err)
	}
	return row.ID, nil
}

// GetAgentReflex returns the row by ID, or ErrAgentReflexNotFound.
func (s *Store) GetAgentReflex(ctx context.Context, id string) (*AgentReflex, error) {
	var out AgentReflex
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentReflexColumns+` FROM agent_reflexes WHERE id = ?`, id,
	)
	if err := scanAgentReflex(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentReflexNotFound
		}
		return nil, fmt.Errorf("get agent_reflexes: %w", err)
	}
	return &out, nil
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
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentReflexColumns+`
		 FROM agent_reflexes
		 WHERE status = 'active'
		   AND (
		         (
		           agent_id IS NULL AND class_tag = ?
		           AND (
		                 opt_out_allowed = 0
		              OR NOT EXISTS (
		                   SELECT 1 FROM agent_reflex_opt_outs o
		                    WHERE o.agent_id = ? AND o.reflex_id = agent_reflexes.id
		                 )
		               )
		         )
		      OR agent_id = ?
		       )
		 ORDER BY priority DESC, created_at ASC`,
		classTag, agentID, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_reflexes: %w", err)
	}
	defer rows.Close()
	out := make([]AgentReflex, 0)
	for rows.Next() {
		var r AgentReflex
		if err := scanAgentReflex(rows, &r); err != nil {
			return nil, fmt.Errorf("scan agent_reflexes: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAllAgentReflexes returns every row in agent_reflexes ordered by
// priority DESC, created_at ASC. Used by the operator surface.
func (s *Store) ListAllAgentReflexes(ctx context.Context, agentID string) ([]AgentReflex, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentReflexColumns+`
		 FROM agent_reflexes
		 WHERE agent_id = ?
		 ORDER BY priority DESC, created_at ASC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list all agent_reflexes: %w", err)
	}
	defer rows.Close()
	out := make([]AgentReflex, 0)
	for rows.Next() {
		var r AgentReflex
		if err := scanAgentReflex(rows, &r); err != nil {
			return nil, fmt.Errorf("scan agent_reflexes: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateAgentReflex updates an existing row's editable fields. Scope
// bindings (agent_id/class_tag), created_at, and created_by are immutable.
func (s *Store) UpdateAgentReflex(ctx context.Context, row AgentReflex) error {
	if row.ID == "" {
		return fmt.Errorf("update agent_reflexes: id is required")
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_reflexes
		    SET name = ?,
		        trigger_kind = ?,
		        trigger_spec = ?,
		        action_kind = ?,
		        action_spec = ?,
		        status = ?,
		        priority = ?,
		        fired_count = ?,
		        last_fired_at = ?,
		        opt_out_allowed = ?
		  WHERE id = ?`,
		row.Name, row.TriggerKind, row.TriggerSpec,
		row.ActionKind, row.ActionSpec,
		row.Status, row.Priority, row.FiredCount,
		nullIfEmpty(row.LastFiredAt), row.OptOutAllowed, row.ID,
	)
	if err != nil {
		return fmt.Errorf("update agent_reflexes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent_reflexes rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentReflexNotFound
	}
	return nil
}

// BumpAgentReflexFired increments fired_count and stamps last_fired_at.
// Called by the executor after a reflex's action has been staged.
func (s *Store) BumpAgentReflexFired(ctx context.Context, id string, now time.Time) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_reflexes
		    SET fired_count = fired_count + 1,
		        last_fired_at = ?
		  WHERE id = ?`,
		now.UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return fmt.Errorf("bump agent_reflexes fired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("bump agent_reflexes rows: %w", err)
	}
	if n == 0 {
		return ErrAgentReflexNotFound
	}
	return nil
}

// DeleteAgentReflex removes a row by id. Any agent_reflex_opt_outs rows
// referencing it are removed automatically via ON DELETE CASCADE
// (migration 115_agent_reflex_opt_out.sql) — no manual cleanup needed
// here, unlike agent_reflexes.agent_id's own FK to agent_profiles,
// which DeleteAgent still cleans up explicitly.
func (s *Store) DeleteAgentReflex(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_reflexes WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete agent_reflexes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent_reflexes rows: %w", err)
	}
	if n == 0 {
		return ErrAgentReflexNotFound
	}
	return nil
}

// SetAgentReflexOptOut records that agentID has opted out of reflexID —
// the per-agent override mechanism Phase 1 item 07 built for
// opt_out_allowed=true reflexes (TASKS/phase-1/07-add-reflex-opt-out-field.md).
// Idempotent: re-opting-out an already-opted-out (agentID, reflexID)
// pair is a no-op. Has no effect on a reflex whose opt_out_allowed is
// false — ListAgentReflexesForAgent ignores this table entirely for
// those rows, by design.
func (s *Store) SetAgentReflexOptOut(ctx context.Context, agentID, reflexID string) error {
	if agentID == "" || reflexID == "" {
		return fmt.Errorf("set agent_reflex_opt_outs: agent_id and reflex_id are required")
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO agent_reflex_opt_outs (agent_id, reflex_id) VALUES (?, ?)`,
		agentID, reflexID,
	)
	if err != nil {
		return fmt.Errorf("set agent_reflex_opt_outs: %w", err)
	}
	return nil
}

// ClearAgentReflexOptOut removes an opt-out marker, re-enabling the
// reflex for that agent. A no-op (not an error) if no such marker
// exists.
func (s *Store) ClearAgentReflexOptOut(ctx context.Context, agentID, reflexID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_reflex_opt_outs WHERE agent_id = ? AND reflex_id = ?`,
		agentID, reflexID,
	)
	if err != nil {
		return fmt.Errorf("clear agent_reflex_opt_outs: %w", err)
	}
	return nil
}

// ListAgentReflexOptOuts returns the reflex ids agentID has opted out
// of.
func (s *Store) ListAgentReflexOptOuts(ctx context.Context, agentID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT reflex_id FROM agent_reflex_opt_outs WHERE agent_id = ? ORDER BY created_at ASC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_reflex_opt_outs: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan agent_reflex_opt_outs: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CountClassBaseReflexByName returns the count of class-base rows (no
// agent_id) with the given class_tag and name. Used by the seeder to
// idempotently re-insert only when missing.
func (s *Store) CountClassBaseReflexByName(ctx context.Context, classTag, name string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_reflexes
		   WHERE agent_id IS NULL AND class_tag = ? AND name = ?`,
		classTag, name,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count class-base reflexes: %w", err)
	}
	return n, nil
}

// CountAgentReflexByName returns the count of agent-scoped rows (a
// specific agent_id, no class_tag) with the given agent_id and name.
// The AgentID-scoped mirror of CountClassBaseReflexByName — used by
// seeders that idempotently attach reflexes to one resolved agent
// profile (e.g. CW-20260816-0023's Loom Curator/Weaver pilot pair)
// rather than to a whole class.
func (s *Store) CountAgentReflexByName(ctx context.Context, agentID, name string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_reflexes
		   WHERE agent_id = ? AND name = ?`,
		agentID, name,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count agent reflexes: %w", err)
	}
	return n, nil
}

// InsertPendingReflex inserts a new pending_reflexes row. If row.ID is
// empty, a ULID is generated.
func (s *Store) InsertPendingReflex(ctx context.Context, row PendingReflex) (string, error) {
	if row.ProposedBy == "" {
		return "", fmt.Errorf("insert pending_reflexes: proposed_by is required")
	}
	if row.Name == "" {
		return "", fmt.Errorf("insert pending_reflexes: name is required")
	}
	if row.TriggerKind == "" || row.TriggerSpec == "" {
		return "", fmt.Errorf("insert pending_reflexes: trigger_kind and trigger_spec required")
	}
	if row.ActionKind == "" || row.ActionSpec == "" {
		return "", fmt.Errorf("insert pending_reflexes: action_kind and action_spec required")
	}
	if row.Rationale == "" {
		return "", fmt.Errorf("insert pending_reflexes: rationale is required")
	}
	if row.Status == "" {
		row.Status = PendingReflexStatusPending
	}
	if row.ID == "" {
		row.ID = "prfx-" + ulid.Make().String()
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO pending_reflexes
		    (id, proposed_by, proposed_at, target_agent_id, name,
		     trigger_kind, trigger_spec, action_kind, action_spec,
		     rationale, status)
		 VALUES (?, ?, COALESCE(NULLIF(?, ''), datetime('now')), ?, ?,
		         ?, ?, ?, ?,
		         ?, ?)`,
		row.ID, row.ProposedBy, row.ProposedAt,
		nullIfEmpty(row.TargetAgentID), row.Name,
		row.TriggerKind, row.TriggerSpec, row.ActionKind, row.ActionSpec,
		row.Rationale, row.Status,
	)
	if err != nil {
		return "", fmt.Errorf("insert pending_reflexes: %w", err)
	}
	return row.ID, nil
}

// GetPendingReflex returns a row by id, or ErrPendingReflexNotFound.
func (s *Store) GetPendingReflex(ctx context.Context, id string) (*PendingReflex, error) {
	var out PendingReflex
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+pendingReflexColumns+` FROM pending_reflexes WHERE id = ?`, id,
	)
	if err := scanPendingReflex(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPendingReflexNotFound
		}
		return nil, fmt.Errorf("get pending_reflexes: %w", err)
	}
	return &out, nil
}

// ListPendingReflexes returns all rows with the given status, or all
// statuses if status is empty. Ordered by proposed_at DESC.
func (s *Store) ListPendingReflexes(ctx context.Context, status string) ([]PendingReflex, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+pendingReflexColumns+`
			 FROM pending_reflexes
			 ORDER BY proposed_at DESC`,
		)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+pendingReflexColumns+`
			 FROM pending_reflexes
			 WHERE status = ?
			 ORDER BY proposed_at DESC`,
			status,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list pending_reflexes: %w", err)
	}
	defer rows.Close()
	out := make([]PendingReflex, 0)
	for rows.Next() {
		var r PendingReflex
		if err := scanPendingReflex(rows, &r); err != nil {
			return nil, fmt.Errorf("scan pending_reflexes: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ApprovePendingReflex flips the pending row to status='approved' and
// inserts a new agent_reflexes row carrying the trigger/action spec.
// Returns the newly inserted agent_reflexes row. opt_out_allowed is not
// in this INSERT's column list, so it takes the column's own DEFAULT
// TRUE — correct here since agent-proposed reflexes approved through
// this path are never the hand-picked safety-critical seeds Phase 1
// item 07 marks non-opt-outable in seeds.go.
func (s *Store) ApprovePendingReflex(ctx context.Context, id, reviewedBy string) (*AgentReflex, error) {
	pending, err := s.GetPendingReflex(ctx, id)
	if err != nil {
		return nil, err
	}
	if pending.Status != PendingReflexStatusPending {
		return nil, fmt.Errorf("approve pending_reflexes: row is %s, not pending", pending.Status)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("approve pending_reflexes: begin tx: %w", err)
	}
	defer tx.Rollback()

	newID := "rfx-" + ulid.Make().String()
	createdBy := pending.ProposedBy
	if reviewedBy != "" {
		createdBy = "operator:" + reviewedBy
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agent_reflexes
		    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
		     action_kind, action_spec, status, priority, fired_count,
		     last_fired_at, created_at, created_by)
		 VALUES (?, ?, NULL, ?, ?, ?,
		         ?, ?, 'active', 0, 0,
		         NULL, datetime('now'), ?)`,
		newID, nullIfEmpty(pending.TargetAgentID),
		pending.Name, pending.TriggerKind, pending.TriggerSpec,
		pending.ActionKind, pending.ActionSpec, createdBy,
	); err != nil {
		return nil, fmt.Errorf("approve pending_reflexes: insert agent_reflex: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE pending_reflexes
		    SET status = 'approved',
		        reviewed_at = datetime('now'),
		        reviewed_by = ?
		  WHERE id = ?`,
		reviewedBy, id,
	); err != nil {
		return nil, fmt.Errorf("approve pending_reflexes: update pending: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("approve pending_reflexes: commit: %w", err)
	}
	return s.GetAgentReflex(ctx, newID)
}

// RejectPendingReflex flips the row to status='rejected' with the
// reason recorded in reviewed_by (e.g. "alice: too aggressive").
func (s *Store) RejectPendingReflex(ctx context.Context, id, reviewedBy, reason string) error {
	tag := reviewedBy
	if reason != "" {
		tag = reviewedBy + ": " + reason
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE pending_reflexes
		    SET status = 'rejected',
		        reviewed_at = datetime('now'),
		        reviewed_by = ?
		  WHERE id = ? AND status = 'pending'`,
		tag, id,
	)
	if err != nil {
		return fmt.Errorf("reject pending_reflexes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reject pending_reflexes rows: %w", err)
	}
	if n == 0 {
		return ErrPendingReflexNotFound
	}
	return nil
}
