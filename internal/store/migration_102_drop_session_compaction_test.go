package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrate102DropsSessionCompactionColumns is the regression test for
// TASKS/phase-0/26-cut-session-compaction-summary-fields.md: a fresh full
// migration run must leave sessions without compaction_summary/compacted_at,
// and a simulated restart (a second s.migrate() call) must be a clean no-op.
func TestMigrate102DropsSessionCompactionColumns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, col := range []string{"compaction_summary", "compacted_at"} {
		exists, err := sessionsColumnExists(ctx, s, col)
		if err != nil {
			t.Fatalf("check sessions.%s exists: %v", col, err)
		}
		if exists {
			t.Errorf("sessions.%s still exists after a fresh full migration run — migration 102 should have dropped it", col)
		}
	}

	// The sessions table must still be otherwise usable — dropping the two
	// columns must not touch the rest of the schema or any existing rows.
	var count int
	if err := s.DB.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("live sessions table unusable after 102: %v", err)
	}
	if count != 0 {
		t.Errorf("sessions row count: got %d, want 0 on a fresh store", count)
	}

	assertGooseHasNothingPending(t, s)

	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("re-migrate after columns already dropped: %v", err)
	}

	for _, col := range []string{"compaction_summary", "compacted_at"} {
		exists, err := sessionsColumnExists(ctx, s, col)
		if err != nil {
			t.Fatalf("check sessions.%s exists after re-migrate: %v", col, err)
		}
		if exists {
			t.Errorf("sessions.%s reappeared after a re-migrate — migration 102 must not be lost or reapplied on restart", col)
		}
	}
}

// TestMigrate102DownReaddsSessionCompactionColumns is the tested Down half
// of migration 102: goose's DownTo must be able to reverse this migration,
// re-adding both columns with their original nullable TEXT/DATETIME shape
// (see migrations/001_schema.sql and migrations/074_agent_profiles_multi_agent.sql).
func TestMigrate102DownReaddsSessionCompactionColumns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Confirm the Up side actually ran before exercising Down.
	for _, col := range []string{"compaction_summary", "compacted_at"} {
		exists, err := sessionsColumnExists(ctx, s, col)
		if err != nil {
			t.Fatalf("check sessions.%s exists before down: %v", col, err)
		}
		if exists {
			t.Fatalf("sessions.%s exists before down migration — Up did not drop it as expected", col)
		}
	}

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 101); err != nil {
		t.Fatalf("goose DownTo 101 (reverse migration 102): %v", err)
	}

	for _, col := range []string{"compaction_summary", "compacted_at"} {
		exists, err := sessionsColumnExists(ctx, s, col)
		if err != nil {
			t.Fatalf("check sessions.%s exists after down: %v", col, err)
		}
		if !exists {
			t.Errorf("sessions.%s missing after Down migration — 102's Down section should have re-added it", col)
		}
	}

	// The re-added columns must be writable and readable, not just present.
	_, err = s.DB.Exec(`INSERT INTO sessions (id, short_code, compaction_summary, compacted_at)
		VALUES ('sess-down-test', 'DOWN1', 'a summary', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert into re-added columns after down: %v", err)
	}
	var summary, compactedAt string
	if err := s.DB.QueryRow(`SELECT compaction_summary, compacted_at FROM sessions WHERE id = 'sess-down-test'`).
		Scan(&summary, &compactedAt); err != nil {
		t.Fatalf("read back re-added columns after down: %v", err)
	}
	if summary != "a summary" || compactedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("re-added columns round-trip: got (%q, %q), want (%q, %q)", summary, compactedAt, "a summary", "2026-01-01T00:00:00Z")
	}

	// Back up to the top so the store is left in its normal fully-migrated
	// state (mirrors the shape of a real down-then-back-up-again cycle).
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up after DownTo 101: %v", err)
	}
	for _, col := range []string{"compaction_summary", "compacted_at"} {
		exists, err := sessionsColumnExists(ctx, s, col)
		if err != nil {
			t.Fatalf("check sessions.%s exists after re-up: %v", col, err)
		}
		if exists {
			t.Errorf("sessions.%s reappeared after Up replayed 102 — Up should drop it again", col)
		}
	}
}

func sessionsColumnExists(ctx context.Context, s *Store, column string) (bool, error) {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(sessions)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return false, err
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return false, err
		}
		for i, c := range cols {
			if c == "name" {
				if name, ok := vals[i].(string); ok && name == column {
					return true, nil
				}
			}
		}
	}
	return false, rows.Err()
}
