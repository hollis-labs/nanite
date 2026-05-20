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
// reports the total run count and the breakdown of failure reasons
// the fail-fast gate is intended to retire — the orphan-reaper signal
// (CW-20260519-0073: "timeout: orphan, no child session") and the
// post-gate config-error signal (CW-20260519-0123: the new sentinels
// in service.go).
//
// `HasProfile` is the lookup result against ProfileResolver — the
// operator-facing signal. When false, the audit suggests the role
// should either be registered (Phase-6 work) or routed to a known
// slug. When true, the appearance in the audit means the role is
// known but its runs landed in failure paths the audit surfaces.
type RoleAuditEntry struct {
	Role            string `json:"role"`
	TotalRuns       int    `json:"total_runs"`
	OrphanFailures  int    `json:"orphan_failures"`
	ConfigFailures  int    `json:"config_failures"`
	OtherFailures   int    `json:"other_failures"`
	HasProfile      bool   `json:"has_profile"`
	ProfileCanExec  bool   `json:"profile_can_execute"`
	InTextOnlyList  bool   `json:"in_text_only_whitelist"`
}

// AuditUnknownRoles scans subagent_runs and returns one RoleAuditEntry
// per distinct role slug whose runs include at least one failure of
// the kind the fail-fast gate is meant to retire (orphan-reaped or
// config-error). Surfaces roles requested-but-unregistered to the
// operator (CW-20260519-0123 scope item 3).
//
// The query is bounded by status=failed AND
// (error LIKE '%orphan%' OR error LIKE '%no profile%' OR error LIKE
// '%not executable%') so it only reports rows that this ticket's gate
// changes the disposition of. A role slug whose only runs succeeded
// (or failed for unrelated reasons) does NOT appear — the audit is a
// diagnostic for the missing-profile / non-executable pattern, not a
// general failure report.
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
			profile, lerr := svc.profiles.GetAgentBySlug(out[i].Role)
			if lerr == nil && profile != nil {
				out[i].HasProfile = true
				out[i].ProfileCanExec = profile.CanExecute
			}
		}
	}

	return out, nil
}
