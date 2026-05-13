package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigration062_SeedsSixRolePrompts is the acceptance smoke for
// CW-20260512-0113 Wave 4: migration 062 INSERT-OR-IGNOREs the six role
// rows (researcher, analyst, file-backend, backend, fragments-engine,
// background-job) on a fresh database with source='internal' and a body
// matching the corresponding internal/agent/builtin/profiles/<slug>.md
// file.
//
// Test shape:
//  1. Open a fresh DB. Migrations 001-062 run in order; migration 060
//     seeds the four canonical slugs (default, worker, planner,
//     hint-selector), 061 ejects any non-internal rows (no-op on fresh
//     DB), and 062 seeds the six role slugs.
//  2. For each of the six expected slugs, assert source='internal',
//     source_ref='embedded:profiles/<slug>.md', and a non-empty body.
//  3. Spot-check role identity tokens to guard against accidental body
//     swaps and to provide the unit-test-stub smoke evidence described
//     in the CW-20260512-0113 boot prompt (§8).
//  4. Verify can_execute is set correctly per role: read-only profiles
//     (researcher, analyst, fragments-engine) must be can_execute=false;
//     execution profiles (file-backend, backend, background-job) must
//     be can_execute=true.
func TestMigration062_SeedsSixRolePrompts(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fresh.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	type want struct {
		slug         string
		canExecute   bool
		identityTokens []string // role-identity tokens that must appear in the body
	}
	cases := []want{
		{
			slug:           "researcher",
			canExecute:     false,
			identityTokens: []string{"Researcher agent", "read-only", "Cite", "path/to/file.go:line"},
		},
		{
			slug:           "analyst",
			canExecute:     false,
			identityTokens: []string{"Analyst agent", "one-shot classifier", "low_confidence"},
		},
		{
			slug:           "file-backend",
			canExecute:     true,
			identityTokens: []string{"File Backend agent", "file-tier I/O", "dev_glob", "Migrations are immutable"},
		},
		{
			slug:           "backend",
			canExecute:     true,
			identityTokens: []string{"Backend agent", "Go server-side", "go test -race", "Migrations are append-only"},
		},
		{
			slug:           "fragments-engine",
			canExecute:     false,
			identityTokens: []string{"Fragments Engine agent", "Volon", "do not modify"},
		},
		{
			slug:           "background-job",
			canExecute:     true,
			identityTokens: []string{"Background Job agent", "async", "Idempotency", "terminal envelope"},
		},
	}

	for _, c := range cases {
		got, err := s.GetAgentBySlug(c.slug)
		if err != nil {
			t.Errorf("GetAgentBySlug %q after migration 062: %v", c.slug, err)
			continue
		}
		if got.Source != "internal" {
			t.Errorf("slug=%q: Source = %q, want 'internal'", c.slug, got.Source)
		}
		wantRef := "embedded:profiles/" + c.slug + ".md"
		if got.SourceRef != wantRef {
			t.Errorf("slug=%q: SourceRef = %q, want %q", c.slug, got.SourceRef, wantRef)
		}
		if got.SystemPrompt == "" {
			t.Errorf("slug=%q: SystemPrompt is empty — migration 062 seed must include the body", c.slug)
			continue
		}
		if got.CanExecute != c.canExecute {
			t.Errorf("slug=%q: CanExecute = %v, want %v", c.slug, got.CanExecute, c.canExecute)
		}
		for _, token := range c.identityTokens {
			if !strings.Contains(got.SystemPrompt, token) {
				t.Errorf("slug=%q: body missing identity token %q (role identity drifted from .md SOT?)", c.slug, token)
			}
		}
	}
}

// TestMigration062_RolePromptsExcludeUniversalRules guards against role
// bodies re-introducing the universal grounding/refusal/verification rules
// that live in internal/chat/universal_rules.go (CW-20260512-0100 +
// CW-20260512-0114). Duplicating universal content here would undo the
// layering benefit and reopen the c160 fabrication regression.
func TestMigration062_RolePromptsExcludeUniversalRules(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fresh.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Sentinels lifted verbatim from internal/chat/universal_rules.go.
	universalSentinels := []string{
		"## Universal rules",
		"Refuse rather than fabricate",
		"Acknowledge honestly when you fail",
		"Use what tools return",
		"Count, do not estimate",
	}

	for _, slug := range []string{"researcher", "analyst", "file-backend", "backend", "fragments-engine", "background-job"} {
		got, err := s.GetAgentBySlug(slug)
		if err != nil {
			t.Errorf("GetAgentBySlug %q: %v", slug, err)
			continue
		}
		for _, sentinel := range universalSentinels {
			if strings.Contains(got.SystemPrompt, sentinel) {
				t.Errorf("slug=%q: body duplicates universal sentinel %q — universal_rules.go owns this content", slug, sentinel)
			}
		}
	}
}
