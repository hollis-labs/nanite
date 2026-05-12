package store

import (
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
func (s *Store) ActiveSubagentRunForParent(parentSessionID string) (id, role, childSessionID string, ok bool, err error) {
	const q = `SELECT id, role, child_session_id
	             FROM subagent_runs
	            WHERE parent_session_id = ?
	              AND status = 'running'
	         ORDER BY created_at DESC
	            LIMIT 1`
	row := s.DB.QueryRow(q, parentSessionID)
	if scanErr := row.Scan(&id, &role, &childSessionID); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return "", "", "", false, nil
		}
		return "", "", "", false, fmt.Errorf("active subagent run for parent %s: %w", parentSessionID, scanErr)
	}
	return id, role, childSessionID, true, nil
}
