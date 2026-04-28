package toolclient

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/go-toolbroker/broker"
)

// stubMemoryRecaller is a deterministic MemoryRecaller for tests. It returns
// a fixed list of hits keyed by intent substring; "" intent returns nothing.
type stubMemoryRecaller struct {
	hitsByIntent map[string][]ToolPatternHit
	written      []recordedPattern
}

type recordedPattern struct {
	sessionID string
	intent    string
	sequence  []string
	outcome   string
}

func (s *stubMemoryRecaller) RecallToolPatterns(_ context.Context, intent string) ([]ToolPatternHit, error) {
	for k, v := range s.hitsByIntent {
		if strings.Contains(intent, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (s *stubMemoryRecaller) RecordToolPattern(_ context.Context, sessionID, intent string, sequence []string, outcome string) error {
	s.written = append(s.written, recordedPattern{sessionID, intent, sequence, outcome})
	return nil
}

func TestRankTools_SkillsBeatKeyword(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "dev_glob", Description: "find files matching a pattern"},
		{Name: "dev_grep", Description: "regex search through files"},
		{Name: "context_search", Description: "search context store for items"},
	}
	skills := []ToolPreferenceSkill{
		{Pattern: "search", Prefer: []string{"dev_glob", "dev_grep"}, Weight: 7},
	}
	scored := RankTools(RankingSignals{
		Skills:       MatchingSkills(skills, "search code"),
		BrokerResult: tools,
	})
	if len(scored) < 2 {
		t.Fatalf("expected ranked output, got %d", len(scored))
	}
	if scored[0].Tool.Name != "dev_glob" && scored[0].Tool.Name != "dev_grep" {
		t.Errorf("expected dev_* skill prefer at top, got %q", scored[0].Tool.Name)
	}
	if scored[0].SkillScore == 0 {
		t.Errorf("top tool should have non-zero skill score, got %d", scored[0].SkillScore)
	}
}

func TestRankTools_MemoryAddsHit(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "dev_read", Description: "read a file"},
		{Name: "noise_tool", Description: "irrelevant"},
	}
	scored := RankTools(RankingSignals{
		MemoryHits: []ToolPatternHit{
			{ToolName: "dev_read", Confidence: 1.0, Source: "test_pattern"},
		},
		BrokerResult: tools,
	})
	// dev_read should be ranked first by memory signal.
	if scored[0].Tool.Name != "dev_read" {
		t.Errorf("expected dev_read top by memory hit, got %q", scored[0].Tool.Name)
	}
	if scored[0].MemoryScore == 0 {
		t.Errorf("expected non-zero memory score on top tool, got %d", scored[0].MemoryScore)
	}
}

func TestRankTools_SkillReferenceUnknownToolIgnored(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "real_tool", Description: "a tool the broker knows"},
	}
	skills := []ToolPreferenceSkill{
		{Pattern: "anything", Prefer: []string{"ghost_tool", "real_tool"}, Weight: 5, Source: "test"},
	}
	scored := RankTools(RankingSignals{
		Skills:       MatchingSkills(skills, "anything"),
		BrokerResult: tools,
	})
	if len(scored) != 1 {
		t.Fatalf("expected 1 scored tool (ghost_tool dropped), got %d", len(scored))
	}
	if scored[0].Tool.Name != "real_tool" {
		t.Errorf("expected real_tool, got %q", scored[0].Tool.Name)
	}
}

func TestSelectWithSignals_TightBudgetBoundsSelection(t *testing.T) {
	cfg := DefaultConfig()
	// Override the budget knobs to a very tight setup: 1000-token window
	// times 0.20 = 200 tokens budget, which fits 5-10 tools at most.
	cfg.ContextWindowTokens = 1000
	cfg.ToolTokenBudgetPct = 0.20
	cfg.ErrTowardMorePad = 0 // disable bias for the bound test

	tb := New(nil, nil, cfg)

	// Register many bulky tools so the budget actually bites.
	defs := make([]broker.ToolDefinition, 30)
	for i := range defs {
		defs[i] = broker.ToolDefinition{
			Name:        sprintfName(i),
			Description: strings.Repeat("padding ", 20), // ~30 tokens each
		}
	}
	tb.RegisterTools(defs)

	final, _, signals, err := tb.SelectWithSignals(
		context.Background(),
		"general", nil, "", "", 1000,
		nil, nil, 0,
	)
	if err != nil {
		t.Fatalf("SelectWithSignals: %v", err)
	}
	if len(final) == 0 {
		t.Fatal("expected at least one tool")
	}
	if EstimateToolTokens(final) > 200 {
		// PruneToolsToTokenBudget is allowed to keep at least 1 tool even
		// if it exceeds the budget. Validate the smaller-of guarantee:
		// either we are under budget OR we kept exactly 1.
		if len(final) != 1 {
			t.Errorf("expected under-budget OR 1-tool fallback; got len=%d tokens=%d",
				len(final), EstimateToolTokens(final))
		}
	}
	if signals == "" {
		t.Error("expected diagnostic signals JSON, got empty string")
	}
}

func TestSelectWithSignals_GenerousBudgetPadsExtra(t *testing.T) {
	cfg := DefaultConfig()
	// Generous budget so the err-toward-more pad fits comfortably.
	cfg.ContextWindowTokens = 200000
	cfg.ToolTokenBudgetPct = 0.20

	tb := New(nil, nil, cfg)

	// Mix of tools — the skill below promotes 2 of these to the strict tier.
	defs := []broker.ToolDefinition{
		{Name: "dev_glob", Description: "find files matching glob pattern"},
		{Name: "dev_grep", Description: "search code with regex"},
		{Name: "filler_one", Description: "unrelated tool one"},
		{Name: "filler_two", Description: "unrelated tool two"},
		{Name: "filler_three", Description: "unrelated tool three"},
		{Name: "filler_four", Description: "unrelated tool four"},
	}
	tb.RegisterTools(defs)

	// Skill: only dev_glob and dev_grep score in strict.
	skills := []ToolPreferenceSkill{
		{Pattern: "search", Prefer: []string{"dev_glob", "dev_grep"}, Weight: 7, Source: "test"},
	}

	// First: pad=0 → strict only (2 dev_* tools).
	strict, _, _, err := tb.SelectWithSignals(
		context.Background(),
		"search code", nil, "", "", 200000,
		skills, nil, 0,
	)
	if err != nil {
		t.Fatalf("strict SelectWithSignals: %v", err)
	}
	if len(strict) != 2 {
		t.Errorf("strict tier should be 2 (dev_glob, dev_grep); got %d (%v)", len(strict), strict)
	}

	// Second: pad=3 → strict + up to 3 zero-score extras.
	padded, _, _, err := tb.SelectWithSignals(
		context.Background(),
		"search code", nil, "", "", 200000,
		skills, nil, 3,
	)
	if err != nil {
		t.Fatalf("padded SelectWithSignals: %v", err)
	}

	if len(padded) <= len(strict) {
		t.Errorf("err-toward-more pad should add candidates: strict=%d padded=%d",
			len(strict), len(padded))
	}
	if len(padded) > len(strict)+3 {
		t.Errorf("pad should be capped at 3: got %d extras", len(padded)-len(strict))
	}
}

// sprintfName generates a unique 8-char name for the tightness test.
func sprintfName(i int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	a := alphabet[i%26]
	b := alphabet[(i/26)%26]
	c := alphabet[(i/676)%26]
	return string([]byte{a, b, c, '_', 't', 'o', 'o', 'l'})
}
