package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrate105NarrowsSessionStatusCheck is the regression test for
// TASKS/phase-0/25-drop-unused-session-status-enum.md: a fresh full
// migration run must leave sessions.status's CHECK constraint accepting
// only active/paused/archived — sleeping/halted/terminated (added by
// 074_agent_profiles_multi_agent.sql, never written by any code path) must
// be rejected at the SQL layer. halted_at/halted_reason (a separate, real
// mechanism, see internal/store/session_halt.go) must be unaffected.
func TestMigrate105NarrowsSessionStatusCheck(t *testing.T) {
	s := newTestStore(t)

	sess := &Session{Title: "status-check-probe"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession after migration 105: %v", err)
	}

	// The three removed values must now be rejected at the SQL layer.
	for _, removed := range []string{"sleeping", "halted", "terminated"} {
		_, err := s.DB.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, removed, sess.ID)
		if err == nil {
			t.Errorf("raw UPDATE to removed status %q should have failed the narrowed CHECK constraint, got nil error", removed)
		}
	}

	// The three retained values must still round-trip normally.
	for _, kept := range []string{"active", "paused", "archived"} {
		if _, err := s.DB.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, kept, sess.ID); err != nil {
			t.Errorf("raw UPDATE to retained status %q unexpectedly rejected: %v", kept, err)
		}
		var got string
		if err := s.DB.QueryRow(`SELECT status FROM sessions WHERE id = ?`, sess.ID).Scan(&got); err != nil {
			t.Fatalf("read back status %q: %v", kept, err)
		}
		if got != kept {
			t.Errorf("status round-trip: got %q, want %q", got, kept)
		}
	}

	// halted_at/halted_reason must be untouched by this migration — the
	// monitor-loop halt mechanism (a distinct concept from the status enum
	// despite the shared word "halted") must keep working identically.
	if err := s.MarkSessionHalted(context.Background(), sess.ID, "circuit breaker tripped"); err != nil {
		t.Fatalf("MarkSessionHalted after migration 105: %v", err)
	}
	halt, err := s.GetSessionHalt(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionHalt after migration 105: %v", err)
	}
	if !halt.IsHalted() {
		t.Errorf("expected session to be halted after MarkSessionHalted")
	}
	if halt.HaltedReason == nil || *halt.HaltedReason != "circuit breaker tripped" {
		t.Errorf("halted_reason round-trip: got %v, want %q", halt.HaltedReason, "circuit breaker tripped")
	}
	if err := s.ClearSessionHalt(context.Background(), sess.ID); err != nil {
		t.Fatalf("ClearSessionHalt after migration 105: %v", err)
	}
	halt, err = s.GetSessionHalt(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionHalt after clear: %v", err)
	}
	if halt.IsHalted() {
		t.Errorf("expected session to no longer be halted after ClearSessionHalt")
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op and
	// the narrowed constraint must still hold afterward.
	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("re-migrate after 105 already applied: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET status = 'terminated' WHERE id = ?`, sess.ID); err == nil {
		t.Errorf("removed status 'terminated' accepted after re-migrate — narrowed CHECK must not be lost on restart")
	}
}

// TestMigrate105DownWidensSessionStatusCheck is the tested Down half of
// migration 105: goose's DownTo must be able to reverse this migration,
// restoring the original 6-value CHECK from
// 074_agent_profiles_multi_agent.sql, with the constraint actually enforced
// at the SQL layer again (not just structurally present).
func TestMigrate105DownWidensSessionStatusCheck(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sess := &Session{Title: "down-check-probe"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession before down: %v", err)
	}

	// Confirm the Up side actually narrowed the constraint before exercising Down.
	if _, err := s.DB.Exec(`UPDATE sessions SET status = 'sleeping' WHERE id = ?`, sess.ID); err == nil {
		t.Fatalf("status 'sleeping' accepted before down migration — Up did not narrow the CHECK as expected")
	}

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 104); err != nil {
		t.Fatalf("goose DownTo 104 (reverse migration 105): %v", err)
	}

	// The re-widened constraint must accept all six original values again.
	for _, val := range []string{"active", "paused", "archived", "sleeping", "halted", "terminated"} {
		if _, err := s.DB.Exec(`UPDATE sessions SET status = ? WHERE id = ?`, val, sess.ID); err != nil {
			t.Errorf("status %q rejected after Down — widened CHECK should accept it: %v", val, err)
		}
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET status = 'garbage' WHERE id = ?`, sess.ID); err == nil {
		t.Errorf("status 'garbage' accepted after Down — widened CHECK should still reject non-enum values")
	}

	// halted_at/halted_reason must survive the Down rebuild too.
	if err := s.MarkSessionHalted(context.Background(), sess.ID, "post-down probe"); err != nil {
		t.Fatalf("MarkSessionHalted after down: %v", err)
	}
	halt, err := s.GetSessionHalt(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionHalt after down: %v", err)
	}
	if !halt.IsHalted() || halt.HaltedReason == nil || *halt.HaltedReason != "post-down probe" {
		t.Errorf("halted fields did not survive Down rebuild: %+v", halt)
	}

	// Reset to a value the narrowed CHECK will accept before re-upping —
	// re-running 105's Up rebuilds the table via INSERT ... SELECT, which
	// would itself fail the narrowed CHECK if any row still held one of the
	// three removed values. (This is precisely the real-world hazard 105's
	// own Up migration guards against: a pre-existing out-of-range status
	// row would fail the migration loudly at deploy time, not silently.)
	if _, err := s.DB.Exec(`UPDATE sessions SET status = 'active' WHERE id = ?`, sess.ID); err != nil {
		t.Fatalf("reset status to 'active' before re-up: %v", err)
	}

	// Back up to the top so the store is left in its normal fully-migrated
	// state (mirrors the shape of a real down-then-back-up-again cycle).
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up after DownTo 103: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET status = 'sleeping' WHERE id = ?`, sess.ID); err == nil {
		t.Errorf("status 'sleeping' accepted after Up replayed 105 — Up should narrow the CHECK again")
	}
}
