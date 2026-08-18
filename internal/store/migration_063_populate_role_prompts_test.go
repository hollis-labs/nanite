package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigration063_SeedsFiveRolePrompts is the acceptance smoke for
// CW-20260512-0113 Wave 4: migration 063 INSERT-OR-IGNOREs the five role
// rows (researcher, analyst, file-backend, backend, background-job) on a
// fresh database with source='internal' and a body matching the
// corresponding internal/agent/builtin/profiles/<slug>.md file.
//
// The original draft also seeded a `fragments-engine` historical-reference
// role; that slug was dropped in review round 1 per the Phase 2 / Track A
// nuke of the in-tree fragments-engine plugin (user memory:
// project_nanite_phase_2_scope).
//
// Test shape:
//  1. Open a fresh DB. Migrations 001-063 run in order; migration 061
//     seeds the four canonical slugs (default, worker, planner,
//     hint-selector), 062 ejects any non-internal rows (no-op on fresh
//     DB), and 063 seeds the five role slugs.
//  2. For each of the five expected slugs, assert source='internal',
//     source_ref='embedded:profiles/<slug>.md', and a non-empty body.
//  3. Spot-check role identity tokens to guard against accidental body
//     swaps and to provide the unit-test-stub smoke evidence described
//     in the CW-20260512-0113 boot prompt (§8).
//  4. Verify can_execute is set correctly per role: read-only profiles
//     (researcher, analyst) must be can_execute=false; execution
//     profiles (file-backend, backend, background-job) must be
//     can_execute=true.
func TestMigration063_SeedsFiveRolePrompts(t *testing.T) {
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
		// toolPermissionsContains, when non-empty, asserts the raw
		// tool_permissions JSON contains the substring. The deny-all
		// pattern for no-tools profiles (analyst) is the regression
		// target for PR #161 review round 2 (Copilot items A+B+D):
		// an empty allow_list is PERMISSIVE under
		// toolclient.ToolPermissions.CheckPermission, so a no-tools
		// classifier needs an explicit `deny_list: ["*"]` to actually
		// deny every tool.
		toolPermissionsContains string
	}
	cases := []want{
		{
			slug:           "researcher",
			canExecute:     false,
			identityTokens: []string{"Researcher agent", "read-only", "Cite", "path/to/file.go:line"},
		},
		{
			slug:                    "analyst",
			canExecute:              false,
			identityTokens:          []string{"Analyst agent", "one-shot classifier", "low_confidence"},
			toolPermissionsContains: `"deny_list":["*"]`,
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
			slug:           "background-job",
			canExecute:     true,
			identityTokens: []string{"Background Job agent", "async", "Idempotency", "terminal envelope"},
		},
	}

	for _, c := range cases {
		got, err := s.GetAgentBySlug(c.slug)
		if err != nil {
			t.Errorf("GetAgentBySlug %q after migration 063: %v", c.slug, err)
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
			t.Errorf("slug=%q: SystemPrompt is empty — migration 063 seed must include the body", c.slug)
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
		if c.toolPermissionsContains != "" {
			if !strings.Contains(got.ToolPermissions, c.toolPermissionsContains) {
				t.Errorf("slug=%q: ToolPermissions = %q, want substring %q (no-tools deny-all contract drifted?)",
					c.slug, got.ToolPermissions, c.toolPermissionsContains)
			}
		}
	}
}

// TestMigration063_AnalystDeniesAllTools is the behavioral assertion for the
// analyst no-tools contract: the seeded tool_permissions JSON must, when
// parsed and evaluated, deny a known tool. This complements the string
// containment check in TestMigration063_SeedsFiveRolePrompts by exercising
// the same matcher semantics the runtime uses (toolclient.MatchPattern).
//
// PR #161 review round 2 (Copilot item D): empty allow_list is PERMISSIVE
// under toolclient.ToolPermissions.CheckPermission — the deny check runs
// first and "*" matches every tool via the prefix-glob in MatchPattern
// (HasSuffix("*", "*") → true → HasPrefix(name, "") → true). Without this
// assertion, a regression to `{"allow_list":[]}` would silently re-permit
// every tool.
//
// Implementation note: the store package cannot import toolclient (toolclient
// imports store, so the reverse direction creates a cycle). The test
// re-implements the minimum matcher logic for the wildcard case rather than
// importing toolclient. The full matcher contract is tested at the
// toolclient layer (internal/toolclient/permissions_test.go) and again at
// the file-SoT layer (internal/agent/builtin/profiles_test.go) which can
// import toolclient.
func TestMigration063_AnalystDeniesAllTools(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fresh.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	got, err := s.GetAgentBySlug("analyst")
	if err != nil {
		t.Fatalf("GetAgentBySlug analyst: %v", err)
	}

	// Minimal-deny-check shape; mirrors toolclient.ToolPermissions but
	// avoids the import cycle.
	var perms struct {
		AllowList []string `json:"allow_list,omitempty"`
		DenyList  []string `json:"deny_list,omitempty"`
	}
	if err := json.Unmarshal([]byte(got.ToolPermissions), &perms); err != nil {
		t.Fatalf("unmarshal analyst ToolPermissions %q: %v", got.ToolPermissions, err)
	}

	// Match the toolclient.MatchPattern("*", name) wildcard semantics:
	// HasSuffix(pattern, "*") → HasPrefix(name, "") → true for any name.
	denyAll := false
	for _, pat := range perms.DenyList {
		if pat == "*" {
			denyAll = true
			break
		}
	}
	if !denyAll {
		t.Errorf("analyst DenyList = %v; want a wildcard \"*\" entry so a no-tools profile actually denies every tool (empty allow_list is PERMISSIVE under toolclient.CheckPermission)",
			perms.DenyList)
	}

	// Spot-check that a known canonical tool would be denied. Using the
	// dev_read name lifted from the researcher allow_list — any tool name
	// would match the "*" prefix-glob, dev_read is just a stable anchor.
	for _, pat := range perms.DenyList {
		if pat == "*" {
			// Equivalent to toolclient.MatchPattern("*", "dev_read") = true.
			return
		}
	}
	t.Error("analyst tool_permissions did not produce a deny-all match for dev_read — regression to permissive empty-allow_list?")
}

// TestMigration063_RolePromptsExcludeUniversalRules guards against role
// bodies re-introducing the universal grounding/refusal/verification rules
// that live in internal/chat/universal_rules.go (CW-20260512-0100 +
// CW-20260512-0114). Duplicating universal content here would undo the
// layering benefit and reopen the c160 fabrication regression.
func TestMigration063_RolePromptsExcludeUniversalRules(t *testing.T) {
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

	for _, slug := range []string{"researcher", "analyst", "file-backend", "backend", "background-job"} {
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
