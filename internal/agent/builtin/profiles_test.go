package builtin

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/chat"
)

// TestInternalProfiles_LoadsAllExpectedSlugs asserts the embedded profiles/
// directory carries the full internal profile set Nanite is expected to
// ingest at boot. If a new internal profile is added, update this list so
// the test remains an explicit inventory pin.
func TestInternalProfiles_LoadsAllExpectedSlugs(t *testing.T) {
	defs, err := InternalProfiles()
	if err != nil {
		t.Fatalf("InternalProfiles: %v", err)
	}
	got := make([]string, len(defs))
	for i, d := range defs {
		got[i] = d.Slug
	}
	sort.Strings(got)

	want := []string{
		// Harness primitives (the Chat/Planner/Worker core + infra):
		"default", "hint-selector", "planner", "worker",
		// Worker-family roles still referenced by the harness (dispatch /
		// prompt framing). Phase 2 migrated the zero-ref product/tooling
		// agents (analyst, code-auditor, file-backend, agent-builder,
		// agridd-project-manager, proxima, torque-supervisor,
		// torque-task-writer) out of the compiled-in inventory. Operators may
		// provision replacements through the database-backed management API.
		"backend", "background-job", "researcher",
		// Standing roles resolved via subagent_spawn / prompt framing.
		"reviewer", "system-architect",
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("slugs len: got %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("slug[%d] = %q, want %q (full got=%v)", i, got[i], w, got)
		}
	}
}

// TestInternalProfiles_SourceAndRef asserts every internal profile carries
// Source="internal" and a SourceRef that points back to its embedded path.
// The Wave 2 cleanup migration keys off Source value; SourceRef is embedded
// build provenance only and does not grant filesystem editability.
func TestInternalProfiles_SourceAndRef(t *testing.T) {
	defs, err := InternalProfiles()
	if err != nil {
		t.Fatalf("InternalProfiles: %v", err)
	}
	for _, def := range defs {
		if def.Source != SourceInternal {
			t.Errorf("slug=%s: Source=%q, want %q", def.Slug, def.Source, SourceInternal)
		}
		wantRef := "embedded:profiles/" + def.Slug + ".md"
		if def.SourceRef != wantRef {
			t.Errorf("slug=%s: SourceRef=%q, want %q", def.Slug, def.SourceRef, wantRef)
		}
		if def.Name == "" {
			t.Errorf("slug=%s: Name is empty", def.Slug)
		}
		if def.SystemPrompt == "" {
			t.Errorf("slug=%s: SystemPrompt is empty (parser left body unset)", def.Slug)
		}
	}
}

// TestInternalProfiles_DefaultIdentity guards the Chat-role identity body
// against accidental rewrites that would re-introduce the universal
// grounding rules (which now live in internal/chat/universal_rules.go).
// Migration 058 stripped these from already-deployed databases — adding
// them back here re-opens the c160 fabrication chain.
func TestInternalProfiles_DefaultIdentity(t *testing.T) {
	defs, err := InternalProfiles()
	if err != nil {
		t.Fatalf("InternalProfiles: %v", err)
	}
	def := findBySlug(t, defs, "default")
	if !strings.Contains(def.SystemPrompt, "Nanite chat harness") {
		t.Error("default profile missing chat-harness identity sentence")
	}
	if strings.Contains(def.SystemPrompt, "## Grounding") {
		t.Error("default profile must not carry the ## Grounding section — universal rules layer owns it")
	}
	if strings.Contains(def.SystemPrompt, "When you fail, acknowledge honestly") {
		t.Error("default profile must not carry the universal Refusal bullet — universal rules layer owns it")
	}
	if len(def.ParentDispatchAllowlist) != 3 {
		t.Errorf("default parentDispatchAllowlist len = %d, want 3 (researcher/planner/worker)", len(def.ParentDispatchAllowlist))
	}
}

// TestInternalProfiles_WorkerIdentity guards the Worker body against
// reintroducing the execute-or-bust framing that drove the c160
// fabrication chain (deep-dive §6). Migration 058 stripped it; the file
// SOT must not regress it.
func TestInternalProfiles_WorkerIdentity(t *testing.T) {
	defs, err := InternalProfiles()
	if err != nil {
		t.Fatalf("InternalProfiles: %v", err)
	}
	def := findBySlug(t, defs, "worker")
	if strings.Contains(def.SystemPrompt, "Your job is to execute, not converse") {
		t.Error("worker profile must not carry execute-or-bust framing — universal Refusal owns failure-affordance")
	}
	if !strings.Contains(def.SystemPrompt, "return an explicit failure") {
		t.Error("worker profile must reference the explicit-failure escape valve")
	}
}

// TestInternalProfiles_FrontmatterKeys is the regression pin for PR-152
// review round 1 (item A): the parser recognizes `model:` and
// `permissionMode:`, not `defaultModel:`. A synthetic frontmatter block is
// used (rather than reading the shipped profiles) because CW-20260815-0021
// deliberately removed `model:` from every internal profile so they inherit
// the system default via ResolveProviderAndModel instead of hardcoding a
// model ID that eventually gets retired.
func TestInternalProfiles_FrontmatterKeys(t *testing.T) {
	def, err := agent.ParseMD([]byte(`---
name: Synthetic
slug: synthetic
description: frontmatter-key regression fixture
model: claude-test-model
permissionMode: yolo
---
body
`))
	if err != nil {
		t.Fatalf("ParseMD: %v", err)
	}
	if def.Model != "claude-test-model" {
		t.Errorf("Model = %q, want %q — frontmatter key likely wrong (must be `model:`, not `defaultModel:`)", def.Model, "claude-test-model")
	}
	if def.PermissionMode != "yolo" {
		t.Errorf("PermissionMode = %q, want %q", def.PermissionMode, "yolo")
	}

	defs, err := InternalProfiles()
	if err != nil {
		t.Fatalf("InternalProfiles: %v", err)
	}
	// CW-20260815-0021: none of the shipped internal profiles should
	// hardcode a model — a blank Model is what lets the chat-engine
	// resolver apply the system default at request time. A non-empty
	// Model here means someone reintroduced the stale-model-ID bug.
	for _, slug := range []string{
		"worker", "planner", "hint-selector",
		"researcher", "backend", "background-job", "reviewer",
	} {
		def := findBySlug(t, defs, slug)
		if def.Model != "" {
			t.Errorf("slug=%s: Model = %q, want empty — internal profiles must inherit the system default, not hardcode a model (CW-20260815-0021)", slug, def.Model)
		}
	}
	// Worker must carry PermissionMode=yolo so ToProfile maps to
	// CanExecute=true. Without it the worker can't run tools and the
	// post-058 honest-failure path collapses into fabrication.
	worker := findBySlug(t, defs, "worker")
	if worker.PermissionMode != "yolo" {
		t.Errorf("worker.PermissionMode = %q, want %q", worker.PermissionMode, "yolo")
	}
	if got := worker.ToProfile().CanExecute; !got {
		t.Errorf("worker.ToProfile().CanExecute = false; want true (PermissionMode=yolo should map to can_execute=true)")
	}
}

// TestInternalProfileSlugs_DeterministicOrder asserts the slug list is
// returned in sorted order so any log line or comparison test downstream
// is stable across runs.
func TestInternalProfileSlugs_DeterministicOrder(t *testing.T) {
	slugs, err := InternalProfileSlugs()
	if err != nil {
		t.Fatalf("InternalProfileSlugs: %v", err)
	}
	for i := 1; i < len(slugs); i++ {
		if slugs[i-1] >= slugs[i] {
			t.Errorf("slugs not sorted at [%d]: %q >= %q (full=%v)", i, slugs[i-1], slugs[i], slugs)
		}
	}
}

// TestInternalProfiles_RoleIdentitySmoke is the unit-test-stub smoke
// evidence path described in the CW-20260512-0113 boot prompt (§8): each
// of the five Wave 4 role profiles, plus the expanded Wave 4 planner,
// must carry identity tokens that ground its role-specific behavior.
// This is the regression target for the c160 fabrication chain — when
// `Pattern: "researcher"` resolves to this profile (after W4 lands), the
// body must contain "read-only" + "cite" tokens so the dispatched
// subagent operates from grounded role-identity rather than inheriting
// only the universal slot.
func TestInternalProfiles_RoleIdentitySmoke(t *testing.T) {
	defs, err := InternalProfiles()
	if err != nil {
		t.Fatalf("InternalProfiles: %v", err)
	}
	cases := []struct {
		slug   string
		tokens []string
		// canExecuteWant matches store.AgentProfile.CanExecute after
		// ToProfile (derived from PermissionMode=yolo).
		canExecuteWant bool
	}{
		// Researcher: read-only, cites paths/lines. The boot prompt's
		// §8.1 smoke "researcher refuses fabrication" — the universal
		// Refusal rules supply the refuse-rather-than-fabricate behavior;
		// the role body grounds it with read-only + cite-paths discipline.
		{
			slug:           "researcher",
			tokens:         []string{"read-only", "Cite", "dev_glob"},
			canExecuteWant: false,
		},
		// Planner (boot prompt §8.2 smoke): decomposition + dependency
		// language. The expanded Wave 4 body preserves the Phase 6 stub
		// framing AND carries the dependency-ordered planning tokens.
		{
			slug:           "planner",
			tokens:         []string{"Decompose", "dependenc", "Phase 6"},
			canExecuteWant: false,
		},
		{
			slug:           "backend",
			tokens:         []string{"Go server-side", "go test -race", "Migrations are append-only"},
			canExecuteWant: true,
		},
		{
			slug:           "background-job",
			tokens:         []string{"async worker", "Idempotency", "terminal envelope"},
			canExecuteWant: true,
		},
	}
	for _, c := range cases {
		def := findBySlug(t, defs, c.slug)
		for _, tok := range c.tokens {
			if !strings.Contains(def.SystemPrompt, tok) {
				t.Errorf("slug=%s: body missing role-identity token %q (role identity drifted?)", c.slug, tok)
			}
		}
		// Confirm the read-only/execute split is preserved through the
		// PermissionMode → CanExecute mapping in convert.go.
		if got := def.ToProfile().CanExecute; got != c.canExecuteWant {
			t.Errorf("slug=%s: ToProfile().CanExecute = %v, want %v (check PermissionMode in .md frontmatter)", c.slug, got, c.canExecuteWant)
		}
	}
}

// boldLead matches a markdown bold run — the `**Refuse rather than
// fabricate.**` lead of a universal-rules bullet.
var boldLead = regexp.MustCompile(`\*\*([^*]+)\*\*`)

// universalSentinels derives the duplication sentinels FROM the live
// universal-rules block instead of hand-copying them.
//
// The hand-copied list this replaces coupled two packages by transcription:
// a legitimate reword in internal/chat/universal_rules.go broke a test in
// internal/agent/builtin, and keeping them in step was manual. Deriving
// them means a reword updates both sides in one edit and the guard still
// holds. internal/chat does not import internal/agent/builtin (it imports
// internal/agent), so this direction is cycle-free.
//
// What it extracts: the block's top-level heading, and every bold bullet
// lead. Subsection headings (### Grounding, ### Refusal, …) are
// deliberately NOT sentinels — they are ordinary section names a role body
// could legitimately use, and the list this replaces did not include them.
func universalSentinels(t *testing.T) []string {
	t.Helper()
	block := chat.UniversalRulesBlock()

	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		v := strings.TrimRight(strings.TrimSpace(raw), ".:")
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}

	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "## ") {
			head := strings.TrimPrefix(line, "## ")
			if i := strings.Index(head, " ("); i >= 0 {
				head = head[:i]
			}
			add("## " + head)
			break
		}
	}
	for _, m := range boldLead.FindAllStringSubmatch(block, -1) {
		add(m[1])
	}

	// Floor. A derived list has one failure mode a literal list does not:
	// if the block's markdown shape changes, extraction silently yields
	// nothing and every assertion below passes vacuously. The block has
	// carried 10 sentinels since CW-20260519-0068; 8 leaves room to drop a
	// bullet or two without churn while still failing loud on an empty or
	// gutted parse.
	if len(out) < 8 {
		t.Fatalf("derived only %d universal sentinels from UniversalRulesBlock (%v) — "+
			"the block's markdown shape likely changed and this extraction needs updating; "+
			"without it the duplication guard below passes vacuously", len(out), out)
	}
	return out
}

// TestInternalProfiles_RoleBodiesExcludeUniversalRules guards the five
// Wave 4 role bodies against re-introducing universal-layer content.
// The universal grounding/refusal/verification rules live in
// internal/chat/universal_rules.go and are auto-injected at SlotUniversal
// for every agent; duplicating them in role bodies undoes the layering
// benefit and reopens the c160 fabrication regression.
func TestInternalProfiles_RoleBodiesExcludeUniversalRules(t *testing.T) {
	defs, err := InternalProfiles()
	if err != nil {
		t.Fatalf("InternalProfiles: %v", err)
	}
	sentinels := universalSentinels(t)
	for _, slug := range []string{"researcher", "backend", "background-job", "planner"} {
		def := findBySlug(t, defs, slug)
		for _, sentinel := range sentinels {
			if strings.Contains(def.SystemPrompt, sentinel) {
				t.Errorf("slug=%s: body duplicates universal sentinel %q — universal_rules.go owns this", slug, sentinel)
			}
		}
	}
}

func findBySlug(t *testing.T, defs []*agent.Definition, slug string) *agent.Definition {
	t.Helper()
	for _, d := range defs {
		if d.Slug == slug {
			return d
		}
	}
	t.Fatalf("profile slug=%s not found", slug)
	return nil
}
