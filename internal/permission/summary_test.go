package permission

import (
	"strings"
	"testing"
)

// TestRenderPermissionSummary_EmptyInputProducesEmptyOutput asserts that an
// empty input renders to an empty string, so the slot wiring (chat
// context_client → SlotPermissions) ships nothing when there's nothing to
// say. The header alone is not worth burning tokens on.
func TestRenderPermissionSummary_EmptyInputProducesEmptyOutput(t *testing.T) {
	out := RenderPermissionSummary(SummaryInput{})
	if out != "" {
		t.Fatalf("expected empty output for empty input, got:\n%s", out)
	}
}

// TestRenderPermissionSummary_TypicalChatProfile renders a representative
// chat-profile summary (sample (a) from the implementer report): static
// allow-list + a session grant + an explicit deny rule. Covers ordering
// (allow before deny), source provenance, and the closing refusal hook.
func TestRenderPermissionSummary_TypicalChatProfile(t *testing.T) {
	rs := &RuleSet{
		Rules: []Rule{
			{
				Tool:     "dev_read",
				Pattern:  "/Users/u/secret/**",
				Behavior: DecisionDeny,
				Source:   "profile.yaml",
			},
			{
				Tool:     "dev_edit",
				Pattern:  "/Users/u/project_a/**",
				Behavior: DecisionAllow,
				Source:   "profile.yaml",
			},
		},
	}
	in := SummaryInput{
		WorkingDir: "/Users/u/project_a",
		Rules:      rs,
		AllowedPaths: []string{
			"/Users/u/project_a",
			"/Users/u/project_b",
		},
		OwnGrants: []string{
			"/Users/u/project_a/config.yaml",
		},
		SessionScope: "this chat session's scope",
	}

	out := RenderPermissionSummary(in)

	mustContain(t, out, "## Path access")
	mustContain(t, out, "You can READ under (workspace allow-list):")
	mustContain(t, out, "/Users/u/project_a/")
	mustContain(t, out, "/Users/u/project_b/")
	mustContain(t, out, "You have explicit access to (session grants):")
	mustContain(t, out, "/Users/u/project_a/config.yaml")
	mustContain(t, out, "Additional allow rules from profile:")
	mustContain(t, out, "`dev_edit`")
	mustContain(t, out, "/Users/u/project_a/**")
	mustContain(t, out, "(from profile.yaml)")
	mustContain(t, out, "You CANNOT access (explicitly denied):")
	mustContain(t, out, "`dev_read`")
	mustContain(t, out, "/Users/u/secret/**")
	mustContain(t, out, "outside this chat session's scope")
	mustContain(t, out, "return a failure naming the path")
}

// TestRenderPermissionSummary_ResearcherSubagentWithDeniedPath is the
// c160 turn-16 reproduction shape (sample (b) from the implementer
// report). The researcher subagent has a restricted scope and one of the
// paths the user mentioned in the parent thread is now explicitly denied
// in the researcher's profile. The summary must SHOW the deny so the
// agent reads it and refuses instead of fabricating.
func TestRenderPermissionSummary_ResearcherSubagentWithDeniedPath(t *testing.T) {
	rs := &RuleSet{
		Rules: []Rule{
			{
				Tool:     "dev_read",
				Pattern:  "/Users/u/Projects-apps/Fragments Engine vFinalIdentity/**",
				Behavior: DecisionDeny,
				Source:   "researcher subagent permissions",
			},
		},
	}
	in := SummaryInput{
		WorkingDir: "/Users/u/Projects-apps/nanite",
		Rules:      rs,
		AllowedPaths: []string{
			"/Users/u/Projects-apps/nanite",
		},
		InheritedGrants: []string{
			"/Users/u/Projects-apps/agent-workspaces",
		},
		SessionScope: "this researcher subagent's scope",
	}

	out := RenderPermissionSummary(in)

	mustContain(t, out, "You can READ under (workspace allow-list):")
	mustContain(t, out, "/Users/u/Projects-apps/nanite/")
	mustContain(t, out, "You inherit these from the parent session:")
	mustContain(t, out, "/Users/u/Projects-apps/agent-workspaces  (from parent session)")
	mustContain(t, out, "You CANNOT access (explicitly denied):")
	mustContain(t, out, "Fragments Engine vFinalIdentity")
	mustContain(t, out, "(from researcher subagent permissions)")
	mustContain(t, out, "outside this researcher subagent's scope")
	mustContain(t, out, "return a failure naming the path")

	// The closing refusal hook must direct toward refusal, not fabrication.
	// This is the c160 regression target — same prompt shape that produced
	// "I've analyzed Fragments Engine" must now produce a refusal envelope.
	if !strings.Contains(out, "return a failure") {
		t.Errorf("output missing refusal-hook directive — c160 regression risk:\n%s", out)
	}
}

// TestRenderPermissionSummary_DeterministicOverInputs asserts that two
// invocations with identical inputs produce byte-identical output. This is
// the SHA-256 cache-key invariant the slot wiring depends on — content
// stability across turns is what keeps the slot in the Anthropic
// cacheable prefix.
func TestRenderPermissionSummary_DeterministicOverInputs(t *testing.T) {
	in := SummaryInput{
		WorkingDir: "/Users/u/proj",
		Rules: &RuleSet{
			Rules: []Rule{
				{Tool: "dev_read", Pattern: "/no/**", Behavior: DecisionDeny, Source: "p.yaml"},
			},
		},
		AllowedPaths: []string{"/Users/u/proj"},
		OwnGrants:    []string{"/Users/u/proj/extra"},
	}
	a := RenderPermissionSummary(in)
	b := RenderPermissionSummary(in)
	if a != b {
		t.Errorf("non-deterministic output:\nfirst:\n%s\nsecond:\n%s", a, b)
	}
}

// TestRenderPermissionSummary_SortStability asserts that path/grant order
// in the OUTPUT is deterministic even when the INPUT slices are in
// different orders. Keeps the slot's cache key stable across turns
// regardless of map-iteration order in upstream sources (PathGrants
// internals use Go maps).
func TestRenderPermissionSummary_SortStability(t *testing.T) {
	a := RenderPermissionSummary(SummaryInput{
		AllowedPaths: []string{"/b", "/a", "/c"},
		OwnGrants:    []string{"/x/2", "/x/1"},
	})
	b := RenderPermissionSummary(SummaryInput{
		AllowedPaths: []string{"/c", "/b", "/a"},
		OwnGrants:    []string{"/x/1", "/x/2"},
	})
	if a != b {
		t.Errorf("sort-stability failed:\na:\n%s\nb:\n%s", a, b)
	}
}

// TestRenderPermissionSummary_InheritedSuppressedByOwn asserts that a
// grant already present in OwnGrants is suppressed from the "inherited
// from parent" section. Each path appears exactly once in the rendered
// output, attributed to the most-specific origin (own > parent).
func TestRenderPermissionSummary_InheritedSuppressedByOwn(t *testing.T) {
	in := SummaryInput{
		OwnGrants:       []string{"/Users/u/shared"},
		InheritedGrants: []string{"/Users/u/shared", "/Users/u/parent-only"},
	}
	out := RenderPermissionSummary(in)
	mustContain(t, out, "You have explicit access to (session grants):")
	mustContain(t, out, "/Users/u/shared")
	mustContain(t, out, "You inherit these from the parent session:")
	mustContain(t, out, "/Users/u/parent-only")
	// /Users/u/shared must appear exactly once.
	if strings.Count(out, "/Users/u/shared") != 1 {
		t.Errorf("expected /Users/u/shared to appear once (deduped), got %d:\n%s",
			strings.Count(out, "/Users/u/shared"), out)
	}
}

// TestRenderPermissionSummary_AskRulesSurface asserts that ask-mode rules
// render in their own section so the agent can pre-announce the upcoming
// approval prompt rather than blindly invoking a tool that will pause.
func TestRenderPermissionSummary_AskRulesSurface(t *testing.T) {
	rs := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_bash", Pattern: "rm -rf", Behavior: DecisionAsk, Source: "profile.yaml"},
		},
	}
	out := RenderPermissionSummary(SummaryInput{Rules: rs})
	mustContain(t, out, "These will require approval:")
	mustContain(t, out, "`dev_bash`")
	mustContain(t, out, "rm -rf")
	mustContain(t, out, "(from profile.yaml)")
}

// TestRenderPermissionSummary_SessionScopeDefault asserts that an empty
// SessionScope renders the generic "this session's scope" qualifier.
func TestRenderPermissionSummary_SessionScopeDefault(t *testing.T) {
	out := RenderPermissionSummary(SummaryInput{
		AllowedPaths: []string{"/x"},
	})
	mustContain(t, out, "outside this session's scope")
}

// TestRenderPermissionSummary_AllowedPathsCleanedAndSeparatorAppended
// asserts that the AllowedPaths list gets normalized — trailing separator
// appended so rows read as directory roots, not ambiguous file paths.
func TestRenderPermissionSummary_AllowedPathsCleanedAndSeparatorAppended(t *testing.T) {
	out := RenderPermissionSummary(SummaryInput{
		AllowedPaths: []string{"/Users/u/proj", "/Users/u/proj/"},
	})
	// Both inputs collapse to a single deduped entry with trailing /
	if strings.Count(out, "/Users/u/proj/") != 1 {
		t.Errorf("expected exactly one '/Users/u/proj/' row (dedup + normalize), got %d:\n%s",
			strings.Count(out, "/Users/u/proj/"), out)
	}
}

// TestRenderPermissionSummary_UnknownSourceFallback asserts that a rule
// without a Source tag renders with "unknown source" rather than breaking
// the bullet shape — keeps the renderer deterministic on partial inputs.
func TestRenderPermissionSummary_UnknownSourceFallback(t *testing.T) {
	rs := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/x/**", Behavior: DecisionDeny}, // no Source
		},
	}
	out := RenderPermissionSummary(SummaryInput{Rules: rs})
	mustContain(t, out, "(from unknown source)")
}

// TestListLineageGrants exercises the parent-walk that ListLineageGrants
// performs. Asserts both the union semantics (multiple ancestor buckets
// merged) and the walker isolation: ListLineageGrants returns ONLY parent
// buckets (not the child's own bucket). Overlap between own + parent
// buckets is the summary renderer's subtraction concern, not the walker's.
func TestListLineageGrants(t *testing.T) {
	g := NewPathGrants()
	// Parent session has a grant under /tmp/parent-only/.
	g.RegisterFromUserMessage("parent", "look at /tmp/parent-only/foo.go")
	// Child has a grant under /tmp/child-only/. The two paths share no
	// common parent directory, so the parent-dir co-registration
	// (RegisterFromUserMessage stamps literal + parent) produces no
	// accidental overlap.
	g.RegisterFromUserMessage("child", "also /tmp/child-only/bar.go")
	g.RegisterLineage("child", "parent")

	own := g.ListGrants("child")
	if len(own) == 0 {
		t.Fatal("expected child to have own grants")
	}
	lineage := g.ListLineageGrants("child")
	if len(lineage) == 0 {
		t.Fatal("expected child to inherit parent's grants via lineage walk")
	}

	// Walker isolation — the lineage walk inspects ONLY ancestor buckets.
	// Anything in lineage that also appears in own must have come from the
	// parent's bucket, not from the child's. With disjoint parent dirs
	// (above), this should produce zero intersection.
	for _, p := range lineage {
		for _, o := range own {
			if p == o {
				t.Errorf("walker isolation broken — lineage returned a path that's also in own bucket: %s", p)
			}
		}
	}

	// Specific path checks — parent's path must be reachable via lineage,
	// child's path must NOT.
	mustContainPath(t, lineage, "/tmp/parent-only/foo.go")
	mustNotContainPath(t, lineage, "/tmp/child-only/bar.go")
}

// TestListLineageGrants_EmptyOnNoLineage asserts that a session with no
// lineage entry returns nil (not a wide-cast scan).
func TestListLineageGrants_EmptyOnNoLineage(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("solo", "look at ~/foo")
	if got := g.ListLineageGrants("solo"); got != nil {
		t.Errorf("expected nil lineage grants for solo session, got %v", got)
	}
}

// TestRenderPermissionSummary_W4Integration_ResolvedPatternsRendered
// asserts the W4 (CW-20260512-0120) integration: when the caller invokes
// (*RuleSet).Resolve(workingDir) BEFORE passing to RenderPermissionSummary,
// the rendered patterns appear in their absolute resolved form rather than
// as un-resolved `./` shapes. This is the downstream contract W4 left for
// W2/W3 to consume (see W4 implementer report § 7).
func TestRenderPermissionSummary_W4Integration_ResolvedPatternsRendered(t *testing.T) {
	workingDir := t.TempDir()
	rs := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "./**", Behavior: DecisionAllow, Source: "profile.yaml"},
			{Tool: "dev_write", Pattern: "./generated/**", Behavior: DecisionAllow, Source: "profile.yaml"},
			{Tool: "dev_read", Pattern: "/Users/u/secret/**", Behavior: DecisionDeny, Source: "profile.yaml"},
		},
	}
	resolved, err := rs.Resolve(workingDir)
	if err != nil {
		t.Fatalf("RuleSet.Resolve: %v", err)
	}

	out := RenderPermissionSummary(SummaryInput{
		WorkingDir: workingDir,
		Rules:      resolved,
	})

	// Workspace-relative `./` patterns must NOT appear verbatim — they
	// must be resolved to absolute paths rooted at workingDir.
	if strings.Contains(out, "./**") {
		t.Errorf("workspace-relative `./` leaked into rendered summary (W4 Resolve not consumed):\n%s", out)
	}
	if strings.Contains(out, "./generated/**") {
		t.Errorf("workspace-relative `./generated/**` leaked into rendered summary:\n%s", out)
	}

	// Absolute pattern (deny rule with absolute path) passes through
	// verbatim — W4's contract is "absolute patterns untouched".
	if !strings.Contains(out, "/Users/u/secret/**") {
		t.Errorf("absolute deny pattern missing from rendered summary:\n%s", out)
	}

	// The resolved absolute prefix must appear in the rendered patterns.
	// We can't predict the canonical temp dir without symlink resolution,
	// so check that the patterns no longer start with "./".
	for _, r := range resolved.Rules {
		if strings.HasPrefix(r.Pattern, "./") {
			t.Errorf("resolved RuleSet still has un-resolved pattern: %q", r.Pattern)
		}
	}
}

// TestListLineageGrants_CycleGuard asserts the walk terminates even when
// the lineage map has a cycle (defensive — production callers use fresh
// IDs per spawn, but the walker should not spin).
func TestListLineageGrants_CycleGuard(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("a", "look at ~/a-path")
	g.RegisterFromUserMessage("b", "look at ~/b-path")
	g.RegisterLineage("a", "b")
	g.RegisterLineage("b", "a")
	// Should return b's grants (one hop) and then stop on the cycle guard.
	got := g.ListLineageGrants("a")
	if len(got) == 0 {
		t.Fatal("expected at least one hop's worth of grants before cycle guard fires")
	}
}

func mustContain(t *testing.T, body, needle string) {
	t.Helper()
	if !strings.Contains(body, needle) {
		t.Errorf("output missing %q in:\n%s", needle, body)
	}
}

func mustContainPath(t *testing.T, paths []string, needle string) {
	t.Helper()
	for _, p := range paths {
		if p == needle {
			return
		}
	}
	t.Errorf("expected %q in paths, got: %v", needle, paths)
}

func mustNotContainPath(t *testing.T, paths []string, needle string) {
	t.Helper()
	for _, p := range paths {
		if p == needle {
			t.Errorf("did not expect %q in paths, got: %v", needle, paths)
			return
		}
	}
}
