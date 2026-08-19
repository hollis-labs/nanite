package toolclient

import (
	"context"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

func TestSelectToolsAugmented_MemoryHitsLiftRanking(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ContextWindowTokens = 200000
	tb := New(nil, nil, cfg)

	defs := []llmtypes.ToolDefinition{
		{Name: "memory_get", Description: "fetch a single memory"},
		{Name: "memory_recall", Description: "rank memories by activation"},
		{Name: "filler_a", Description: "unrelated"},
		{Name: "filler_b", Description: "unrelated"},
	}
	tb.RegisterTools(defs)

	// Stub recaller → returns memory_recall as a hit when intent contains "memory".
	tb.SetMemoryRecaller(&stubMemoryRecaller{
		hitsByIntent: map[string][]ToolPatternHit{
			"memory": {{ToolName: "memory_recall", Confidence: 1.0, Source: "test"}},
		},
	})

	final, signals, err := tb.SelectToolsAugmented(
		context.Background(),
		"search memory for prior decisions", nil, "", "", 200000,
	)
	if err != nil {
		t.Fatalf("SelectToolsAugmented: %v", err)
	}
	if len(final) == 0 {
		t.Fatal("expected at least one tool")
	}
	// memory_recall should appear in the strict tier (non-zero score).
	found := false
	for _, t := range final {
		if t.Name == "memory_recall" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("memory_recall should be promoted by memory hit; got %v", defNames(final))
	}
	if signals == "" {
		t.Error("expected diagnostic signals JSON")
	}
}

func TestSelectToolsAugmented_NilRecallerIsSafe(t *testing.T) {
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	defs := []llmtypes.ToolDefinition{
		{Name: "any_tool", Description: "noop"},
	}
	tb.RegisterTools(defs)

	final, _, err := tb.SelectToolsAugmented(
		context.Background(),
		"any intent", nil, "", "", 200000,
	)
	if err != nil {
		t.Fatalf("nil recaller path errored: %v", err)
	}
	if len(final) == 0 {
		t.Errorf("nil recaller should not gate selection; got 0 tools")
	}
}

func TestRecordToolPattern_NoOpWhenSequenceEmpty(t *testing.T) {
	r := &memoryRecaller{} // svc is nil, sequence is empty
	if err := r.RecordToolPattern(context.Background(), "s", "i", nil, "ok"); err != nil {
		t.Errorf("expected nil error from no-op record, got %v", err)
	}
}

func TestParseToolNames(t *testing.T) {
	cases := map[string][]string{
		"":                    nil,
		"a":                   {"a"},
		"a, b , c":            {"a", "b", "c"},
		"a, bad-name, c":      {"a", "c"},
		"a, , c":              {"a", "c"},
	}
	for in, want := range cases {
		got := parseToolNames(in)
		if !sliceEqual(got, want) {
			t.Errorf("parseToolNames(%q) = %v, want %v", in, got, want)
		}
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func defNames(in []llmtypes.ToolDefinition) []string {
	out := make([]string, len(in))
	for i, t := range in {
		out[i] = t.Name
	}
	return out
}
