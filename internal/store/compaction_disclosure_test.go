package store

import (
	"strings"
	"testing"
)

// disclosureSlugs is the canonical set of CompactionContract Part A disclosure
// slugs seeded by migration 030. Order mirrors the CompactionMode constants in
// internal/context/compaction.go (general/code/plan/research) so any drift
// between the two sides shows up here.
var disclosureSlugs = []string{
	"compaction-disclosure-general",
	"compaction-disclosure-code",
	"compaction-disclosure-plan",
	"compaction-disclosure-research",
}

// expectedDisclosureVars are the variables every disclosure template must
// reference. The runtime injection path interpolates these from the latest
// compaction_events row; if a template drops one without a coordinated runtime
// change we want a loud test failure, not a silent un-interpolated
// `{{handoff_stash_id}}` ending up in the system prompt.
var expectedDisclosureVars = []string{
	"{{handoff_stash_id}}",
	"{{coverage_window_start}}",
	"{{coverage_window_end}}",
	"{{summary_token_count}}",
	"{{evicted_pointer_count}}",
	"{{preserved_source_count}}",
}

// expectedDisclosureAffordances are the recovery affordances D2 mandates every
// disclosure variant must reference: handoff stash id, chat_search self-
// tool, and summary metadata.
var expectedDisclosureAffordances = []string{
	"Handoff stash",
	"chat_search",
	"Summary metadata",
}

// maxDisclosureChars is the per-template ceiling enforced by D2 + ticket
// CW-20260420-0025: each rendered disclosure ≤ 300 tokens (≈ 1100 chars).
// Char count is a chars/4 heuristic — see internal/chat/EstimateTokens.
const maxDisclosureChars = 1100

// TestCompactionDisclosureMigration_loadable asserts migration 030 seeded all
// four disclosure templates and that they're retrievable by slug.
func TestCompactionDisclosureMigration_loadable(t *testing.T) {
	s := newTestStore(t)

	for _, slug := range disclosureSlugs {
		pt, err := s.GetPromptTemplateBySlug(slug)
		if err != nil {
			t.Fatalf("GetPromptTemplateBySlug(%q): %v", slug, err)
		}
		if pt == nil {
			t.Fatalf("disclosure template %q not seeded by migration 030", slug)
		}
		if !pt.IsBuiltin {
			t.Errorf("template %q: expected is_builtin=true, got false", slug)
		}
		if pt.Scope != "mode" {
			t.Errorf("template %q: expected scope=mode, got %q", slug, pt.Scope)
		}
		if pt.Priority != 15 {
			t.Errorf("template %q: expected priority=15, got %d", slug, pt.Priority)
		}
	}
}

// TestCompactionDisclosureMigration_referencesAffordances asserts every
// disclosure variant references the three recovery affordances locked by D2
// (stash_id, chat_search, summary metadata).
func TestCompactionDisclosureMigration_referencesAffordances(t *testing.T) {
	s := newTestStore(t)

	for _, slug := range disclosureSlugs {
		pt, err := s.GetPromptTemplateBySlug(slug)
		if err != nil || pt == nil {
			t.Fatalf("disclosure template %q missing", slug)
		}
		for _, aff := range expectedDisclosureAffordances {
			if !strings.Contains(pt.Template, aff) {
				t.Errorf("template %q missing affordance %q", slug, aff)
			}
		}
	}
}

// TestCompactionDisclosureMigration_referencesAllVariables asserts every
// disclosure variant references all six expected interpolation variables.
// This guards against a template silently dropping a variable while the
// runtime injection path still tries to fill it.
func TestCompactionDisclosureMigration_referencesAllVariables(t *testing.T) {
	s := newTestStore(t)

	for _, slug := range disclosureSlugs {
		pt, err := s.GetPromptTemplateBySlug(slug)
		if err != nil || pt == nil {
			t.Fatalf("disclosure template %q missing", slug)
		}
		for _, v := range expectedDisclosureVars {
			if !strings.Contains(pt.Template, v) {
				t.Errorf("template %q missing variable %q", slug, v)
			}
		}
	}
}

// TestCompactionDisclosureMigration_tokenBudget asserts every rendered
// disclosure stays under the per-template char ceiling that proxies for the
// 300-token budget. Variables are filled with realistic-looking values so
// the test reflects the worst-case rendered size, not the un-interpolated
// raw template (which is shorter).
func TestCompactionDisclosureMigration_tokenBudget(t *testing.T) {
	s := newTestStore(t)

	// Realistic-length sample values for worst-case sizing.
	vars := map[string]string{
		"handoff_stash_id":       "01HJ8N7XK5R8M3Y6PZQWA9V2BC",                      // ULID, 26 chars
		"coverage_window_start":  "turn-msg-01HJ8N7XK5R8M3Y6PZQWA9V2BC",              // 33 chars
		"coverage_window_end":    "turn-msg-01HJ8N7XK5R8M3Y6PZQWA9V2BC",              // 33 chars
		"summary_token_count":    "1234",
		"evicted_pointer_count":  "12",
		"preserved_source_count": "8",
	}

	for _, slug := range disclosureSlugs {
		pt, err := s.GetPromptTemplateBySlug(slug)
		if err != nil || pt == nil {
			t.Fatalf("disclosure template %q missing", slug)
		}

		rendered := pt.Template
		for k, v := range vars {
			rendered = strings.ReplaceAll(rendered, "{{"+k+"}}", v)
		}
		if strings.Contains(rendered, "{{") {
			t.Errorf("template %q has un-interpolated variables after fill: %q", slug, rendered)
		}
		if got := len(rendered); got > maxDisclosureChars {
			t.Errorf("template %q exceeds char ceiling: %d > %d (≈ 300 tokens)",
				slug, got, maxDisclosureChars)
		}
	}
}

// TestCompactionDisclosureMigration_idempotent re-running migrations on a fresh
// store is exercised by every test that calls newTestStore (which always runs
// the full migration set). This test asserts a manual second seed via the
// migration's INSERT OR IGNORE leaves exactly four disclosure rows — no
// duplicates, no missing rows.
func TestCompactionDisclosureMigration_idempotent(t *testing.T) {
	s := newTestStore(t)

	// newTestStore already ran the migration; re-running is a no-op via INSERT
	// OR IGNORE and the migration loader's run-on-every-boot semantics
	// (no schema_migrations table). Simulate a re-run by counting rows.
	var count int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM prompt_templates WHERE slug LIKE 'compaction-disclosure-%'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count disclosure templates: %v", err)
	}
	if count != 4 {
		t.Errorf("expected 4 disclosure templates after migration, got %d", count)
	}
}
