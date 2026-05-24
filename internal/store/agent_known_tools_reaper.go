package store

import (
	"context"
	"fmt"
)

// ReapExpiredAgentKnownTools deletes non-pinned agent_known_tools rows whose
// last_used_at + ttl_seconds has passed. Pinned rows and rows with NULL
// ttl_seconds (role-seed default) are immortal — they are never deleted by
// this sweep.
//
// Returns the number of rows actually deleted so callers (the periodic
// reaper goroutine + tests) can log + assert telemetry.
//
// SQL details:
//   - pinned = 0 guards immortal rows.
//   - last_used_at IS NOT NULL guards rows that have never been used; until
//     a tool is actually activated we have no clock to age against.
//   - ttl_seconds IS NOT NULL guards rows with no TTL configured (role seed
//     default). The schema makes ttl_seconds nullable specifically for this
//     "never expire" case.
//   - datetime(last_used_at, '+N seconds') < datetime('now') is the standard
//     SQLite TTL comparison; both sides go through datetime() so the string
//     comparison is canonical (cf. subagent.Reaper for the same pattern).
func (s *Store) ReapExpiredAgentKnownTools(ctx context.Context) (int64, error) {
	const q = `DELETE FROM agent_known_tools
		 WHERE pinned = 0
		   AND last_used_at IS NOT NULL
		   AND ttl_seconds IS NOT NULL
		   AND datetime(last_used_at, '+' || ttl_seconds || ' seconds') < datetime('now')`
	res, err := s.DB.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("reap agent_known_tools: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reap agent_known_tools rows affected: %w", err)
	}
	return n, nil
}

// ReapExpiredAgentKnownSkills mirrors ReapExpiredAgentKnownTools for the
// agent_known_skills table — same shape, same TTL semantics, separate
// statement because the schema separation is real (different secondary key
// column, different indexes).
func (s *Store) ReapExpiredAgentKnownSkills(ctx context.Context) (int64, error) {
	const q = `DELETE FROM agent_known_skills
		 WHERE pinned = 0
		   AND last_used_at IS NOT NULL
		   AND ttl_seconds IS NOT NULL
		   AND datetime(last_used_at, '+' || ttl_seconds || ' seconds') < datetime('now')`
	res, err := s.DB.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("reap agent_known_skills: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reap agent_known_skills rows affected: %w", err)
	}
	return n, nil
}
