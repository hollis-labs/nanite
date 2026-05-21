package subagent

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// insertFailedRun helps seed subagent_runs rows that simulate a failure
// (status='failed' with the captured error string) for audit testing.
// Bypasses the Service.Spawn → fail-fast gate by inserting directly,
// which is exactly how the orphan reaper and legacy pre-gate paths
// landed these rows historically.
func insertFailedRun(t *testing.T, db *sql.DB, id, role, errMsg string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO subagent_runs
		   (id, parent_session_id, child_session_id, role, prompt, mode,
		    status, inputs_json, result_json, error, timeout_seconds,
		    created_at, started_at, completed_at, parent_agent_id,
		    envelope_instance_id, approved_at, approved_by, rejected_at,
		    rejection_reason, provider)
		 VALUES (?, 'sess-1', '', ?, 'p', 'sync', 'failed', '{}', '', ?, 300,
		         '2026-05-19T00:00:00Z', '', '2026-05-19T00:01:00Z', 'p-agent',
		         '', '', '', '', '', '')`,
		id, role, errMsg)
	if err != nil {
		t.Fatalf("seed failed run %q: %v", id, err)
	}
}

// TestAuditUnknownRoles_GroupsByRoleAndCounts covers the audit happy
// path: orphan failures, config failures, and unrelated failures are
// counted per role; rows whose error doesn't match the audit filter
// (e.g. "context deadline exceeded" on a legitimate timeout) are
// excluded.
func TestAuditUnknownRoles_GroupsByRoleAndCounts(t *testing.T) {
	db, s := newTestDB(t)

	// system-architect — the c271 pattern. Two orphan reaper rows.
	insertFailedRun(t, db, "r1", "system-architect", "timeout: orphan, no child session")
	insertFailedRun(t, db, "r2", "system-architect", "timeout: orphan, no child session")
	// system-architect, post-gate: one config rejection landing in
	// the audit via the new sentinel message.
	insertFailedRun(t, db, "r3", "system-architect",
		`subagent: no agent profile registered for role "system-architect"`)

	// planner — c256 pattern. One stalled run that the gate would also
	// retire (matches "not executable" in the audit filter).
	insertFailedRun(t, db, "r4", "planner",
		`subagent: agent profile is not executable and not in the text-only role whitelist: "planner"`)

	// Unrelated failure (e.g. provider crash). Must NOT appear in
	// the audit output — it's not the fail-fast gate's failure mode.
	insertFailedRun(t, db, "r5", "worker", "provider stream stalled — no response")

	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetProfileResolver(s)

	entries, err := svc.AuditUnknownRoles(context.Background())
	if err != nil {
		t.Fatalf("AuditUnknownRoles: %v", err)
	}

	// We expect two rows: system-architect and planner. Worker should
	// be excluded because its failure does not match the audit filter.
	if len(entries) != 2 {
		t.Fatalf("got %d audit entries, want 2 (system-architect, planner): %+v", len(entries), entries)
	}

	byRole := map[string]RoleAuditEntry{}
	for _, e := range entries {
		byRole[e.Role] = e
	}

	sa, ok := byRole["system-architect"]
	if !ok {
		t.Fatal("missing audit entry for system-architect")
	}
	if sa.OrphanFailures != 2 {
		t.Errorf("system-architect orphan_failures = %d, want 2", sa.OrphanFailures)
	}
	if sa.ConfigFailures != 1 {
		t.Errorf("system-architect config_failures = %d, want 1", sa.ConfigFailures)
	}
	if sa.HasProfile {
		t.Error("system-architect: HasProfile=true; expected false (no profile registered)")
	}

	pl, ok := byRole["planner"]
	if !ok {
		t.Fatal("missing audit entry for planner")
	}
	if pl.ConfigFailures != 1 {
		t.Errorf("planner config_failures = %d, want 1", pl.ConfigFailures)
	}
	if !pl.HasProfile {
		t.Error("planner: HasProfile=false; expected true (canonical internal profile is seeded)")
	}
	if pl.ProfileCanExec {
		t.Error("planner: ProfileCanExec=true; expected false (planner profile has no tool surface)")
	}
}

// TestAuditUnknownRoles_EmptyWhenNoFailingRuns ensures the audit
// returns an empty slice (not an error, not nil-with-error) when no
// rows match. Important so the self-tool's JSON encoding stays a
// well-formed empty array.
func TestAuditUnknownRoles_EmptyWhenNoFailingRuns(t *testing.T) {
	db, s := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetProfileResolver(s)

	entries, err := svc.AuditUnknownRoles(context.Background())
	if err != nil {
		t.Fatalf("AuditUnknownRoles: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty slice, got %d entries: %+v", len(entries), entries)
	}
}

// TestAuditUnknownRoles_FlagsTextOnlyWhitelist ensures the text-only
// whitelist flag is reflected. A future config-failure on
// hint-selector (e.g. if someone removes its profile) would surface
// here AND advertise that it's still in the whitelist — the operator
// can then decide whether to restore the profile or remove the
// whitelist entry.
func TestAuditUnknownRoles_FlagsTextOnlyWhitelist(t *testing.T) {
	db, _ := newTestDB(t)
	// Synthetic: hint-selector orphan row (won't happen in practice
	// because hint-selector is whitelisted, but pin the flag wiring).
	insertFailedRun(t, db, "r1", "hint-selector", "timeout: orphan, no child session")

	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	// Deliberately do NOT wire the resolver — exercises the
	// HasProfile=false branch when svc.profiles is nil.

	entries, err := svc.AuditUnknownRoles(context.Background())
	if err != nil {
		t.Fatalf("AuditUnknownRoles: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(entries), entries)
	}
	if !entries[0].InTextOnlyList {
		t.Errorf("hint-selector audit entry: InTextOnlyList=false; expected true")
	}
}

// Confirm we link the symbol so the test stays accurate even if
// the `store` import is removed by an over-eager edit.
var _ = store.AgentProfile{}
