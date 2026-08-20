package store

// TASKS/teams/05-agent-reflexes-run-scoping.md — resolves a session to the
// TeamRun (workflow_runs.id) it belongs to, via team_run_members
// (TASKS/teams/02-team-run-members-table.md's table:
// docs/engineering/architecture/15-teams.md's "Slot resolution and the one
// genuinely new persistence table" section — workflow_run_id, slot_name,
// agent_id, session_id, resolved_at, status). Consumed by the two
// dispatch_to_agent call sites (internal/service/chat_reflex_dispatch.go's
// attemptReflexDispatch, internal/selftools/self_tools_dispatch.go's
// matchDispatchToAgentReflex) to widen their agent_reflexes candidate set
// with Store.ListAgentReflexesForWorkflowRun's run-scoped rows whenever the
// evaluating session is itself a resolved Team-run member.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ResolveWorkflowRunIDForSession returns the workflow_run_id (a
// workflow_runs.id, i.e. a TeamRun — "TeamRun IS a WorkflowRun",
// docs/engineering/architecture/15-teams.md's Decision 2) that sessionID
// resolves to via team_run_members, and whether a row was found at all.
//
// Fail-open, deliberately, on two distinct "no scoping" conditions that
// must not be conflated with a real error:
//   - no team_run_members row for this session (found=false, err=nil) —
//     the overwhelmingly common case: most sessions are not part of any
//     Team run.
//   - team_run_members itself does not exist yet (found=false, err=nil,
//     detected via a "no such table" substring match on the driver
//     error) — true for any database that has run this migration
//     (131_agent_reflexes_workflow_run_scoping.sql) but not yet
//     TASKS/teams/02-team-run-members-table.md's own migration (both are
//     independent, parallel-safe schema additions per that task's own
//     "Depends on" note — this store method must not turn "Team routing
//     isn't wired up yet on this database" into a hard failure of the
//     ordinary, non-Team dispatch_to_agent evaluation path every session
//     goes through). Once that migration lands on a given database this
//     branch is simply never hit again — a real query runs every time.
//
// Any other error (a genuine DB fault, not a missing/absent-row
// condition) is returned as a real error — callers should treat that as
// "the lookup itself failed," not "no Team run," and log/no-op
// accordingly (matching how every other reflex list/lookup failure in
// this package's call sites is already handled).
func (s *Store) ResolveWorkflowRunIDForSession(ctx context.Context, sessionID string) (runID string, found bool, err error) {
	if sessionID == "" {
		return "", false, nil
	}
	row := s.DB.QueryRowContext(ctx,
		`SELECT workflow_run_id FROM team_run_members WHERE session_id = ? LIMIT 1`,
		sessionID,
	)
	if scanErr := row.Scan(&runID); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return "", false, nil
		}
		if strings.Contains(scanErr.Error(), "no such table") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("resolve workflow_run_id for session: %w", scanErr)
	}
	if runID == "" {
		// Defensive: team_run_members.workflow_run_id is NOT NULL per
		// TASKS/teams/02-team-run-members-table.md's own DDL, so this
		// should be unreachable against a real row — treated the same as
		// "not found" rather than returning a nonsensical empty-but-found
		// result.
		return "", false, nil
	}
	return runID, true, nil
}
