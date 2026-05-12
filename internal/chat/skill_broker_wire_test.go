package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/skillbroker"
	"github.com/hollis-labs/nanite/internal/store"
)

// SP-20260512-0008 W2B (CW-20260512-0106) — wire-integration tests
// pinning the Skill Broker's behavior end-to-end through the chat
// assembly path. Verifies that the broker-aware skill-list renderer
// (a) returns a ranked subset, not the full assignment, and (b) ranks
// differently for the same agent under different intents — the
// acceptance criteria called out in the ticket spec.

// TestBuildSkillListForSessionWithIntent_RankedSubsetNotFullRegistry is
// the wire test for "broker returns subset, not full registry". The agent
// has more than skillbroker.MaxSelectedSkills assigned; the rendered list
// must (a) cap at MaxSelectedSkills, (b) include intent-matched skills,
// and (c) omit the unrelated overflow skills entirely.
func TestBuildSkillListForSessionWithIntent_RankedSubsetNotFullRegistry(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "wire-rs-agent")

	// Create MaxSelectedSkills+overflow assigned skills. The few named
	// intent-matchers below must rank above the generic filler so the
	// filler tail gets trimmed by the broker's cap.
	overflowExtra := 6
	totalFiller := skillbroker.MaxSelectedSkills + overflowExtra

	// Named intent-matchers — these score above any generic filler.
	intentMatch := mustCreateSkill(t, s, &store.Skill{Name: "Debug Issue", Slug: "debug-issue", Description: "debug a runtime error", ToolBindings: `[]`, ModeIDs: `[]`, Source: "builtin"})
	mustAssignSkill(t, s, agent.ID, intentMatch.ID)

	// Filler with stable, sortable names. Names use a leading "ZZZZ-" prefix
	// so they sort AFTER "Debug Issue" alphabetically — the broker tiebreaks
	// on alphabetical when scores tie, and we want a deterministic overflow
	// tail. With the intent matcher pinned in early ranks, filler IDs
	// MaxSelectedSkills..end will land in the overflow tail and must not
	// appear in the rendered output.
	fillerNames := make([]string, totalFiller)
	for i := 0; i < totalFiller; i++ {
		// "ZZZZ-001", "ZZZZ-002", ... — width-stable for alpha sort.
		name := nameForFillerIdx(i)
		fillerNames[i] = name
		sk := mustCreateSkill(t, s, &store.Skill{
			Name: name, Slug: name, Description: "filler skill", ToolBindings: `[]`, ModeIDs: `[]`, Source: "builtin",
		})
		mustAssignSkill(t, s, agent.ID, sk.ID)
	}

	intent := contextbroker.Intent{
		Type:      contextbroker.IntentDebugIssue,
		Keywords:  []string{"debug", "error"},
		QueryText: "I have a runtime error",
	}
	identity := skillbroker.AgentIdentity{ID: agent.ID, Slug: agent.Slug}

	got := buildSkillListForSessionWithIntent(context.Background(), s, agent.ID, "", intent, identity)
	if got == "" {
		t.Fatalf("rendered list should be non-empty")
	}

	// "Debug Issue" must render (intent matcher).
	if !strings.Contains(got, "Debug Issue") {
		t.Errorf("debug intent should include Debug Issue skill, got: %q", got)
	}

	// Cap enforcement: count "- " rendered lines. The renderer emits one
	// "- Name: ..." line per selected skill, plus an additional LoadHint
	// block at the tail (not prefixed with "- "). Selected-skill lines
	// must equal exactly MaxSelectedSkills.
	renderedLines := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "- ") {
			renderedLines++
		}
	}
	if renderedLines != skillbroker.MaxSelectedSkills {
		t.Errorf("expected exactly %d rendered skill lines (broker cap), got %d; render:\n%s",
			skillbroker.MaxSelectedSkills, renderedLines, got)
	}

	// Overflow filler names (last `overflowExtra` entries by alpha order)
	// must be absent from the rendered output. Their slot in the inline
	// list is reserved for higher-ranked skills (Debug Issue + the
	// MaxSelectedSkills-1 alphabetically-earliest filler names).
	overflowStart := totalFiller - overflowExtra
	for i := overflowStart; i < totalFiller; i++ {
		if strings.Contains(got, fillerNames[i]+":") {
			t.Errorf("overflow filler %q should be trimmed by cap, but appeared in render:\n%s",
				fillerNames[i], got)
		}
	}

	// LoadHint must surface the trimmed-overflow count so the agent knows
	// the catalog has more available. With 1 named intent-matcher and
	// MaxSelectedSkills-1 filler names rendered, the LoadHint count is
	// the remaining filler tail: overflowExtra+1 (because one filler that
	// would have rendered now gets bumped by the intent matcher).
	if !strings.Contains(got, "additional skills are available") {
		t.Errorf("LoadHint must surface trimmed-overflow count, got: %s", got)
	}
}

// nameForFillerIdx returns a stable, alphabetically-sortable filler skill
// name. The "ZZZZ-" prefix ensures filler names sort AFTER any real intent
// matcher (e.g. "Debug Issue"), and the zero-padded suffix preserves a
// deterministic alpha order among filler entries — critical for asserting
// which tail entries get trimmed by the broker's cap.
func nameForFillerIdx(i int) string {
	const a = '0'
	hundreds := byte(a + (i/100)%10)
	tens := byte(a + (i/10)%10)
	ones := byte(a + i%10)
	return "ZZZZ-" + string([]byte{hundreds, tens, ones})
}

// TestBuildSkillListForSessionWithIntent_SameAgentDifferentIntents is the
// ticket acceptance check: same agent + different intent → different
// ranked subset. We compare two renders and assert the top skill differs.
func TestBuildSkillListForSessionWithIntent_SameAgentDifferentIntents(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "wire-intent-agent")

	skills := []*store.Skill{
		mustCreateSkill(t, s, &store.Skill{Name: "Research", Slug: "research", Description: "research things", ToolBindings: `[]`, ModeIDs: `[]`, Source: "builtin"}),
		mustCreateSkill(t, s, &store.Skill{Name: "Plan", Slug: "plan", Description: "plan a feature", ToolBindings: `[]`, ModeIDs: `[]`, Source: "builtin"}),
		mustCreateSkill(t, s, &store.Skill{Name: "Debug", Slug: "debug", Description: "debug code", ToolBindings: `[]`, ModeIDs: `[]`, Source: "builtin"}),
	}
	for _, sk := range skills {
		mustAssignSkill(t, s, agent.ID, sk.ID)
	}
	identity := skillbroker.AgentIdentity{ID: agent.ID, Slug: agent.Slug}

	intentDebug := contextbroker.Intent{
		Type:      contextbroker.IntentDebugIssue,
		Keywords:  []string{"debug"},
		QueryText: "debug this error",
	}
	intentPlan := contextbroker.Intent{
		Type:      contextbroker.IntentPlanFeature,
		Keywords:  []string{"plan"},
		QueryText: "plan the next feature",
	}

	gotDebug := buildSkillListForSessionWithIntent(context.Background(), s, agent.ID, "", intentDebug, identity)
	gotPlan := buildSkillListForSessionWithIntent(context.Background(), s, agent.ID, "", intentPlan, identity)

	// The FIRST rendered skill should be different across the two intents.
	firstDebug := firstSkillLine(gotDebug)
	firstPlan := firstSkillLine(gotPlan)

	if firstDebug == "" || firstPlan == "" {
		t.Fatalf("both renders should have at least one skill (got debug=%q, plan=%q)", gotDebug, gotPlan)
	}

	if firstDebug == firstPlan {
		t.Errorf("same agent + different intent should rank different top skill: both rendered %q first", firstDebug)
	}
	if !strings.HasPrefix(firstDebug, "- Debug") {
		t.Errorf("debug intent should rank Debug skill first, got line: %q", firstDebug)
	}
	if !strings.HasPrefix(firstPlan, "- Plan") {
		t.Errorf("plan intent should rank Plan skill first, got line: %q", firstPlan)
	}
}

// TestBuildSkillListForSessionWithIntent_AgentTagBiasesSelection verifies
// the agent-tag signal flows through the chat-helper layer. An agent
// tagged "planning" should see plan-named skills rank above unrelated.
func TestBuildSkillListForSessionWithIntent_AgentTagBiasesSelection(t *testing.T) {
	s := newTestStoreForChat(t)
	// Build the agent with a 'planning' tag via direct profile.
	agent := &store.AgentProfile{
		Slug:        "wire-tag-agent",
		Name:        "wire-tag-agent",
		Description: "test agent",
		Source:      "user",
		Tags:        `["planning"]`,
	}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	planSkill := mustCreateSkill(t, s, &store.Skill{
		Name: "Planning Helpers", Slug: "planning", Description: "planning helpers", ToolBindings: `[]`, ModeIDs: `[]`, Source: "builtin",
		Category: "planning",
	})
	alphaSkill := mustCreateSkill(t, s, &store.Skill{
		Name: "Alpha", Slug: "alpha", Description: "general", ToolBindings: `[]`, ModeIDs: `[]`, Source: "builtin",
	})
	mustAssignSkill(t, s, agent.ID, planSkill.ID)
	mustAssignSkill(t, s, agent.ID, alphaSkill.ID)

	identity := skillbroker.AgentIdentityFromProfile(agent)
	if len(identity.Tags) != 1 || identity.Tags[0] != "planning" {
		t.Fatalf("AgentIdentityFromProfile should parse Tags: got %v", identity.Tags)
	}

	got := buildSkillListForSessionWithIntent(context.Background(), s, agent.ID, "", contextbroker.Intent{}, identity)
	if !strings.Contains(got, "Planning Helpers") {
		t.Fatalf("Planning Helpers skill should render: %q", got)
	}
	idxPlan := strings.Index(got, "Planning Helpers")
	idxAlpha := strings.Index(got, "Alpha")
	if idxPlan == -1 || idxAlpha == -1 {
		t.Fatalf("both Planning Helpers and Alpha should render: %q", got)
	}
	if idxPlan >= idxAlpha {
		t.Errorf("tag-matched skill (Planning Helpers) should rank above unrelated (Alpha); got plan at %d, alpha at %d", idxPlan, idxAlpha)
	}
}

// TestBuildSkillListForSession_BackCompatNoIntent verifies the 3-arg
// signature still produces a deterministic render that downstream
// callers (legacy tests, background skill registration) rely on. With
// no intent + zero identity, the broker ranks by source-bias +
// alphabetical, which is stable across turns.
func TestBuildSkillListForSession_BackCompatNoIntent(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "wire-bc-agent")
	mustAssignSkill(t, s, agent.ID, mustCreateSkill(t, s, &store.Skill{Name: "Zeta", Slug: "z", Description: "x", ToolBindings: `[]`, ModeIDs: `[]`, Source: "plugin"}).ID)
	mustAssignSkill(t, s, agent.ID, mustCreateSkill(t, s, &store.Skill{Name: "Alpha", Slug: "a", Description: "x", ToolBindings: `[]`, ModeIDs: `[]`, Source: "user"}).ID)
	mustAssignSkill(t, s, agent.ID, mustCreateSkill(t, s, &store.Skill{Name: "Beta", Slug: "b", Description: "x", ToolBindings: `[]`, ModeIDs: `[]`, Source: "builtin"}).ID)

	got := buildSkillListForSession(context.Background(), s, agent.ID, "")
	// All three should appear (3 < cap).
	for _, name := range []string{"Alpha", "Beta", "Zeta"} {
		if !strings.Contains(got, name) {
			t.Errorf("expected %s in render: %q", name, got)
		}
	}
	// Source-bias sort: builtin (Beta) > user (Alpha) > plugin (Zeta).
	idxBeta := strings.Index(got, "Beta")
	idxAlpha := strings.Index(got, "Alpha")
	idxZeta := strings.Index(got, "Zeta")
	if !(idxBeta < idxAlpha && idxAlpha < idxZeta) {
		t.Errorf("source-bias order (Beta < Alpha < Zeta) not preserved: got beta=%d alpha=%d zeta=%d", idxBeta, idxAlpha, idxZeta)
	}
}

// firstSkillLine returns the first rendered "- Name: ..." line, or "" if
// none is present. Tolerant of leading LoadHint text or empty renders.
func firstSkillLine(rendered string) string {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, "- ") {
			return line
		}
	}
	return ""
}
