package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// legacyStatus is the pre-148 British spelling this migration removes. It is
// named once, here, rather than repeated as a Go literal across every
// assertion: a test that verifies a value's removal has to contain that value
// somewhere, and one clearly-labeled occurrence per file is the smallest
// honest form of that. (Deliberately NOT hidden from the misspell gate behind
// string concatenation or a //nolint — an evaded finding and a fixed one are
// not the same thing. The occurrences inside the SQL below are single-quoted,
// which misspell's word-boundary rule does not flag, so this constant is the
// only one the gate sees in this file.)
const legacyStatus = "cancelled"

// canceledBackfillColumn names one column migration 148 rewrites, together
// with every statement this test needs against it.
//
// The SQL is stored whole rather than assembled at the call site on purpose:
// a table or column name cannot be a bind parameter, so building these by
// concatenation at the point of Exec is both a gosec G202 finding and the
// habit that finding exists to discourage. Written out once per column, they
// are static strings with no interpolation at all.
type canceledBackfillColumn struct {
	label string
	// plant inserts one row holding the pre-148 spelling. It runs against the
	// schema rolled back to 147, where that spelling is still CHECK-legal.
	plant string
	// countLegacy / countNew / countAll are scalar COUNT(*) queries.
	countLegacy string
	countNew    string
	countAll    string
	// setLegacy / setNew retarget one existing row. After 148 the first must
	// be refused by the rebuilt CHECK and the second must be accepted; a
	// column with no CHECK leaves these empty and is skipped.
	setLegacy string
	setNew    string
}

// migration148Columns is the full set of columns migration 148 touches.
//
// The four CHECK-carrying tables come from the migration ledger
// (subagent_runs/068, workflow_runs/144, goals/138, loop_runs/141). The last
// two carry no CHECK and are the ones a ledger-only reading misses:
// nanite_recovery_breadcrumbs.outcome states its vocabulary only in a trailing
// comment (055), and envelope_instances.response_status states it only in Go
// (chat.ResponseStatus). They are listed here precisely so a future widening
// of this migration cannot silently drop them.
var migration148Columns = []canceledBackfillColumn{
	{
		label:       "goals.status",
		plant:       `INSERT INTO goals (id, intent, status) SELECT 'mig148-goal', 'probe', 'cancelled'`,
		countLegacy: `SELECT COUNT(*) FROM goals WHERE status = 'cancelled'`,
		countNew:    `SELECT COUNT(*) FROM goals WHERE status = 'canceled'`,
		countAll:    `SELECT COUNT(*) FROM goals`,
		setLegacy:   `UPDATE goals SET status = 'cancelled' WHERE rowid = (SELECT MIN(rowid) FROM goals)`,
		setNew:      `UPDATE goals SET status = 'canceled' WHERE rowid = (SELECT MIN(rowid) FROM goals)`,
	},
	{
		label: "loop_runs.status",
		plant: `INSERT INTO loop_runs (id, goal_id, definition_name, status, started_at, updated_at)
		        SELECT 'mig148-loop', 'mig148-goal', 'probe', 'cancelled', datetime('now'), datetime('now')`,
		countLegacy: `SELECT COUNT(*) FROM loop_runs WHERE status = 'cancelled'`,
		countNew:    `SELECT COUNT(*) FROM loop_runs WHERE status = 'canceled'`,
		countAll:    `SELECT COUNT(*) FROM loop_runs`,
		setLegacy:   `UPDATE loop_runs SET status = 'cancelled' WHERE rowid = (SELECT MIN(rowid) FROM loop_runs)`,
		setNew:      `UPDATE loop_runs SET status = 'canceled' WHERE rowid = (SELECT MIN(rowid) FROM loop_runs)`,
	},
	{
		label: "workflow_runs.status",
		plant: `INSERT INTO workflow_runs (id, definition_name, status, started_at)
		        SELECT 'mig148-wfrun', 'probe', 'cancelled', datetime('now')`,
		countLegacy: `SELECT COUNT(*) FROM workflow_runs WHERE status = 'cancelled'`,
		countNew:    `SELECT COUNT(*) FROM workflow_runs WHERE status = 'canceled'`,
		countAll:    `SELECT COUNT(*) FROM workflow_runs`,
		setLegacy:   `UPDATE workflow_runs SET status = 'cancelled' WHERE rowid = (SELECT MIN(rowid) FROM workflow_runs)`,
		setNew:      `UPDATE workflow_runs SET status = 'canceled' WHERE rowid = (SELECT MIN(rowid) FROM workflow_runs)`,
	},
	{
		label: "subagent_runs.status",
		plant: `INSERT INTO subagent_runs (id, parent_session_id, status, created_at)
		        SELECT 'mig148-subrun', 'probe-parent', 'cancelled', datetime('now')`,
		countLegacy: `SELECT COUNT(*) FROM subagent_runs WHERE status = 'cancelled'`,
		countNew:    `SELECT COUNT(*) FROM subagent_runs WHERE status = 'canceled'`,
		countAll:    `SELECT COUNT(*) FROM subagent_runs`,
		setLegacy:   `UPDATE subagent_runs SET status = 'cancelled' WHERE rowid = (SELECT MIN(rowid) FROM subagent_runs)`,
		setNew:      `UPDATE subagent_runs SET status = 'canceled' WHERE rowid = (SELECT MIN(rowid) FROM subagent_runs)`,
	},
	{
		label: "nanite_recovery_breadcrumbs.outcome",
		plant: `INSERT INTO nanite_recovery_breadcrumbs (timestamp, session_id, class, remediation, action, outcome)
		        SELECT datetime('now'), 'probe-session', 'transient', 'none', 'retry_transient', 'cancelled'`,
		countLegacy: `SELECT COUNT(*) FROM nanite_recovery_breadcrumbs WHERE outcome = 'cancelled'`,
		countNew:    `SELECT COUNT(*) FROM nanite_recovery_breadcrumbs WHERE outcome = 'canceled'`,
		countAll:    `SELECT COUNT(*) FROM nanite_recovery_breadcrumbs`,
	},
	{
		label: "envelope_instances.response_status",
		plant: `INSERT INTO envelope_instances (id, session_id, envelope_type, envelope_json, emitted_at, response_status)
		        SELECT 'mig148-env', (SELECT id FROM sessions LIMIT 1), 'approval-card', '{}', datetime('now'), 'cancelled'`,
		countLegacy: `SELECT COUNT(*) FROM envelope_instances WHERE response_status = 'cancelled'`,
		countNew:    `SELECT COUNT(*) FROM envelope_instances WHERE response_status = 'canceled'`,
		countAll:    `SELECT COUNT(*) FROM envelope_instances`,
	},
}

// The envelope_response message prefix chat.FormatEnvelopeResponseContent
// stamps, which migration 148 also retags. The status token is a bind
// parameter rather than an inline literal — unlike a table or column name it
// legally can be one, so both spellings flow through statusPrefixPattern.
const (
	countEnvelopeMessages = `SELECT COUNT(*) FROM messages
		WHERE role = 'envelope_response' AND content LIKE ?`
	plantEnvelopeMessage = `INSERT INTO messages (id, session_id, role, content)
		SELECT 'mig148-msg', (SELECT id FROM sessions LIMIT 1), 'envelope_response', ?`
)

// statusPrefixPattern builds the LIKE pattern matching the machine-written
// "[envelope:<type> status:<status>] " prefix for one status spelling.
func statusPrefixPattern(status string) string {
	return "[envelope:% status:" + status + "] %"
}

// TestMigrate148BackfillsCanceledStatusOverRealBackup is the regression test
// for Torque CW-20260824-0027's Part B: migration 148 must both rebuild the
// four status CHECK constraints around 'canceled' AND rewrite every row still
// carrying the old spelling.
//
// It runs against a copy of a real operator backup rather than a synthetic
// fixture, per the precedent
// TestRealBackupWorkflowRunStepsSurviveLoopKindMigration already sets: the
// real backup supplies the pre-existing rows whose survival across four table
// rebuilds is the other half of what this migration must not break.
//
// The old-spelling rows themselves are planted rather than found, and that is
// load-bearing rather than a convenience. Every reachable database on this
// machine holds ZERO old-spelling rows in all six columns (measured directly
// against both the live main.db and this backup), so a test that merely ran
// the migration and counted would assert a 0 -> 0 transition and still pass
// with every backfill statement deleted. Rolling the schema back to 147,
// planting rows at the version where the old spelling is still CHECK-legal,
// then replaying 148 forward is the only shape that actually exercises the
// rewrite. Same reasoning, and the same DownTo/Up boundary technique, as
// migration 139's real-backup test.
func TestMigrate148BackfillsCanceledStatusOverRealBackup(t *testing.T) {
	rs := openRealBackupCopy(t)
	ctx := context.Background()

	// Row totals captured at head. Every one of these rows has to come through
	// four create-copy-drop-rename rebuilds unchanged in count — that is the
	// half of this migration that has nothing to do with spelling.
	realTotals := make([]int, len(migration148Columns))
	for i, col := range migration148Columns {
		realTotals[i] = countRows(t, rs, col.countAll)
	}
	if realTotals[3] == 0 {
		t.Fatal("real backup copy has 0 subagent_runs rows; this test needs a populated backup, not an empty fixture")
	}
	t.Logf("real backup copy row totals at head: %v", realTotals)

	provider := newMigrationProvider(t, rs)
	if _, err := provider.DownTo(ctx, 147); err != nil {
		t.Fatalf("goose DownTo 147 (reverse migration 148 on real backup copy): %v", err)
	}

	before := plantLegacyRowsAt147(t, rs)
	beforeMessages := countRows(t, rs, countEnvelopeMessages, statusPrefixPattern(legacyStatus))
	if beforeMessages == 0 {
		t.Fatal("0 envelope_response messages carry the legacy status prefix before migration 148 — assertion would be vacuous")
	}
	t.Logf("BEFORE migration 148: legacy row counts %v, envelope_response messages %d", before, beforeMessages)

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up (replay migration 148 over real backup copy + planted rows): %v", err)
	}

	assertLegacyRowsRewritten(t, rs, before, realTotals)
	assertEnvelopeMessagesRetagged(t, rs, beforeMessages)
	assertCheckConstraintsMoved(t, rs)

	assertGooseHasNothingPending(t, rs)

	// Simulated restart: a second full migrate() must be a clean no-op.
	if err := rs.migrate(ctx); err != nil {
		t.Fatalf("re-migrate after 148 already applied: %v", err)
	}
}

// openRealBackupCopy copies the operator's real backup into t.TempDir() and
// opens it, running every migration. The backup file itself is never opened in
// place — it is a real operator artifact, so the test skips rather than fails
// when it is not present on this machine.
func openRealBackupCopy(t *testing.T) *Store {
	t.Helper()
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		t.Skipf("cannot resolve home directory: %v", homeErr)
	}
	backupSrc := filepath.Join(home, ".local", "share", "nanite", "workspaces", "default", "backups",
		"main.db.pre-execution-backup-20260818-132726")
	if _, statErr := os.Stat(backupSrc); statErr != nil {
		t.Skipf("real backup db not present at %s (skipping live-backup verification): %v", backupSrc, statErr)
	}

	dstPath := filepath.Join(t.TempDir(), "main.db")
	if copyErr := copyFile(dstPath, backupSrc); copyErr != nil {
		t.Fatalf("copy real backup db to scratch path: %v", copyErr)
	}
	if walInfo, walErr := os.Stat(backupSrc + "-wal"); walErr == nil && walInfo.Size() > 0 {
		if copyErr := copyFile(dstPath+"-wal", backupSrc+"-wal"); copyErr != nil {
			t.Fatalf("copy real backup -wal to scratch path: %v", copyErr)
		}
	}
	absPath, absErr := filepath.Abs(dstPath)
	if absErr != nil {
		t.Fatalf("resolve scratch db path: %v", absErr)
	}

	rs, openErr := New(context.Background(), absPath)
	if openErr != nil {
		t.Fatalf("open+migrate scratch copy of real backup db: %v", openErr)
	}
	t.Cleanup(func() { rs.Close(context.Background()) })
	return rs
}

func newMigrationProvider(t *testing.T, s *Store) *goose.Provider {
	t.Helper()
	migrationsDir, subErr := fs.Sub(migrationsFS, "migrations")
	if subErr != nil {
		t.Fatalf("sub migrations fs: %v", subErr)
	}
	provider, provErr := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if provErr != nil {
		t.Fatalf("construct goose provider: %v", provErr)
	}
	return provider
}

// plantLegacyRowsAt147 inserts one old-spelling row per tracked column plus one
// envelope_response message, and returns the resulting legacy counts. Doubles
// as a positive control for the pre-148 schema: if any of these inserts were
// refused, every "the backfill moved my row" assertion downstream would be
// vacuous, so each is a hard failure.
func plantLegacyRowsAt147(t *testing.T, s *Store) []int {
	t.Helper()
	ctx := context.Background()
	for _, col := range migration148Columns {
		if _, err := s.DB.ExecContext(ctx, col.plant); err != nil {
			t.Fatalf("plant pre-148 legacy row for %s at schema version 147: %v", col.label, err)
		}
	}
	content := "[envelope:approval-card status:" + legacyStatus + "] {}"
	if _, err := s.DB.ExecContext(ctx, plantEnvelopeMessage, content); err != nil {
		t.Fatalf("plant pre-148 envelope_response message at schema version 147: %v", err)
	}

	counts := make([]int, len(migration148Columns))
	for i, col := range migration148Columns {
		counts[i] = countRows(t, s, col.countLegacy)
		if counts[i] == 0 {
			t.Fatalf("%s has 0 legacy-spelled rows before migration 148 — the backfill assertion would be vacuous", col.label)
		}
	}
	return counts
}

func assertLegacyRowsRewritten(t *testing.T, s *Store, before, realTotals []int) {
	t.Helper()
	for i, col := range migration148Columns {
		if got := countRows(t, s, col.countLegacy); got != 0 {
			t.Errorf("%s still has %d legacy-spelled row(s) after migration 148, want 0", col.label, got)
		}
		if got := countRows(t, s, col.countNew); got != before[i] {
			t.Errorf("%s has %d row(s) spelled 'canceled' after migration 148, want %d (the pre-migration legacy count)",
				col.label, got, before[i])
		}
		// The rebuild must not lose the real backup's pre-existing rows:
		// the head total plus the one row this test planted.
		if got, want := countRows(t, s, col.countAll), realTotals[i]+1; got != want {
			t.Errorf("%s holds %d rows after migration 148, want %d (real backup rows must survive the rebuild)",
				col.label, got, want)
		}
	}
}

func assertEnvelopeMessagesRetagged(t *testing.T, s *Store, before int) {
	t.Helper()
	if got := countRows(t, s, countEnvelopeMessages, statusPrefixPattern(legacyStatus)); got != 0 {
		t.Errorf("%d envelope_response message(s) still carry the legacy status prefix after migration 148, want 0", got)
	}
	got := countRows(t, s, countEnvelopeMessages, statusPrefixPattern("canceled"))
	if got != before {
		t.Errorf("%d envelope_response message(s) carry the 'canceled' status prefix after migration 148, want %d", got, before)
	}
	t.Logf("AFTER migration 148: every planted row rewritten; %d envelope_response message(s) retagged", got)
}

// assertCheckConstraintsMoved proves the constraints themselves moved, not just
// the data. Both directions are asserted for every CHECK-carrying column:
// asserting only the rejection would also pass against a table whose CHECK
// forbade every value, and asserting only the acceptance would pass against a
// table with no CHECK at all.
func assertCheckConstraintsMoved(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, col := range migration148Columns {
		if col.setLegacy == "" {
			continue // no CHECK on this column by design
		}
		if _, err := s.DB.ExecContext(ctx, col.setNew); err != nil {
			t.Errorf("%s = 'canceled' rejected after migration 148, want accepted: %v", col.label, err)
		}
		if _, err := s.DB.ExecContext(ctx, col.setLegacy); err == nil {
			t.Errorf("%s = legacy spelling was accepted after migration 148, want a CHECK violation", col.label)
		}
	}
}

// countRows runs a single scalar COUNT(*) query and closes its cursor before
// returning. rs's pool is a single connection (sqlitekit.OpenSingle), so a
// second query issued while an outer *sql.Rows is still open deadlocks —
// QueryRowContext+Scan is the shape this package uses to avoid that.
func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}
