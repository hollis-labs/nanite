package store

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 158's backfill is invisible to every other test in this repo, and
// that is the reason this file exists.
//
// A test store migrates to head and only then seeds, so seeding writes the
// corrected bodies and there is never an old-body row for 158 to update. The
// migration therefore runs as a no-op in the whole suite, and a typo in either
// WHERE clause — the old body restated one character off — would update nothing,
// fail nothing, and leave every already-seeded install carrying the broken
// reminder forever. A silent no-op and a successful backfill produce identical
// output.
//
// So this rolls the schema back to 157, plants the exact pre-fix rows, migrates
// up, and asserts the rows moved. The planted bodies are the literals the
// migration matches on; if those drift apart, the plant still succeeds and the
// assertion goes red rather than vacuous.
const (
	oldProcessBody = "Mail in the inbox — call mux_message_inbox to triage before continuing."
	oldAdvisorBody = "Mail in the inbox — call mux_message_inbox to triage."
)

func TestMigration158_BackfillsAlreadySeededWakeOnMailBodies(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	provider := gooseProviderFor(t, s)

	if _, err := provider.DownTo(ctx, 157); err != nil {
		t.Fatalf("goose DownTo 157 (reverse migration 158): %v", err)
	}

	// Plant one system-seeded row per class, exactly as a pre-158 install holds
	// them. Both are hard failures: if either insert were refused, every
	// assertion below would pass against nothing.
	plantWakeOnMailRow(t, s, "process", oldProcessBody)
	plantWakeOnMailRow(t, s, "advisor", oldAdvisorBody)

	// Positive control on the pre-state itself.
	for _, tc := range []struct{ class, body string }{
		{"process", oldProcessBody},
		{"advisor", oldAdvisorBody},
	} {
		if got := wakeOnMailBody(t, s, tc.class); got != tc.body {
			t.Fatalf("pre-migration class=%s body = %q, want the planted old body %q — "+
				"the backfill assertion would be vacuous", tc.class, got, tc.body)
		}
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up (replay migration 158 over planted rows): %v", err)
	}

	for _, class := range []string{"process", "advisor"} {
		got := wakeOnMailBody(t, s, class)
		if strings.Contains(got, "mux_") {
			t.Errorf("class=%s: body still names a mux_* tool after 158: %q", class, got)
		}
		if !strings.Contains(got, "message_inbox") {
			t.Errorf("class=%s: body does not name the read after 158: %q", class, got)
		}
		if !strings.Contains(got, "message_ack") {
			t.Errorf("class=%s: body does not name the discharge after 158, so an "+
				"already-seeded install keeps a reflex it cannot clear: %q", class, got)
		}
	}

	// Re-running must be a clean no-op — the WHERE clauses are scoped to the old
	// body, so a second pass has nothing left to match.
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("re-migrate after 158 already applied: %v", err)
	}
	for _, class := range []string{"process", "advisor"} {
		if got := wakeOnMailBody(t, s, class); !strings.Contains(got, "message_ack") {
			t.Errorf("class=%s: body lost its discharge on re-migrate: %q", class, got)
		}
	}
}

// TestMigration158_LeavesOperatorEditedBodiesAlone pins the other half of the
// scoping. The UPDATE matches on created_by = 'system' AND the exact old body,
// so a body an operator has already changed is not silently overwritten by an
// upgrade.
func TestMigration158_LeavesOperatorEditedBodiesAlone(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	provider := gooseProviderFor(t, s)

	if _, err := provider.DownTo(ctx, 157); err != nil {
		t.Fatalf("goose DownTo 157: %v", err)
	}

	const operatorBody = "Mail waiting. Follow our house triage runbook."
	plantWakeOnMailRowAs(t, s, "process", operatorBody, "operator")

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up: %v", err)
	}

	if got := wakeOnMailBody(t, s, "process"); got != operatorBody {
		t.Errorf("migration 158 rewrote an operator-authored body: got %q, want %q", got, operatorBody)
	}
}

func plantWakeOnMailRow(t *testing.T, s *Store, classTag, body string) {
	t.Helper()
	plantWakeOnMailRowAs(t, s, classTag, body, "system")
}

func plantWakeOnMailRowAs(t *testing.T, s *Store, classTag, body, createdBy string) {
	t.Helper()
	// Clear any row the pre-158 seed pass already wrote for this class so the
	// planted body is unambiguously the one under test.
	if _, err := s.DB.ExecContext(context.Background(),
		`DELETE FROM agent_reflexes WHERE name = 'wake_on_mail' AND class_tag = ?`, classTag); err != nil {
		t.Fatalf("clear existing wake_on_mail rows for class=%s: %v", classTag, err)
	}
	spec := `{"body":` + quoteJSONString(body) + `,"urgency":"info"}`
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO agent_reflexes
		   (class_tag, name, trigger_kind, trigger_spec, action_kind, action_spec,
		    priority, created_by, opt_out_allowed)
		 VALUES (?, 'wake_on_mail', 'event', '{"name":"mail_received"}', 'inject_reminder', ?, 50, ?, 1)`,
		classTag, spec, createdBy); err != nil {
		t.Fatalf("plant pre-158 wake_on_mail row for class=%s: %v", classTag, err)
	}
}

func wakeOnMailBody(t *testing.T, s *Store, classTag string) string {
	t.Helper()
	var body string
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT json_extract(action_spec, '$.body') FROM agent_reflexes
		  WHERE name = 'wake_on_mail' AND class_tag = ?`, classTag).Scan(&body)
	if err != nil {
		t.Fatalf("read wake_on_mail body for class=%s: %v", classTag, err)
	}
	return body
}

// quoteJSONString renders a Go string as a JSON string literal. The bodies
// carry an em dash and no quotes or backslashes, so this stays a narrow helper
// rather than pulling encoding/json in for one field.
func quoteJSONString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func gooseProviderFor(t *testing.T, s *Store) *goose.Provider {
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
