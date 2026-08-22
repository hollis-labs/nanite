package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ActiveSubagentRunForParent reports whether the given parent session
// currently has a subagent_runs row in status='running'. Used by
// chat_generate's deadline / error-event sites to classify "subagent
// hang vs parent stream failure" so the FE-visible artifacts can be
// suppressed for the subagent-caused branch (CW-20260512-0002 d).
//
// Returns:
//   - id: the most recent active run's id (empty when ok=false).
//   - role: the role slug the run targets (for structured logging).
//   - childSessionID: the child session id (may be empty if the row
//     hasn't yet recorded one — orphan case).
//   - ok: true when an active row exists.
//   - err: only set on real DB errors; sql.ErrNoRows is normalized to
//     ok=false, nil error.
//
// LIMIT 1 because the chat loop only needs to know "is at least one
// active" for classification; per-row details for *all* active runs
// belong on the subagent.Status API, not this hot-path classification.
// "Most recent" is keyed by created_at DESC so the call site logs the
// run most likely associated with the parent's current pause.
func (s *Store) ActiveSubagentRunForParent(ctx context.Context, parentSessionID string) (id, role, childSessionID string, ok bool, err error) {
	const q = `SELECT id, role, child_session_id
	             FROM subagent_runs
	            WHERE parent_session_id = ?
	              AND status = 'running'
	         ORDER BY created_at DESC
	            LIMIT 1`
	row := s.DB.QueryRowContext(ctx, q, parentSessionID)
	if scanErr := row.Scan(&id, &role, &childSessionID); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return "", "", "", false, nil
		}
		return "", "", "", false, fmt.Errorf("active subagent run for parent %s: %w", parentSessionID, scanErr)
	}
	return id, role, childSessionID, true, nil
}

// IsSubagentSession reports whether sessionID is itself a spawned
// subagent — i.e. it appears as the child_session_id of some
// subagent_runs row. This is the authoritative "does this session have
// a parent?" signal: the subagent runner stamps child_session_id back
// onto the run row once it creates the child chat session
// (persistChildSessionID), so a non-empty match means sessionID was
// created by a spawn.
//
// Used by the subagent recursion-depth cap (CW-20260516-0066): only a
// depth-0 progenitor (a session with NO parent) may spawn
// session-creating subagents. A session that is itself a subagent must
// have its spawn requests rejected.
//
// An empty sessionID returns (false, nil) — a missing caller identity
// is treated as "no parent" so direct/test invocations are not blocked
// by the cap (the cap fails open on an unknown caller; the spawn still
// passes through trust + approval gating).
func (s *Store) IsSubagentSession(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	const q = `SELECT 1 FROM subagent_runs WHERE child_session_id = ? LIMIT 1`
	var dummy int
	err := s.DB.QueryRowContext(ctx, q, sessionID).Scan(&dummy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("is subagent session %s: %w", sessionID, err)
	}
	return true, nil
}
