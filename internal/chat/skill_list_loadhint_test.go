package chat

import (
	"context"
	"log/slog"
	"strings"
	"sync"
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
	// Lines lead with the slug — the identifier skill_get takes.
	if !strings.Contains(got, "- asn-1: first") || !strings.Contains(got, "- asn-2: second") {
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

// D-37 (CW-20260919-0012): a listing line is slug + one bounded line of
// description, never a body. A multi-line or oversized description is
// collapsed so a skill can't spend the SlotSkills budget on its own.
func TestBuildSkillListForSession_DescriptionIsOneBoundedLine(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-long-desc")
	long := "Use when writing docs.\n\n## Body\n" + strings.Repeat("word ", 200)
	sk := mustCreateSkill(t, s, &store.Skill{Name: "Doc Writer", Slug: "doc-writer", Description: long})
	mustAssignSkill(t, s, agent.ID, sk.ID)

	got := buildSkillsSlotContent(context.Background(), s, agent.ID, "")
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "- doc-writer: ") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no listing line for doc-writer in: %q", got)
	}
	if !strings.HasPrefix(line, "- doc-writer: Use when writing docs. ## Body") {
		t.Errorf("description not collapsed to one line: %q", line)
	}
	if n := len([]rune(strings.TrimPrefix(line, "- doc-writer: "))); n > SkillDescriptionMaxRunes {
		t.Errorf("description %d runes, want <= %d", n, SkillDescriptionMaxRunes)
	}
	if !strings.Contains(got, "skill_get(slug)") {
		t.Errorf("slot header must name skill_get: %q", got)
	}
}

func TestBuildSkillsSlotContent_EmptyWhenNothingToList(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-no-skills")
	if got := buildSkillsSlotContent(context.Background(), s, agent.ID, ""); got != "" {
		t.Errorf("want empty slot for agent with no skills and empty catalog, got %q", got)
	}
}

// CW-20260929-0019: a granted skill with no catalog row is invisible to the
// agent, and that is logged, once per (agent, slug).
func TestBuildSkillListForSession_WarnsOnceOnDanglingGrant(t *testing.T) {
	s := newTestStoreForChat(t)
	agent := mustCreateAgent(t, s, "agent-dangling")
	installed := mustCreateSkill(t, s, &store.Skill{Name: "Real", Slug: "real", Description: "d"})
	mustAssignSkill(t, s, agent.ID, installed.ID)
	if _, err := s.DB.Exec(`INSERT INTO agent_known_skills (agent_id, skill_name) VALUES (?, 'ghost-skill')`, agent.ID); err != nil {
		t.Fatal(err)
	}

	sink := &syncBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for i := 0; i < 3; i++ {
		got := buildSkillListForSession(context.Background(), s, agent.ID, "")
		if !strings.Contains(got, "- real:") || strings.Contains(got, "ghost-skill") {
			t.Fatalf("listing = %q", got)
		}
	}
	if n := strings.Count(sink.String(), "ghost-skill"); n != 1 {
		t.Errorf("warning logged %d times over 3 turns, want once: %s", n, sink.String())
	}
	if !strings.Contains(sink.String(), agent.ID) {
		t.Errorf("warning does not name the agent: %s", sink.String())
	}
}

type syncBuf struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
