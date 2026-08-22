package subagent

import (
	"context"
	"fmt"
)

// RoleAuditEntry summarizes one role slug's audit footprint in the
// subagent_runs table (CW-20260519-0123). Returned by
// Service.AuditUnknownRoles.
//
// Distinct rows are reported per role slug; for each slug the audit
// reports the total run count and the breakdown of failure reasons.
//
// Scope (PR #214 review fix item 4): post-fail-fast-gate the audit is
// effectively an ORPHAN-ONLY signal. The CW-20260519-0123 gate at the
// Spawn boundary returns ErrNoProfileForRole / ErrRoleNotExecutable
// BEFORE any subagent_runs row is inserted — so in the steady state,
// config-error spawns produce zero rows and therefore zero audit
// entries. The `ConfigFailures` counter survives in the schema and
// query for backward compat with historical rows written before the
// gate landed (or in the future, if a deployment ever persists a
// config-fault row pre-runner; the query is the catch). The
// operator-facing meaning is now: this audit surfaces orphans the
// reaper marked failed — registering a profile for the named role (or
// flipping its can_execute) retires future appearances.
//
// `HasProfile` is the lookup result against ProfileResolver — the
// operator-facing signal. When false, the audit suggests the role
// should either be registered (Phase-6 work) or routed to a known
// slug. When true, the appearance in the audit means the role is
// known but its runs landed in failure paths the audit surfaces.
type RoleAuditEntry struct {
	Role           string `json:"role"`
	TotalRuns      int    `json:"total_runs"`
	OrphanFailures int    `json:"orphan_failures"`
	ConfigFailures int    `json:"config_failures"`
	OtherFailures  int    `json:"other_failures"`
	HasProfile     bool   `json:"has_profile"`
	ProfileCanExec bool   `json:"profile_can_execute"`
	InTextOnlyList bool   `json:"in_text_only_whitelist"`
}

// AuditUnknownRoles scans subagent_runs and returns one RoleAuditEntry
// per distinct role slug whose runs include at least one failure of
// the kind the fail-fast gate is meant to retire. Surfaces roles
// requested-but-unregistered to the operator (CW-20260519-0123 scope
// item 3).
//
// Scope (PR #214 review fix item 4): the post-gate steady-state shape
// of this audit is ORPHAN-ONLY. ErrNoProfileForRole /
// ErrRoleNotExecutable now return from Spawn BEFORE inserting a
// subagent_runs row, so config-fault spawns produce no rows for this
// query to scan. The "no agent profile registered" / "not executable"
// LIKE clauses still match in the query so historical rows written
// pre-gate (e.g. databases that ran on a build between c271 reproduction
// and the gate landing) and any future code path that persists a
// pre-runner config-fault row still appear in the report. In the
// steady state, this audit's signal is "orphans the reaper marked
// failed"; the operator action is the same as before (register the
// missing profile or route the parent to a known slug).
//
// When svc.profiles is wired, each returned entry's HasProfile and
// ProfileCanExec fields are populated by a per-slug GetAgentBySlug
// call. When svc.profiles is nil, both fields are false and the
// caller cannot distinguish "no profile" from "profile present but
// not consulted"; production wiring always sets the resolver.
func (svc *Service) AuditUnknownRoles(ctx context.Context) ([]RoleAuditEntry, error) {
	if svc.db == nil {
		return nil, fmt.Errorf("subagent audit: no db configured")
	}
	const q = `
		SELECT role,
		       COUNT(*) AS total,
		       SUM(CASE WHEN error LIKE '%orphan%' THEN 1 ELSE 0 END) AS orphans,
		       SUM(CASE WHEN error LIKE '%no agent profile registered%'
		                  OR error LIKE '%not executable%'
		                THEN 1 ELSE 0 END) AS configs,
		       SUM(CASE WHEN status = 'failed'
		                  AND error NOT LIKE '%orphan%'
		                  AND error NOT LIKE '%no agent profile registered%'
		                  AND error NOT LIKE '%not executable%'
		                THEN 1 ELSE 0 END) AS others
		  FROM subagent_runs
		 WHERE status = 'failed'
		   AND (error LIKE '%orphan%'
		     OR error LIKE '%no agent profile registered%'
		     OR error LIKE '%not executable%')
		 GROUP BY role
		 ORDER BY orphans DESC, configs DESC, total DESC`

	rows, err := svc.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("subagent audit query: %w", err)
	}

	// Drain the result set first, THEN do per-row profile lookups.
	// Scanning rows holds a database connection; calling
	// svc.profiles.GetAgentBySlug from inside the loop opens a second
	// connection — SQLite under the default sqlite driver serializes
	// strictly enough that this deadlocks the test DB (writer/reader
	// pool of 1). Close the iterator before resolving profiles to
	// avoid the contention.
	var out []RoleAuditEntry
	for rows.Next() {
		var e RoleAuditEntry
		if err := rows.Scan(&e.Role, &e.TotalRuns, &e.OrphanFailures, &e.ConfigFailures, &e.OtherFailures); err != nil {
			rows.Close()
			return nil, fmt.Errorf("subagent audit scan: %w", err)
		}
		e.InTextOnlyList = isTextOnlyRole(e.Role)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("subagent audit rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("subagent audit rows close: %w", err)
	}

	// Resolve the role against the profile registry so the operator
	// can tell at a glance whether the audit row maps to "no profile"
	// (HasProfile=false → consider creating one) or "not executable"
	// (HasProfile=true, ProfileCanExec=false → consider adding tool
	// surface or whitelisting). When svc.profiles is nil (tests), both
	// fields stay zero-valued.
	if svc.profiles != nil {
		for i := range out {
			profile, lerr := svc.profiles.GetAgentBySlug(ctx, out[i].Role)
			if lerr == nil && profile != nil {
				out[i].HasProfile = true
				out[i].ProfileCanExec = profile.CanExecute
			}
		}
	}

	return out, nil
}
