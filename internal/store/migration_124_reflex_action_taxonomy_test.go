package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrate124SeedsTaxonomyLookupTables is the regression test for
// TASKS/reflex-taxonomy/01-taxonomy-schema-foundation.md: the three new
// lookup tables (reflex_action_categories, reflex_action_kinds,
// reflex_provenance_tiers) exist and are seeded with exactly the rows
// docs/engineering/architecture/10-reflex-action-taxonomy.md's Facet 1/2/3
// tables specify, byte-for-byte.
func TestMigrate124SeedsTaxonomyLookupTables(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// reflex_action_categories: exactly two rows.
	catRows, err := s.DB.QueryContext(ctx, `SELECT name FROM reflex_action_categories ORDER BY name`)
	if err != nil {
		t.Fatalf("query reflex_action_categories: %v", err)
	}
	var cats []string
	for catRows.Next() {
		var name string
		if err := catRows.Scan(&name); err != nil {
			t.Fatalf("scan reflex_action_categories: %v", err)
		}
		cats = append(cats, name)
	}
	catRows.Close()
	wantCats := []string{"execute_action", "system_message"}
	if !equalStrings(cats, wantCats) {
		t.Errorf("reflex_action_categories = %v, want %v", cats, wantCats)
	}

	// reflex_provenance_tiers: exactly three rows.
	tierRows, err := s.DB.QueryContext(ctx, `SELECT name FROM reflex_provenance_tiers ORDER BY name`)
	if err != nil {
		t.Fatalf("query reflex_provenance_tiers: %v", err)
	}
	var tiers []string
	for tierRows.Next() {
		var name string
		if err := tierRows.Scan(&name); err != nil {
			t.Fatalf("scan reflex_provenance_tiers: %v", err)
		}
		tiers = append(tiers, name)
	}
	tierRows.Close()
	wantTiers := []string{"operator", "plugin", "system"}
	if !equalStrings(tiers, wantTiers) {
		t.Errorf("reflex_provenance_tiers = %v, want %v", tiers, wantTiers)
	}

	// reflex_action_kinds: exactly six rows, matching the design doc's
	// per-kind reclassification table byte-for-byte.
	wantKinds := map[string]ReflexActionKind{
		"inject_reminder":   {Name: "inject_reminder", Category: "system_message", CombiningAlgorithm: "all_applicable", DefaultRecurrenceSeconds: nil},
		"force_tool_choice": {Name: "force_tool_choice", Category: "system_message", CombiningAlgorithm: "first_applicable", DefaultRecurrenceSeconds: nil},
		"send_message":      {Name: "send_message", Category: "execute_action", CombiningAlgorithm: "all_applicable", DefaultRecurrenceSeconds: nil},
		"add_schedule":      {Name: "add_schedule", Category: "execute_action", CombiningAlgorithm: "all_applicable", DefaultRecurrenceSeconds: nil},
		"halt_session":      {Name: "halt_session", Category: "execute_action", CombiningAlgorithm: "deny_overrides", DefaultRecurrenceSeconds: nil},
	}
	zero := int64(0)
	wantKinds["dispatch_to_agent"] = ReflexActionKind{Name: "dispatch_to_agent", Category: "execute_action", CombiningAlgorithm: "first_applicable", DefaultRecurrenceSeconds: &zero}

	var kindCount int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reflex_action_kinds`).Scan(&kindCount); err != nil {
		t.Fatalf("count reflex_action_kinds: %v", err)
	}
	if kindCount != 6 {
		t.Fatalf("reflex_action_kinds row count = %d, want 6", kindCount)
	}

	for name, want := range wantKinds {
		got, err := s.GetReflexActionKind(ctx, name)
		if err != nil {
			t.Fatalf("GetReflexActionKind(%q): %v", name, err)
		}
		if got.Category != want.Category || got.CombiningAlgorithm != want.CombiningAlgorithm {
			t.Errorf("GetReflexActionKind(%q) = %+v, want category=%q combining_algorithm=%q", name, got, want.Category, want.CombiningAlgorithm)
		}
		if (got.DefaultRecurrenceSeconds == nil) != (want.DefaultRecurrenceSeconds == nil) {
			t.Errorf("GetReflexActionKind(%q).DefaultRecurrenceSeconds = %v, want %v", name, ptrInt64String(got.DefaultRecurrenceSeconds), ptrInt64String(want.DefaultRecurrenceSeconds))
		} else if got.DefaultRecurrenceSeconds != nil && *got.DefaultRecurrenceSeconds != *want.DefaultRecurrenceSeconds {
			t.Errorf("GetReflexActionKind(%q).DefaultRecurrenceSeconds = %d, want %d", name, *got.DefaultRecurrenceSeconds, *want.DefaultRecurrenceSeconds)
		}
	}

	if _, err := s.GetReflexActionKind(ctx, "does_not_exist"); err != ErrReflexActionKindNotFound {
		t.Errorf("GetReflexActionKind(unknown) err = %v, want ErrReflexActionKindNotFound", err)
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after 124 already applied: %v", err)
	}
}

// TestMigrate124BackfillsProvenanceTierFromCreatedBy verifies the migration's
// one real data change: pre-existing agent_reflexes rows get provenance_tier
// backfilled from their created_by value per the rule
// TASKS/reflex-taxonomy/01-taxonomy-schema-foundation.md specifies
// (created_by = 'system' -> 'system', everything else -> 'operator', never
// 'plugin'). Exercised by rolling the DB back to just before 124, inserting
// probe rows under each created_by convention actually in use today, then
// replaying 124's Up and checking the backfilled value.
func TestMigrate124BackfillsProvenanceTierFromCreatedBy(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 123); err != nil {
		t.Fatalf("goose DownTo 123 (reverse migration 124): %v", err)
	}

	// Pre-124 shape has no provenance_tier column yet — confirm that, then
	// insert probe rows under each created_by convention this migration's
	// own comment documents as live: 'system' (base reflex seeder),
	// 'operator' (CRUD create path), and an 'operator:<reviewer>'-prefixed
	// value (ApprovePendingReflex's collapse).
	var probe sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT created_by FROM agent_reflexes LIMIT 1`).Scan(&probe); err != nil && err != sql.ErrNoRows {
		t.Fatalf("sanity check pre-124 agent_reflexes shape: %v", err)
	}
	var provenanceProbe string
	err = s.DB.QueryRowContext(ctx, `SELECT provenance_tier FROM agent_reflexes LIMIT 1`).Scan(&provenanceProbe)
	if err == nil {
		t.Fatal("provenance_tier column should not exist before 124 is applied")
	}

	insertProbe := func(id, createdBy string) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO agent_reflexes
			    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
			     action_kind, action_spec, status, priority, fired_count,
			     last_fired_at, created_at, created_by, opt_out_allowed)
			 VALUES (?, NULL, 'process', ?, 'event', '{"name":"probe"}',
			         'halt_session', '{"reason":"probe"}', 'active', 0, 0,
			         NULL, datetime('now'), ?, 0)`,
			id, id, createdBy,
		); err != nil {
			t.Fatalf("insert pre-124 probe row %s: %v", id, err)
		}
	}
	insertProbe("rfx-124-probe-system", "system")
	insertProbe("rfx-124-probe-operator", "operator")
	insertProbe("rfx-124-probe-approved", "operator:alice")

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up (replay migration 124): %v", err)
	}

	wantTier := map[string]string{
		"rfx-124-probe-system":   "system",
		"rfx-124-probe-operator": "operator",
		"rfx-124-probe-approved": "operator",
	}
	for id, want := range wantTier {
		row, err := s.GetAgentReflex(ctx, id)
		if err != nil {
			t.Fatalf("GetAgentReflex(%s): %v", id, err)
		}
		if row.ProvenanceTier != want {
			t.Errorf("row %s (created_by=%s): provenance_tier = %q, want %q", id, row.CreatedBy, row.ProvenanceTier, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ptrInt64String(v *int64) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *v)
}
