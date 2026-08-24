package store

// TASKS/teams/02-team-run-members-table.md — Go-side read/write support for
// the team_run_members table migration 129_team_run_members.sql adds. See
// docs/engineering/architecture/15-teams.md ("Slot resolution and the one
// genuinely new persistence table") for the design this table encodes: a
// TeamRun (a workflow_runs row) resolves each of its Team's slots to a
// concrete (agent_id, session_id) tuple, and this table is the durable
// record of that resolution.
//
// This file is storage-only, per the task's own "Done means": no
// slot-resolution logic (that's task 08) or routing-failure handling
// (that's task 09) lives here — just CRUD over the table.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// team_run_members.status vocabulary — migration 129, taken verbatim from
// that migration's CHECK constraint and doc comment. active is the only
// status this file's InsertTeamRunMember defaults to; failed/replaced/
// stopped are written by future slot-resolution/routing-failure logic
// (tasks 08/09), not by anything in this file.
const (
	TeamRunMemberStatusActive   = "active"
	TeamRunMemberStatusFailed   = "failed"
	TeamRunMemberStatusReplaced = "replaced"
	TeamRunMemberStatusStopped  = "stopped"
)

// ErrTeamRunMemberNotFound is returned when a team_run_members row cannot
// be located by id (UpdateTeamRunMemberStatus's WHERE id=? matches
// nothing).
var ErrTeamRunMemberNotFound = errors.New("team run member not found")

// TeamRunMember is one row in the team_run_members table — the resolved
// (agent_id, session_id) tuple occupying a Team Slot for the lifetime of
// one TeamRun (a workflow_runs row). Mirrors the table's columns 1:1; see
// migration 129's doc comment for the full status vocabulary.
type TeamRunMember struct {
	ID            string `json:"id"`
	WorkflowRunID string `json:"workflow_run_id"`
	SlotName      string `json:"slot_name"`
	AgentID       string `json:"agent_id"`
	SessionID     string `json:"session_id"`
	ResolvedAt    string `json:"resolved_at"` // TEXT column, sqlite datetime('now') format
	Status        string `json:"status"`
}

const teamRunMemberColumns = `id, workflow_run_id, slot_name, agent_id, session_id, resolved_at, status`

func scanTeamRunMember(scanner interface{ Scan(...any) error }, m *TeamRunMember) error {
	return scanner.Scan(
		&m.ID, &m.WorkflowRunID, &m.SlotName, &m.AgentID, &m.SessionID,
		&m.ResolvedAt, &m.Status,
	)
}

// InsertTeamRunMember inserts a new team_run_members row recording one
// slot resolution. m.ID is generated via uuid.New().String() when the
// caller leaves it empty, matching this codebase's own
// insert-time-ID-generation convention (internal/store/agents.go's
// CreateAgent, internal/store/artifacts.go's CreateArtifact,
// internal/store/selftool_reactions.go's InsertSelftoolReaction). m.Status
// defaults to TeamRunMemberStatusActive when the caller leaves it empty —
// a slot resolution is live the moment it's recorded, unless the caller
// says otherwise.
//
// Deliberately does not enforce uniqueness on (workflow_run_id, slot_name)
// — migration 129's own CREATE TABLE has no such constraint, by design:
// a concurrent-activation-mode slot resolves to multiple rows for the same
// (workflow_run_id, slot_name) pair (see the migration's doc comment).
func (s *Store) InsertTeamRunMember(ctx context.Context, m TeamRunMember) (*TeamRunMember, error) {
	if m.WorkflowRunID == "" {
		return nil, fmt.Errorf("insert team_run_members: workflow_run_id is required")
	}
	if m.SlotName == "" {
		return nil, fmt.Errorf("insert team_run_members: slot_name is required")
	}
	if m.AgentID == "" {
		return nil, fmt.Errorf("insert team_run_members: agent_id is required")
	}
	if m.SessionID == "" {
		return nil, fmt.Errorf("insert team_run_members: session_id is required")
	}
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.Status == "" {
		m.Status = TeamRunMemberStatusActive
	}

	if m.ResolvedAt == "" {
		// Let the column default (datetime('now')) populate resolved_at,
		// then read it back so the returned struct reflects the row that
		// actually landed rather than a Go-side approximation of "now".
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO team_run_members (id, workflow_run_id, slot_name, agent_id, session_id, status)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			m.ID, m.WorkflowRunID, m.SlotName, m.AgentID, m.SessionID, m.Status,
		)
		if err != nil {
			return nil, fmt.Errorf("insert team_run_members: %w", err)
		}
		row := s.DB.QueryRowContext(ctx,
			`SELECT `+teamRunMemberColumns+` FROM team_run_members WHERE id = ?`, m.ID,
		)
		if err := scanTeamRunMember(row, &m); err != nil {
			return nil, fmt.Errorf("read back inserted team_run_members row: %w", err)
		}
		return &m, nil
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO team_run_members (id, workflow_run_id, slot_name, agent_id, session_id, resolved_at, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.WorkflowRunID, m.SlotName, m.AgentID, m.SessionID, m.ResolvedAt, m.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("insert team_run_members: %w", err)
	}
	return &m, nil
}

// ListTeamRunMembersByRun returns every team_run_members row for runID,
// across every slot, ordered by resolved_at then id for a stable result
// order across calls.
func (s *Store) ListTeamRunMembersByRun(ctx context.Context, runID string) ([]TeamRunMember, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+teamRunMemberColumns+` FROM team_run_members
		  WHERE workflow_run_id = ?
		  ORDER BY resolved_at, id`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list team_run_members by run: %w", err)
	}
	defer closeRows(rows)

	out := make([]TeamRunMember, 0)
	for rows.Next() {
		var m TeamRunMember
		if err := scanTeamRunMember(rows, &m); err != nil {
			return nil, fmt.Errorf("scan team_run_members: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListTeamRunMembersBySlot returns every team_run_members row for the
// (runID, slotName) pair — every concrete member currently or previously
// resolved into that Team Slot for this run, including
// non-active-status rows (callers that only want live members should
// filter Status == TeamRunMemberStatusActive themselves; this function
// intentionally does not scope by status, since "every member ever
// resolved into this slot" is also a legitimate read, e.g. for the
// routing-provenance trace 15-teams.md's "Routing" section describes).
// A concurrent-activation-mode slot (min:1, max:4) is expected to return
// more than one row here, correctly distinguished from a different slot's
// rows in the same run by the slot_name filter.
func (s *Store) ListTeamRunMembersBySlot(ctx context.Context, runID, slotName string) ([]TeamRunMember, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+teamRunMemberColumns+` FROM team_run_members
		  WHERE workflow_run_id = ? AND slot_name = ?
		  ORDER BY resolved_at, id`,
		runID, slotName,
	)
	if err != nil {
		return nil, fmt.Errorf("list team_run_members by slot: %w", err)
	}
	defer closeRows(rows)

	out := make([]TeamRunMember, 0)
	for rows.Next() {
		var m TeamRunMember
		if err := scanTeamRunMember(rows, &m); err != nil {
			return nil, fmt.Errorf("scan team_run_members: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateTeamRunMemberStatus sets the status of the team_run_members row
// identified by id. Returns ErrTeamRunMemberNotFound if no row matches.
// status is validated against migration 129's CHECK constraint vocabulary
// before the write, so a caller gets a typed Go error instead of a raw
// sqlite CHECK-constraint-violation error for an invalid value.
func (s *Store) UpdateTeamRunMemberStatus(ctx context.Context, id, status string) error {
	switch status {
	case TeamRunMemberStatusActive, TeamRunMemberStatusFailed, TeamRunMemberStatusReplaced, TeamRunMemberStatusStopped:
	default:
		return fmt.Errorf("update team_run_members status: invalid status %q", status)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE team_run_members SET status = ? WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("update team_run_members status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update team_run_members status rows affected: %w", err)
	}
	if n == 0 {
		return ErrTeamRunMemberNotFound
	}
	return nil
}

// GetTeamRunMember returns the team_run_members row identified by id, or
// ErrTeamRunMemberNotFound. Not required by task 02's own "What to do"
// list, but a natural, cheap complement to UpdateTeamRunMemberStatus for
// callers that need to read back a single row by id rather than scanning
// a whole run/slot.
func (s *Store) GetTeamRunMember(ctx context.Context, id string) (*TeamRunMember, error) {
	var m TeamRunMember
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+teamRunMemberColumns+` FROM team_run_members WHERE id = ?`, id,
	)
	if err := scanTeamRunMember(row, &m); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTeamRunMemberNotFound
		}
		return nil, fmt.Errorf("get team_run_members: %w", err)
	}
	return &m, nil
}
