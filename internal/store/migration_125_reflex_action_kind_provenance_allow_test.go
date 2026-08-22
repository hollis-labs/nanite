package store

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// wantProvenanceAllow is the exact allow-list
// TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md specifies: every
// one of the 6 action kinds x 3 provenance tiers, except
// (halt_session, plugin) and (dispatch_to_agent, plugin) -- plus every
// later migration's own additions to the same table. This test runs
// against the fully-migrated live schema (newTestStore applies every
// migration, not just 125 in isolation), so it also covers
// TASKS/loops/11-loop-event-predicate-trigger.md's migration
// 145_agent_reflex_resume_loop_run.sql, which adds resume_loop_run x
// {system, operator} (not plugin, mirroring halt_session/
// dispatch_to_agent's own restriction). Bump this map (and the row-count
// checks below) again the next time a migration adds another action kind.
var wantProvenanceAllow = map[[2]string]bool{
	{"inject_reminder", "system"}:     true,
	{"inject_reminder", "operator"}:   true,
	{"inject_reminder", "plugin"}:     true,
	{"force_tool_choice", "system"}:   true,
	{"force_tool_choice", "operator"}: true,
	{"force_tool_choice", "plugin"}:   true,
	{"send_message", "system"}:        true,
	{"send_message", "operator"}:      true,
	{"send_message", "plugin"}:        true,
	{"add_schedule", "system"}:        true,
	{"add_schedule", "operator"}:      true,
	{"add_schedule", "plugin"}:        true,
	{"halt_session", "system"}:        true,
	{"halt_session", "operator"}:      true,
	{"halt_session", "plugin"}:        false, // deliberately denied
	{"dispatch_to_agent", "system"}:   true,
	{"dispatch_to_agent", "operator"}: true,
	{"dispatch_to_agent", "plugin"}:   false, // deliberately denied
	{"resume_loop_run", "system"}:     true,
	{"resume_loop_run", "operator"}:   true,
	{"resume_loop_run", "plugin"}:     false, // deliberately denied
}

// TestMigrate125SeedsProvenanceAllowList is the regression test for
// TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md: exactly 16
// allowed (kind, tier) rows as of migration 125 itself (plus 2 more from
// migration 145's own resume_loop_run x {system, operator} addition, 18
// total as of this fully-migrated schema), matching the design doc's two
// named "obvious candidates" for exclusion (halt_session/dispatch_to_agent
// away from plugin-tier) plus 145's own resume_loop_run exclusion,
// byte-for-byte.
func TestMigrate125SeedsProvenanceAllowList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rows, err := s.DB.QueryContext(ctx,
		`SELECT kind_name, tier_name FROM reflex_action_kind_provenance_allow ORDER BY kind_name, tier_name`)
	if err != nil {
		t.Fatalf("query reflex_action_kind_provenance_allow: %v", err)
	}
	var got []string
	for rows.Next() {
		var kind, tier string
		if err := rows.Scan(&kind, &tier); err != nil {
			t.Fatalf("scan reflex_action_kind_provenance_allow: %v", err)
		}
		got = append(got, kind+"/"+tier)
	}
	rows.Close()
	sort.Strings(got)

	var wantAllowed []string
	for pair, allowed := range wantProvenanceAllow {
		if allowed {
			wantAllowed = append(wantAllowed, pair[0]+"/"+pair[1])
		}
	}
	sort.Strings(wantAllowed)

	if len(got) != 18 {
		t.Fatalf("reflex_action_kind_provenance_allow row count = %d, want 18 (got rows: %v)", len(got), got)
	}
	if !equalStrings(got, wantAllowed) {
		t.Errorf("reflex_action_kind_provenance_allow rows = %v, want %v", got, wantAllowed)
	}

	// Round-trip every one of the 21 (kind, tier) combinations through
	// ActionKindAllowsProvenanceTier, confirming both the 18 allowed and
	// the 3 deliberately-denied pairs resolve as expected.
	for pair, wantAllow := range wantProvenanceAllow {
		allowed, err := s.ActionKindAllowsProvenanceTier(ctx, pair[0], pair[1])
		if err != nil {
			t.Fatalf("ActionKindAllowsProvenanceTier(%q, %q): %v", pair[0], pair[1], err)
		}
		if allowed != wantAllow {
			t.Errorf("ActionKindAllowsProvenanceTier(%q, %q) = %v, want %v", pair[0], pair[1], allowed, wantAllow)
		}
	}

	// An unrecognized kind/tier pair is a plain miss (false), not an error.
	unknownAllowed, err := s.ActionKindAllowsProvenanceTier(ctx, "does_not_exist", "operator")
	if err != nil {
		t.Fatalf("ActionKindAllowsProvenanceTier(unknown kind): %v", err)
	}
	if unknownAllowed {
		t.Errorf("ActionKindAllowsProvenanceTier(unknown kind) = true, want false")
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after 125 already applied: %v", err)
	}
}

// TestApprovePendingReflexCollapsesProvenanceTierToOperator is the
// regression test for TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md
// step 4: the live agent_reflexes row ApprovePendingReflex produces must
// carry provenance_tier='operator' regardless of the pending reflex's own
// proposer identity — mirroring the pre-existing created_by ->
// "operator:"+reviewedBy collapse (Facet 3: agent_proposed is not a fourth
// live tier; "active in agent_reflexes => operator-approved" holds for
// provenance_tier the same way it already holds for created_by).
func TestApprovePendingReflexCollapsesProvenanceTierToOperator(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name       string
		proposedBy string
	}{
		{"proposer looks like a plain agent id", "agent-curator"},
		{"proposer looks like a system seeder", "system"},
		{"proposer already looks operator-ish", "operator:someone-else"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pendingID, err := s.InsertPendingReflex(ctx, PendingReflex{
				ProposedBy:  tc.proposedBy,
				Name:        "pending-" + tc.proposedBy,
				TriggerKind: ReflexTriggerPredicate,
				TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`,
				ActionKind:  ReflexActionInjectReminder,
				ActionSpec:  `{"body":"ground"}`,
				Rationale:   "test",
			})
			if err != nil {
				t.Fatalf("InsertPendingReflex: %v", err)
			}

			approved, err := s.ApprovePendingReflex(ctx, pendingID, "reviewer-alice")
			if err != nil {
				t.Fatalf("ApprovePendingReflex: %v", err)
			}
			if approved.ProvenanceTier != "operator" {
				t.Errorf("ApprovePendingReflex(proposed_by=%q).ProvenanceTier = %q, want %q", tc.proposedBy, approved.ProvenanceTier, "operator")
			}

			// Re-fetch independently to confirm what's actually persisted,
			// not just what the in-memory return value claims.
			reloaded, err := s.GetAgentReflex(ctx, approved.ID)
			if err != nil {
				t.Fatalf("GetAgentReflex(%s): %v", approved.ID, err)
			}
			if reloaded.ProvenanceTier != "operator" {
				t.Errorf("reloaded agent_reflexes row provenance_tier = %q, want %q", reloaded.ProvenanceTier, "operator")
			}
		})
	}
}

// TestRealBackupAgentReflexesValidateAgainstProvenanceAllowList is the
// "no false-positive rejection of already-live data" check TASKS/
// reflex-taxonomy/05-provenance-tier-enforcement.md's Done-means requires:
// every real, pre-existing agent_reflexes row from an actual production
// backup must still pass the new provenance allow-list gate post-migration.
//
// Per EXECUTION-PROCESS.md's schema-migration testing requirement and this
// task's own safety note, the real backup file is never opened in place —
// it is copied into t.TempDir() (an absolute, per-test scratch path) before
// store.New ever touches it. Skips (rather than fails) when the backup
// isn't present on this machine, since it's a real operator artifact, not
// a checked-in fixture.
func TestRealBackupAgentReflexesValidateAgainstProvenanceAllowList(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home directory: %v", err)
	}
	backupSrc := filepath.Join(home, ".local", "share", "nanite", "workspaces", "default", "backups",
		"main.db.pre-execution-backup-20260818-132726")
	if _, err := os.Stat(backupSrc); err != nil {
		t.Skipf("real backup db not present at %s (skipping live-backup verification): %v", backupSrc, err)
	}

	scratchDir := t.TempDir()
	dstPath := filepath.Join(scratchDir, "main.db")
	if err := copyFile(dstPath, backupSrc); err != nil {
		t.Fatalf("copy real backup db to scratch path: %v", err)
	}
	// Copy the matching -wal file too, if it carries any pending frames
	// (this particular backup's is 0 bytes, but don't assume that in
	// general).
	if walInfo, err := os.Stat(backupSrc + "-wal"); err == nil && walInfo.Size() > 0 {
		if err := copyFile(dstPath+"-wal", backupSrc+"-wal"); err != nil {
			t.Fatalf("copy real backup -wal to scratch path: %v", err)
		}
	}
	absPath, err := filepath.Abs(dstPath)
	if err != nil {
		t.Fatalf("resolve scratch db path: %v", err)
	}

	ctx := context.Background()
	rs, err := New(ctx, absPath)
	if err != nil {
		t.Fatalf("open+migrate scratch copy of real backup db: %v", err)
	}
	defer rs.Close()

	// Collect every row fully (and close the cursor) before checking each
	// one against the allow-list — rs's connection pool is a single
	// connection (sqlitekit.OpenSingle), so issuing a second query
	// (ActionKindAllowsProvenanceTier) while this outer *sql.Rows is still
	// open would deadlock waiting for a connection that can't free up
	// until this cursor is fully drained/closed.
	type reflexRow struct {
		id, actionKind, tier string
	}
	var realRows []reflexRow
	rows, err := rs.DB.QueryContext(ctx, `SELECT id, action_kind, provenance_tier FROM agent_reflexes`)
	if err != nil {
		t.Fatalf("query agent_reflexes on real backup copy: %v", err)
	}
	for rows.Next() {
		var r reflexRow
		if err := rows.Scan(&r.id, &r.actionKind, &r.tier); err != nil {
			rows.Close()
			t.Fatalf("scan agent_reflexes row: %v", err)
		}
		realRows = append(realRows, r)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		t.Fatalf("iterate agent_reflexes rows: %v", rowsErr)
	}

	count := 0
	for _, r := range realRows {
		count++
		allowed, err := rs.ActionKindAllowsProvenanceTier(ctx, r.actionKind, r.tier)
		if err != nil {
			t.Fatalf("ActionKindAllowsProvenanceTier(%q, %q) for real row %s: %v", r.actionKind, r.tier, r.id, err)
		}
		if !allowed {
			t.Errorf("real pre-existing agent_reflexes row %s (action_kind=%q, provenance_tier=%q) rejected by the new allow-list — false-positive rejection of already-live data", r.id, r.actionKind, r.tier)
		}
	}
	if count < 14 {
		t.Fatalf("real backup agent_reflexes row count = %d, want >= 14 (spot-check requires the real pre-existing rows, not an empty/synthetic db)", count)
	}
	t.Logf("verified %d real agent_reflexes rows from backup all validate against the new provenance allow-list", count)
}
