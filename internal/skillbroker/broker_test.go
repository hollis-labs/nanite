package skillbroker

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/store"
)

// mkSkill is a small constructor that keeps the test table compact.
func mkSkill(slug, name, desc, category, source, modeIDs string) store.Skill {
	return store.Skill{
		ID:          slug + "-id",
		Slug:        slug,
		Name:        name,
		Description: desc,
		Category:    category,
		Source:      source,
		ModeIDs:     modeIDs,
	}
}

func TestSelectSkills_RankedSubsetNotFullRegistry(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("alpha", "alpha-skill", "tool for alpha tasks", "general", "builtin", ""),
		mkSkill("beta", "beta-skill", "tool for beta tasks", "general", "builtin", ""),
		mkSkill("research", "research-codebase", "research the codebase architecture", "research", "builtin", ""),
		mkSkill("plan", "plan-feature", "plan a new feature", "planning", "builtin", ""),
		mkSkill("debug", "debug-issue", "debug a runtime error", "debugging", "builtin", ""),
	}
	agent := AgentIdentity{Slug: "researcher", Tags: []string{}}
	intent := contextbroker.Intent{
		Type:     contextbroker.IntentWriteCode,
		Keywords: []string{"research"},
	}

	got := SelectSkills(context.Background(), intent, agent, candidates, Options{MaxSkills: 2})

	if len(got) != 2 {
		t.Fatalf("want 2 skills (cap), got %d", len(got))
	}
	if got[0].Slug != "research" {
		t.Errorf("want research skill ranked first, got %s", got[0].Slug)
	}
	// Acceptance check: the broker returned a subset, not the full
	// registry.
	if len(got) >= len(candidates) {
		t.Errorf("broker returned full registry (%d/%d) — should be subset", len(got), len(candidates))
	}
}

func TestSelectSkills_SameAgentDifferentIntentDifferentSelection(t *testing.T) {
	// Acceptance test from ticket: same agent + different intent →
	// different skill selection.
	candidates := []store.Skill{
		mkSkill("research", "research-codebase", "research the codebase", "research", "builtin", ""),
		mkSkill("plan", "plan-feature", "plan a new feature", "planning", "builtin", ""),
		mkSkill("debug", "debug-issue", "debug a runtime error", "debugging", "builtin", ""),
		mkSkill("write", "write-test", "write a unit test", "testing", "builtin", ""),
	}
	agent := AgentIdentity{Slug: "worker", Tags: []string{}}

	intentDebug := contextbroker.Intent{
		Type:      contextbroker.IntentDebugIssue,
		Keywords:  []string{"debug", "error"},
		QueryText: "I have a runtime error in the parser",
	}
	intentPlan := contextbroker.Intent{
		Type:      contextbroker.IntentPlanFeature,
		Keywords:  []string{"plan", "feature"},
		QueryText: "plan a new feature for the broker",
	}

	gotDebug := SelectSkills(context.Background(), intentDebug, agent, candidates, Options{MaxSkills: 2})
	gotPlan := SelectSkills(context.Background(), intentPlan, agent, candidates, Options{MaxSkills: 2})

	if len(gotDebug) == 0 || len(gotPlan) == 0 {
		t.Fatalf("both selections should be non-empty (got %d / %d)", len(gotDebug), len(gotPlan))
	}
	if gotDebug[0].Slug != "debug" {
		t.Errorf("debug intent — want debug skill first, got %s", gotDebug[0].Slug)
	}
	if gotPlan[0].Slug != "plan" {
		t.Errorf("plan intent — want plan skill first, got %s", gotPlan[0].Slug)
	}
	if gotDebug[0].Slug == gotPlan[0].Slug {
		t.Errorf("same agent + different intent should rank different top skill")
	}
}

func TestSelectSkills_CapEnforced(t *testing.T) {
	// Build > MaxSelectedSkills candidates to exercise the default cap.
	candidates := make([]store.Skill, 0, MaxSelectedSkills+10)
	for i := 0; i < MaxSelectedSkills+10; i++ {
		candidates = append(candidates, mkSkill(
			"skill-"+string(rune('a'+i%26))+string(rune('a'+i/26)),
			"skill-name-"+string(rune('a'+i%26)),
			"description",
			"general",
			"builtin",
			"",
		))
	}
	agent := AgentIdentity{Slug: "worker"}
	intent := contextbroker.Intent{Keywords: []string{"name"}}

	// Default cap (Options.MaxSkills == 0).
	got := SelectSkills(context.Background(), intent, agent, candidates, Options{})
	if len(got) != MaxSelectedSkills {
		t.Errorf("default cap not enforced — got %d, want %d", len(got), MaxSelectedSkills)
	}

	// Custom cap.
	gotCapped := SelectSkills(context.Background(), intent, agent, candidates, Options{MaxSkills: 5})
	if len(gotCapped) != 5 {
		t.Errorf("custom cap not enforced — got %d, want 5", len(gotCapped))
	}
}

func TestSelectSkills_AgentTagBoost(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("a", "alpha-skill", "general purpose alpha", "general", "builtin", ""),
		mkSkill("p", "planning-tools", "planning helpers", "planning", "builtin", ""),
	}
	agent := AgentIdentity{Slug: "worker", Tags: []string{"planning"}}
	intent := contextbroker.Intent{} // no keywords — pure tag match

	got := SelectSkillsScored(context.Background(), intent, agent, candidates, Options{})

	if len(got) != 2 {
		t.Fatalf("want 2 scored, got %d", len(got))
	}
	if got[0].Skill.Slug != "p" {
		t.Errorf("tag-matched skill should rank first, got %s", got[0].Skill.Slug)
	}
	if got[0].AgentScore == 0 {
		t.Errorf("expected non-zero AgentScore on tag-matched skill, got %d", got[0].AgentScore)
	}
}

func TestSelectSkills_SlugTreatedAsHonoraryTag(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("a", "alpha-skill", "general", "general", "builtin", ""),
		mkSkill("r", "research-codebase", "research", "research", "builtin", ""),
	}
	agent := AgentIdentity{Slug: "researcher", Tags: nil}
	intent := contextbroker.Intent{}

	got := SelectSkills(context.Background(), intent, agent, candidates, Options{})

	if got[0].Slug != "r" {
		t.Errorf("agent slug should boost matching skill via honorary-tag, got %s first", got[0].Slug)
	}
}

func TestSelectSkills_ModeBoundBonus(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("a", "alpha-skill", "general", "general", "builtin", ""),
		mkSkill("b", "alpha-skill-mode", "general", "general", "builtin", `["mode-plan-id"]`),
	}
	agent := AgentIdentity{Slug: "worker"}
	intent := contextbroker.Intent{}

	got := SelectSkillsScored(context.Background(), intent, agent, candidates, Options{})

	if len(got) != 2 {
		t.Fatalf("want 2 scored, got %d", len(got))
	}
	if got[0].Skill.Slug != "b" {
		t.Errorf("mode-bound skill should rank above mode-agnostic, got %s", got[0].Skill.Slug)
	}
	if got[0].ModeScore == 0 {
		t.Errorf("expected non-zero ModeScore on mode-bound skill")
	}
}

func TestSelectSkills_TieBreakSourceBiasThenName(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("z", "zeta", "noop", "general", "plugin", ""),
		mkSkill("a", "alpha", "noop", "general", "user", ""),
		mkSkill("b", "beta", "noop", "general", "builtin", ""),
	}
	agent := AgentIdentity{Slug: "worker"}
	intent := contextbroker.Intent{}

	got := SelectSkills(context.Background(), intent, agent, candidates, Options{})

	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	// All scores are 0 → tie broken by source bias (builtin > user > plugin).
	if got[0].Slug != "b" {
		t.Errorf("builtin should rank first on score tie, got %s", got[0].Slug)
	}
	if got[1].Slug != "a" {
		t.Errorf("user should rank second on score tie, got %s", got[1].Slug)
	}
	if got[2].Slug != "z" {
		t.Errorf("plugin should rank last on score tie, got %s", got[2].Slug)
	}
}

func TestSelectSkills_EmptyCandidatesReturnsNil(t *testing.T) {
	got := SelectSkills(context.Background(), contextbroker.Intent{}, AgentIdentity{}, nil, Options{})
	if got != nil {
		t.Errorf("empty input should return nil, got %#v", got)
	}
}

func TestSelectSkills_DoesNotMutateInput(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("a", "alpha", "x", "general", "plugin", ""),
		mkSkill("b", "beta", "x", "general", "builtin", ""),
	}
	// Capture pre-call order.
	orig0 := candidates[0].Slug
	orig1 := candidates[1].Slug

	_ = SelectSkills(context.Background(), contextbroker.Intent{}, AgentIdentity{}, candidates, Options{})

	if candidates[0].Slug != orig0 || candidates[1].Slug != orig1 {
		t.Errorf("input slice was mutated: pre=%q,%q post=%q,%q",
			orig0, orig1, candidates[0].Slug, candidates[1].Slug)
	}
}

func TestSelectSkills_DeterministicAcrossCalls(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("a", "alpha", "intent matched", "general", "builtin", ""),
		mkSkill("b", "beta", "intent matched", "general", "builtin", ""),
		mkSkill("c", "gamma", "intent matched", "general", "builtin", ""),
	}
	agent := AgentIdentity{Slug: "worker"}
	intent := contextbroker.Intent{Keywords: []string{"intent"}}

	first := SelectSkills(context.Background(), intent, agent, candidates, Options{})
	second := SelectSkills(context.Background(), intent, agent, candidates, Options{})

	if len(first) != len(second) {
		t.Fatalf("non-deterministic length: %d != %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Slug != second[i].Slug {
			t.Errorf("non-deterministic order at %d: %s != %s", i, first[i].Slug, second[i].Slug)
		}
	}
}

func TestSelectSkills_QueryTextHitsBeyondKeywords(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("a", "alpha-skill", "deal with parser errors gracefully", "general", "builtin", ""),
		mkSkill("b", "beta-skill", "general purpose tool", "general", "builtin", ""),
	}
	agent := AgentIdentity{Slug: "worker"}
	intent := contextbroker.Intent{
		// No keywords, but QueryText mentions "parser".
		QueryText: "I have a problem with the parser module",
	}

	got := SelectSkills(context.Background(), intent, agent, candidates, Options{})

	if got[0].Slug != "a" {
		t.Errorf("query-text token should boost matching skill, got %s first", got[0].Slug)
	}
}

func TestSelectSkills_KeywordPreemptsQueryDoubleCount(t *testing.T) {
	// A term appearing as a keyword AND in the query text should only
	// credit the keyword path (mid-tier), not both.
	candidates := []store.Skill{
		mkSkill("a", "alpha-skill", "parser tools", "general", "builtin", ""),
	}
	intent := contextbroker.Intent{
		Keywords:  []string{"parser"},
		QueryText: "the parser is broken",
	}
	got := SelectSkillsScored(context.Background(), intent, AgentIdentity{}, candidates, Options{})
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].QueryScore != 0 {
		t.Errorf("keyword should preempt query-text scoring for the same token; got QueryScore=%d", got[0].QueryScore)
	}
	if got[0].KeywordScore != keywordWeight {
		t.Errorf("want KeywordScore=%d, got %d", keywordWeight, got[0].KeywordScore)
	}
}

func TestIsModeBound(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"[]", false},
		{`["abc"]`, true},
		{`["a","b"]`, true},
		{`not-json`, false},
	}
	for _, c := range cases {
		if got := isModeBound(c.in); got != c.want {
			t.Errorf("isModeBound(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAgentIdentityFromProfile(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		got := AgentIdentityFromProfile(nil)
		if got.ID != "" || got.Slug != "" || got.Tags != nil {
			t.Errorf("nil input should yield zero AgentIdentity, got %#v", got)
		}
	})

	t.Run("empty tags", func(t *testing.T) {
		got := AgentIdentityFromProfile(&store.AgentProfile{ID: "x", Slug: "s", Tags: "[]"})
		if got.Slug != "s" || len(got.Tags) != 0 {
			t.Errorf("empty-tags profile: got %#v", got)
		}
	})

	t.Run("valid tags", func(t *testing.T) {
		got := AgentIdentityFromProfile(&store.AgentProfile{ID: "x", Slug: "s", Tags: `["a","b"]`})
		if len(got.Tags) != 2 || got.Tags[0] != "a" || got.Tags[1] != "b" {
			t.Errorf("valid-tags profile: got Tags=%v", got.Tags)
		}
	})

	t.Run("malformed tags", func(t *testing.T) {
		got := AgentIdentityFromProfile(&store.AgentProfile{ID: "x", Slug: "s", Tags: "not-json"})
		// Should not panic, should yield empty tags (fail-open).
		if got.Slug != "s" || got.Tags != nil {
			t.Errorf("malformed-tags profile: want empty Tags, got %v", got.Tags)
		}
	})
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"hello world", []string{"hello", "world"}},
		{"a is dropped", []string{"is", "dropped"}}, // single-char filter
		{"foo-bar baz_qux", []string{"foo-bar", "baz_qux"}},
		{"Mixed CASE", []string{"mixed", "case"}},
		{"punctuation! goes? away.", []string{"punctuation", "goes", "away"}},
	}
	for _, c := range cases {
		got := tokenize(c.in)
		if len(got) != len(c.want) {
			t.Errorf("tokenize(%q) = %v; want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("tokenize(%q)[%d] = %q; want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// Ensure the package consistency: weight constants are sensible.
func TestWeightOrdering(t *testing.T) {
	// Agent tag should outrank a single keyword, which outranks a single
	// query-text hit. Mode bonus stays below keyword. These relationships
	// are documented in the package doc; this test pins them.
	if !(agentTagWeight > keywordWeight) {
		t.Errorf("invariant violated: agentTagWeight (%d) should outrank keywordWeight (%d)",
			agentTagWeight, keywordWeight)
	}
	if !(keywordWeight > queryTextWeight) {
		t.Errorf("invariant violated: keywordWeight (%d) should outrank queryTextWeight (%d)",
			keywordWeight, queryTextWeight)
	}
	if !(keywordWeight > modeBoundWeight) {
		t.Errorf("invariant violated: keywordWeight (%d) should outrank modeBoundWeight (%d)",
			keywordWeight, modeBoundWeight)
	}
}

// Smoke test that the package's public API uses lowercase consistently
// for comparison — defensive against future refactors that forget to
// lowercase before substring-matching.
func TestKeywordCaseInsensitive(t *testing.T) {
	candidates := []store.Skill{
		mkSkill("a", "PARSER-SKILL", "Parser Tools", "GENERAL", "builtin", ""),
	}
	intent := contextbroker.Intent{Keywords: []string{"parser"}}
	got := SelectSkillsScored(context.Background(), intent, AgentIdentity{}, candidates, Options{})

	if len(got) != 1 || got[0].KeywordScore == 0 {
		t.Errorf("case-insensitive match failed: %+v", got)
	}
	// Sanity: ensure we're not relying on accidental lowercase-source.
	if !strings.Contains(strings.ToLower(candidates[0].Name), "parser") {
		t.Errorf("test fixture broken: name should contain 'parser' (case-insensitive)")
	}
}
