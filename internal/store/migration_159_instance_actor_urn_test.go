package store

import (
	"context"
	"strings"
	"testing"
)

// Migration 159 moves actor identity from the profile to the durable instance.
// Two halves need their own evidence, and neither gets it from the rest of the
// suite:
//
//   - The BACKFILL is invisible everywhere else, exactly as 158's was. A test
//     store migrates to head before anything inserts, so no row ever exists
//     without a URN for 159 to fill. A broken UPDATE would leave every
//     pre-existing instance addressless on a real upgrade and fail nothing
//     here.
//   - The RENAME is the retirement. If it silently did not happen,
//     agent_profiles would still expose an addressable urn column and the
//     thing this migration exists to remove would still be there.
func TestMigration159_BackfillsInstanceActorURNs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	provider := gooseProviderFor(t, s)

	if _, err := provider.DownTo(ctx, 158); err != nil {
		t.Fatalf("goose DownTo 158: %v", err)
	}

	// Two instances against ONE profile — the case the architecture forbids
	// sharing an identity for, and the case the live database already
	// contained. They must come out of the migration with different URNs.
	agent := makeTestAgent(t, s, "mig159-profile")
	ids := []string{"mig159-inst-a", "mig159-inst-b"}
	for _, id := range ids {
		plantPre159Instance(t, s, id, agent.ID)
	}

	// Positive control on the pre-state: at 158 the column must not exist, so
	// the assertions below cannot be measuring a column that was there all
	// along.
	if columnExists(t, s, "durable_agent_instances", "urn") {
		t.Fatal("durable_agent_instances.urn exists at schema 158 — the backfill assertion would be vacuous")
	}
	if !columnExists(t, s, "agent_profiles", "urn") {
		t.Fatal("agent_profiles.urn missing at schema 158 — the rename assertion would be vacuous")
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up (replay 159 over planted rows): %v", err)
	}

	// ── The backfill ran.
	seen := map[string]string{}
	for _, id := range ids {
		var urn string
		if err := s.DB.QueryRowContext(ctx,
			`SELECT urn FROM durable_agent_instances WHERE id = ?`, id).Scan(&urn); err != nil {
			t.Fatalf("read urn for %s: %v", id, err)
		}
		if urn == "" {
			t.Errorf("instance %s has an empty urn after 159 — an upgraded install would "+
				"carry addressless instances", id)
			continue
		}
		if !strings.HasPrefix(urn, "msg://agent/nanite/") {
			t.Errorf("instance %s urn = %q, want the nanite authority — agent-mux is Tether's", id, urn)
		}
		if prev, dup := seen[urn]; dup {
			t.Errorf("instances %s and %s share urn %q; two instances of one profile are two "+
				"correspondents", prev, id, urn)
		}
		seen[urn] = id
	}

	// ── Deterministic: re-running the backfill logic reproduces the same URN
	// rather than minting a second actor for the same instance. This is why
	// the migration derives from the id instead of using rand.
	var again string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT 'msg://agent/nanite/agt_' || lower(replace(id, '-', ''))
		   FROM durable_agent_instances WHERE id = ?`, ids[0]).Scan(&again); err != nil {
		t.Fatalf("re-derive: %v", err)
	}
	var stored string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT urn FROM durable_agent_instances WHERE id = ?`, ids[0]).Scan(&stored); err != nil {
		t.Fatalf("read stored urn: %v", err)
	}
	if again != stored {
		t.Errorf("backfill is not deterministic: stored %q, re-derived %q — a restore then "+
			"re-migrate would mint a second set of actors", stored, again)
	}

	// ── The profile URN is retired, not merely unused.
	if columnExists(t, s, "agent_profiles", "urn") {
		t.Error("agent_profiles.urn still exists after 159; the address was not retired")
	}
	if columnExists(t, s, "agent_profiles", "urn_aliases") {
		t.Error("agent_profiles.urn_aliases still exists after 159")
	}
	if !columnExists(t, s, "agent_profiles", "legacy_urn") {
		t.Error("agent_profiles.legacy_urn missing — the rename dropped the history it was " +
			"supposed to preserve")
	}

	// Re-running must be a clean no-op.
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("re-migrate after 159 already applied: %v", err)
	}
}

// TestMigration159_PreservesLegacyURNValues pins the reason the retirement is a
// rename and not a DROP: the 70 strings the old minter wrote survive, so what
// was minted into Tether's authority can still be reconciled.
func TestMigration159_PreservesLegacyURNValues(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	provider := gooseProviderFor(t, s)

	if _, err := provider.DownTo(ctx, 158); err != nil {
		t.Fatalf("goose DownTo 158: %v", err)
	}
	agent := makeTestAgent(t, s, "mig159-legacy")
	const oldURN = "msg://agent/agent-mux/agt_abcdefghij"
	const oldAliases = `["msg://agent/agent-mux/mig159-legacy"]`
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE agent_profiles SET urn = ?, urn_aliases = ? WHERE id = ?`,
		oldURN, oldAliases, agent.ID); err != nil {
		t.Fatalf("plant pre-159 profile URN: %v", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up: %v", err)
	}

	var gotURN, gotAliases string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT legacy_urn, legacy_urn_aliases FROM agent_profiles WHERE id = ?`,
		agent.ID).Scan(&gotURN, &gotAliases); err != nil {
		t.Fatalf("read legacy columns: %v", err)
	}
	if gotURN != oldURN {
		t.Errorf("legacy_urn = %q, want %q preserved", gotURN, oldURN)
	}
	if gotAliases != oldAliases {
		t.Errorf("legacy_urn_aliases = %q, want %q preserved", gotAliases, oldAliases)
	}
}

// plantPre159Instance inserts a durable instance using only columns that exist
// at schema 158. It deliberately does not call CreateDurableAgentInstance,
// which writes the current shape and therefore cannot run against a rolled-back
// schema.
func plantPre159Instance(t *testing.T, s *Store, id, profileID string) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO durable_agent_instances
		     (id, name, slug, profile_id, lifecycle_class, provider, model,
		      runtime_kind, launch_source_type, launch_source_id, work_root,
		      status, current_session_id, failure_reason, metadata_json,
		      created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, '', '', 'api', ?, '', '', ?, '', '', '{}',
		         '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z')`,
		id, "instance "+id, id, profileID,
		DurableAgentClassAdvisor, DurableAgentLaunchDurableAdvisor,
		DurableAgentStatusSleeping); err != nil {
		t.Fatalf("plant pre-159 instance %s: %v", id, err)
	}
}

func columnExists(t *testing.T, s *Store, table, column string) bool {
	t.Helper()
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		if name == column {
			return true
		}
	}
	return false
}
