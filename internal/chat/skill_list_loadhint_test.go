package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// Glass-5 (CW-20260502-0012) — sanity tests for the catalog-discoverability
// LoadHint and the SkillEssentialCap. The LoadHint partition is the
// hot-swap primitive: assigned skills inline, broader catalog reachable
// via skill_list / tool_list. Tests pin both halves.

const loadHintMarker = "additional skills are available"

func TestBuildSkillListForSession_LoadHintWithZeroAssigned(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-zero-assigned")
	mustCreateSkill(t, s, &store.Skill{Name: "Catalog A", Slug: "cat-a", Description: "first"})
	mustCreateSkill(t, s, &store.Skill{Name: "Catalog B", Slug: "cat-b", Description: "second"})
	mustCreateSkill(t, s, &store.Skill{Name: "Catalog C", Slug: "cat-c", Description: "third"})
	// No assignments — rendered=0, catalog=3, discoverable=3.

	got := buildSkillListForSession(context.Background(), s, agent.ID, "")
	if !strings.Contains(got, loadHintMarker) {
		t.Errorf("expected LoadHint when zero assigned + non-empty catalog, got: %q", got)
	}
	if !strings.Contains(got, "3 additional") {
		t.Errorf("expected discoverable count 3, got: %q", got)
	}
	if strings.Contains(got, "Catalog A") || strings.Contains(got, "Catalog B") {
		t.Errorf("LoadHint must not name skills, got: %q", got)
	}
}

func TestBuildSkillListForSession_LoadHintWhenCatalogHasMore(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-some-assigned")
	a := mustCreateSkill(t, s, &store.Skill{Name: "Assigned-1", Slug: "asn-1", Description: "first"})
	b := mustCreateSkill(t, s, &store.Skill{Name: "Assigned-2", Slug: "asn-2", Description: "second"})
	mustCreateSkill(t, s, &store.Skill{Name: "Catalog-Only-1", Slug: "co-1", Description: "x"})
	mustCreateSkill(t, s, &store.Skill{Name: "Catalog-Only-2", Slug: "co-2", Description: "y"})
	mustCreateSkill(t, s, &store.Skill{Name: "Catalog-Only-3", Slug: "co-3", Description: "z"})
	mustAssignSkill(t, s, agent.ID, a.ID)
	mustAssignSkill(t, s, agent.ID, b.ID)
	// rendered=2, catalog=5, discoverable=3.

	got := buildSkillListForSession(context.Background(), s, agent.ID, "")
	if !strings.Contains(got, "Assigned-1") || !strings.Contains(got, "Assigned-2") {
		t.Errorf("essentials missing from rendered list: %q", got)
	}
	if !strings.Contains(got, "3 additional") {
		t.Errorf("expected discoverable count 3, got: %q", got)
	}
	if strings.Contains(got, "Catalog-Only-1") {
		t.Errorf("LoadHint must not enumerate non-essentials, got: %q", got)
	}
}

func TestBuildSkillListForSession_NoLoadHintWhenCatalogExhausted(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-all-assigned")
	a := mustCreateSkill(t, s, &store.Skill{Name: "Only-1", Slug: "only-1", Description: "x"})
	b := mustCreateSkill(t, s, &store.Skill{Name: "Only-2", Slug: "only-2", Description: "y"})
	mustAssignSkill(t, s, agent.ID, a.ID)
	mustAssignSkill(t, s, agent.ID, b.ID)
	// rendered=2, catalog=2, discoverable=0 → no hint.

	got := buildSkillListForSession(context.Background(), s, agent.ID, "")
	if strings.Contains(got, loadHintMarker) {
		t.Errorf("LoadHint should not render when catalog == rendered, got: %q", got)
	}
}

func TestBuildSkillListForSession_EssentialCapEnforced(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-overflow")
	overflowExtra := 5
	total := SkillEssentialCap + overflowExtra
	for i := 0; i < total; i++ {
		// Names with leading zero-pad so ORDER BY name produces a stable
		// "first SkillEssentialCap rendered" outcome we can assert.
		name := nameForIdx(i)
		sk := mustCreateSkill(t, s, &store.Skill{Name: name, Slug: name, Description: "x"})
		mustAssignSkill(t, s, agent.ID, sk.ID)
	}

	got := buildSkillListForSession(context.Background(), s, agent.ID, "")

	// First SkillEssentialCap names must be present (sk-000, sk-001, …).
	for i := 0; i < SkillEssentialCap; i++ {
		if !strings.Contains(got, nameForIdx(i)+":") {
			t.Errorf("expected %q rendered inline, got: %q", nameForIdx(i), got)
		}
	}
	// Overflow names (last `overflowExtra` skills) must NOT appear inline.
	for i := SkillEssentialCap; i < total; i++ {
		if strings.Contains(got, nameForIdx(i)+":") {
			t.Errorf("skill %q should be folded into LoadHint, not inlined: %q", nameForIdx(i), got)
		}
	}
	// LoadHint discoverable count must equal overflow (catalog-rendered = 5).
	if !strings.Contains(got, "5 additional") {
		t.Errorf("expected discoverable count 5 (overflow), got: %q", got)
	}
}

func TestBuildSkillListForSession_LoadHintTokenBudget(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-token-budget")
	mustCreateSkill(t, s, &store.Skill{Name: "Catalog-1", Slug: "c-1", Description: "x"})
	// rendered=0, catalog=1, discoverable=1.

	got := buildSkillListForSession(context.Background(), s, agent.ID, "")
	if !strings.Contains(got, loadHintMarker) {
		t.Fatalf("LoadHint missing: %q", got)
	}
	if tokens := EstimateTokens(got); tokens >= 200 {
		t.Errorf("LoadHint-only contribution must stay <200 tokens, got %d (text: %q)", tokens, got)
	}
}

func TestBuildSkillListForSession_LoadHintReferencesRealTools(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-real-tools")
	mustCreateSkill(t, s, &store.Skill{Name: "Catalog-X", Slug: "c-x", Description: "x"})

	got := buildSkillListForSession(context.Background(), s, agent.ID, "")
	for _, tool := range []string{"skill_list", "tool_list"} {
		if !strings.Contains(got, tool) {
			t.Errorf("LoadHint must reference %q tool, got: %q", tool, got)
		}
	}
}

// nameForIdx returns a sortable, contains-safe skill name for cap tests.
func nameForIdx(i int) string {
	const a = '0'
	hundreds := byte(a + (i/100)%10)
	tens := byte(a + (i/10)%10)
	ones := byte(a + i%10)
	return "sk-" + string([]byte{hundreds, tens, ones})
}
