package builtin

import (
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
)

// TestInternalProfiles_LoadsAllExpectedSlugs asserts the embedded profiles/
// directory carries exactly the four canonical internal profile slugs
// expected by the Wave 2 cleanup migration (CW-20260512-0112). If a new
// internal profile is added, update this list AND the migration's keep-set
// in lockstep.
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

	want := []string{"default", "hint-selector", "planner", "worker"}
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
// The Wave 2 cleanup migration keys off Source value, and the UI uses
// SourceRef to render the "edit this file at:" affordance.
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
// `permissionMode:`, not `defaultModel:`. Without this assertion, a
// typo like `defaultModel:` would silently produce empty Model on every
// internal profile and the worker would lose can_execute=true (re-opening
// the c160 fabrication risk because the worker can't actually execute
// tools, so it would have to fabricate progress).
func TestInternalProfiles_FrontmatterKeys(t *testing.T) {
	defs, err := InternalProfiles()
	if err != nil {
		t.Fatalf("InternalProfiles: %v", err)
	}
	// Every internal profile that ships with a model must parse it
	// (worker / planner / hint-selector all carry a `model:` field;
	// default carries none and inherits the harness default).
	for _, slug := range []string{"worker", "planner", "hint-selector"} {
		def := findBySlug(t, defs, slug)
		if def.Model == "" {
			t.Errorf("slug=%s: Model is empty — frontmatter key likely wrong (must be `model:`, not `defaultModel:`)", slug)
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
