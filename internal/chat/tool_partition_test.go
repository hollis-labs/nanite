package chat

import (
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
)

// G-HOT-SWAP-DEAD activation tests. Every assertion targets one of the
// locked decisions: cap-driven partition, four-rule selection, hysteresis
// stability, request_tools idempotency, LoadHint format.

func mkTools(names ...string) []llmtypes.ToolDefinition {
	out := make([]llmtypes.ToolDefinition, len(names))
	for i, n := range names {
		out[i] = llmtypes.ToolDefinition{
			Name:        n,
			Description: "tool " + n,
		}
	}
	return out
}

func tnames(td []llmtypes.ToolDefinition) []string {
	out := make([]string, len(td))
	for i, t := range td {
		out[i] = t.Name
	}
	return out
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func TestPartitionTools_BelowCapKeepsEverythingEssential(t *testing.T) {
	tools := mkTools("a", "b", "c", "d")
	p, st := PartitionTools(tools, store.ToolOverrideSpec{}, nil, ToolPartitionState{}, ToolEssentialCap)

	if len(p.Lazy) != 0 {
		t.Errorf("lazy should be empty when input <= cap; got %v", tnames(p.Lazy))
	}
	if len(p.Essential) != len(tools) {
		t.Errorf("essential should be all input tools; got %v", tnames(p.Essential))
	}
	if st.Turn != 1 {
		t.Errorf("turn counter should be 1 on first call; got %d", st.Turn)
	}
}

func TestPartitionTools_OverCapTrimsToCap(t *testing.T) {
	// 30 tools, cap=25 → 25 essential, 5 lazy. No mode/recent/hyst signals
	// → filler-cap deterministic in input order.
	names := make([]string, 30)
	for i := range names {
		names[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	tools := mkTools(names...)
	p, _ := PartitionTools(tools, store.ToolOverrideSpec{}, nil, ToolPartitionState{}, 25)

	if len(p.Essential) != 25 {
		t.Errorf("expected 25 essential, got %d", len(p.Essential))
	}
	if len(p.Lazy) != 5 {
		t.Errorf("expected 5 lazy, got %d", len(p.Lazy))
	}
	// First 25 input names should be essential (input order preserved on tie).
	for i := 0; i < 25; i++ {
		if p.Essential[i].Name != names[i] {
			t.Errorf("essential[%d] = %q, want %q (input order on ties)", i, p.Essential[i].Name, names[i])
		}
	}
}

func TestPartitionTools_ModeAllowPromotesAboveCap(t *testing.T) {
	// 30 input tools; mode.Allow names 3 tools that are at the *end* of
	// the input list. They should be promoted into essential — they'd
	// otherwise be filler-cap-trimmed.
	names := make([]string, 30)
	for i := range names {
		names[i] = "tool_" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	tools := mkTools(names...)
	mode := store.ToolOverrideSpec{
		Allow: []string{names[27], names[28], names[29]},
	}
	p, _ := PartitionTools(tools, mode, nil, ToolPartitionState{}, 25)

	if len(p.Essential) != 25 {
		t.Fatalf("expected 25 essential, got %d", len(p.Essential))
	}
	for _, want := range mode.Allow {
		if !contains(tnames(p.Essential), want) {
			t.Errorf("mode-allow %q should be essential; got %v", want, tnames(p.Essential))
		}
	}
}

func TestPartitionTools_RecentlyUsedPromotesAboveCap(t *testing.T) {
	names := make([]string, 30)
	for i := range names {
		names[i] = "tool_" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	tools := mkTools(names...)
	recent := []string{names[26], names[27], names[28]}
	p, _ := PartitionTools(tools, store.ToolOverrideSpec{}, recent, ToolPartitionState{}, 25)

	for _, want := range recent {
		if !contains(tnames(p.Essential), want) {
			t.Errorf("recently-used %q should be essential; got %v", want, tnames(p.Essential))
		}
	}
}

func TestPartitionTools_ModeAllowPatternsPromotion(t *testing.T) {
	// 30 tools; mode.AllowPatterns says hadron_*. 3 hadron_* are at the
	// end and must be promoted.
	names := []string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t",
		"u", "v", "w", "x", "y", "z", "aa", "hadron_run", "hadron_blueprint", "hadron_inspect",
	}
	tools := mkTools(names...)
	mode := store.ToolOverrideSpec{
		AllowPatterns: []string{"hadron_*"},
	}
	p, _ := PartitionTools(tools, mode, nil, ToolPartitionState{}, 25)

	for _, want := range []string{"hadron_run", "hadron_blueprint", "hadron_inspect"} {
		if !contains(tnames(p.Essential), want) {
			t.Errorf("hadron_* match %q should be essential; got %v", want, tnames(p.Essential))
		}
	}
}

func TestPartitionTools_MetaToolsAlwaysEssential(t *testing.T) {
	names := []string{"request_tools", "fetch_tool_result"}
	for i := 0; i < 30; i++ {
		names = append(names, string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	tools := mkTools(names...)
	p, _ := PartitionTools(tools, store.ToolOverrideSpec{}, nil, ToolPartitionState{}, 5)

	for _, meta := range []string{"request_tools", "fetch_tool_result"} {
		if !contains(tnames(p.Essential), meta) {
			t.Errorf("meta-tool %q must always be essential; got %v", meta, tnames(p.Essential))
		}
		if contains(tnames(p.Lazy), meta) {
			t.Errorf("meta-tool %q must not be lazy", meta)
		}
	}
}

func TestPartitionTools_HysteresisPinsAcrossTurns(t *testing.T) {
	// Turn 1: tool X is recently-used and within cap → essential.
	// Turn 2..5: X is no longer recently-used; without hysteresis it'd be
	// trimmed by overflow (other tools competing). With hysteresis, X stays
	// essential for ToolHysteresisFloor turns.
	names := make([]string, 30)
	for i := range names {
		names[i] = "tool_" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	pinned := names[27]
	tools := mkTools(names...)

	// Turn 1: pinned is recently-used.
	p, st := PartitionTools(tools, store.ToolOverrideSpec{}, []string{pinned}, ToolPartitionState{}, 25)
	if !contains(tnames(p.Essential), pinned) {
		t.Fatalf("turn 1: pinned should be essential, got %v", tnames(p.Essential))
	}

	// Turns 2..ToolHysteresisFloor: no recent signal. Hysteresis keeps it.
	for turn := 2; turn <= ToolHysteresisFloor; turn++ {
		p, st = PartitionTools(tools, store.ToolOverrideSpec{}, nil, st, 25)
		if !contains(tnames(p.Essential), pinned) {
			t.Errorf("turn %d (within hysteresis floor): pinned should still be essential", turn)
		}
	}

	// Turn ToolHysteresisFloor+1: hysteresis expired (Turn - first >= floor).
	// Pinned can now drop to lazy if filler-cap doesn't reach it.
	p, _ = PartitionTools(tools, store.ToolOverrideSpec{}, nil, st, 25)
	// 30 tools, 25 cap, names[0..24] are filler-essential; names[25..29] go
	// lazy. pinned = names[27], so after hysteresis expiry it should be lazy.
	if !contains(tnames(p.Lazy), pinned) {
		t.Errorf("turn after hysteresis expiry: pinned should be lazy when filler doesn't reach it; got essential=%v lazy=%v",
			tnames(p.Essential), tnames(p.Lazy))
	}
}

func TestPartitionTools_EmptyInput(t *testing.T) {
	p, st := PartitionTools(nil, store.ToolOverrideSpec{}, nil, ToolPartitionState{}, 25)
	if len(p.Essential) != 0 || len(p.Lazy) != 0 {
		t.Errorf("empty input → empty partition; got %v / %v", tnames(p.Essential), tnames(p.Lazy))
	}
	if st.Turn != 1 {
		t.Errorf("turn should advance on empty input; got %d", st.Turn)
	}
}

func TestPartitionTools_CapZeroFallsBackToDefault(t *testing.T) {
	tools := mkTools("a", "b", "c")
	p, _ := PartitionTools(tools, store.ToolOverrideSpec{}, nil, ToolPartitionState{}, 0)
	// 3 tools, default cap = 25 → all essential.
	if len(p.Essential) != 3 || len(p.Lazy) != 0 {
		t.Errorf("cap=0 should fall back to ToolEssentialCap=%d; got essential=%d lazy=%d",
			ToolEssentialCap, len(p.Essential), len(p.Lazy))
	}
}

func TestRecentlyUsedToolNames_ScansAssistantToolUseBlocks(t *testing.T) {
	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: "do thing 1"},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "text", Text: "ok"},
			{Type: "tool_use", Name: "alpha"},
		}},
		{Role: "tool", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result"}}},
		{Role: "user", Content: "another"},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", Name: "beta"},
			{Type: "tool_use", Name: "gamma"},
		}},
	}
	got := RecentlyUsedToolNames(msgs, 3)
	// Reverse-recency: most recent assistant turn first.
	want := []string{"beta", "gamma", "alpha"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("[%d]: got %q, want %q", i, got[i], n)
		}
	}
}

func TestRecentlyUsedToolNames_DedupsAcrossTurns(t *testing.T) {
	msgs := []llmtypes.ChatMessage{
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", Name: "alpha"}}},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", Name: "alpha"},
			{Type: "tool_use", Name: "beta"},
		}},
	}
	got := RecentlyUsedToolNames(msgs, 3)
	if len(got) != 2 {
		t.Errorf("got %d unique names, want 2; got=%v", len(got), got)
	}
}

func TestRecentlyUsedToolNames_RespectsTurnLimit(t *testing.T) {
	msgs := []llmtypes.ChatMessage{
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", Name: "old1"}}},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", Name: "old2"}}},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", Name: "old3"}}},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", Name: "old4"}}},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", Name: "recent"}}},
	}
	got := RecentlyUsedToolNames(msgs, 2)
	// 2-turn window starting from newest → recent + old4.
	if len(got) != 2 || got[0] != "recent" || got[1] != "old4" {
		t.Errorf("got %v, want [recent old4]", got)
	}
}

func TestRenderToolLazyHint_FormatLockedPerPrompt(t *testing.T) {
	lazy := mkTools("foo", "bar", "baz")
	got := RenderToolLazyHint(lazy)
	if !strings.Contains(got, "Tool catalog (lazy): 3 tools available") {
		t.Errorf("hint missing count phrase; got %q", got)
	}
	if !strings.Contains(got, "request_tools") {
		t.Errorf("hint missing request_tools reference; got %q", got)
	}
	if !strings.Contains(got, "foo, bar, baz") {
		t.Errorf("hint missing tool names; got %q", got)
	}
}

func TestRenderToolLazyHint_EmptyReturnsEmpty(t *testing.T) {
	if got := RenderToolLazyHint(nil); got != "" {
		t.Errorf("empty lazy → empty hint; got %q", got)
	}
}

func TestIsToolsLazyLoadEnabled_DefaultOff(t *testing.T) {
	t.Setenv("NANITE_TOOLS_LAZY_LOAD", "")
	if IsToolsLazyLoadEnabled() {
		t.Errorf("default should be OFF; got ON")
	}
}

func TestIsToolsLazyLoadEnabled_TrueValues(t *testing.T) {
	for _, v := range []string{"true", "1", "yes", "on", "TRUE", "Yes"} {
		t.Setenv("NANITE_TOOLS_LAZY_LOAD", v)
		if !IsToolsLazyLoadEnabled() {
			t.Errorf("value %q should enable; got disabled", v)
		}
	}
}

func TestIsToolsLazyLoadEnabled_FalsyValues(t *testing.T) {
	for _, v := range []string{"false", "0", "no", "off", "random"} {
		t.Setenv("NANITE_TOOLS_LAZY_LOAD", v)
		if IsToolsLazyLoadEnabled() {
			t.Errorf("value %q should not enable; got enabled", v)
		}
	}
}

// Integration-shaped: token-savings sanity check. With 50 tools, cap=25,
// the "essential surface" plus the lazy hint should collectively be much
// smaller than naive serialization of all 50 tool defs.
func TestPartitionTools_TokenSavingsScale(t *testing.T) {
	tools := make([]llmtypes.ToolDefinition, 50)
	for i := range tools {
		tools[i] = llmtypes.ToolDefinition{
			Name:        "t" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Description: strings.Repeat("filler description blob ", 32), // ~768 bytes per tool
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}
	}
	p, _ := PartitionTools(tools, store.ToolOverrideSpec{}, nil, ToolPartitionState{}, 25)
	hint := RenderToolLazyHint(p.Lazy)
	if len(p.Essential) != 25 || len(p.Lazy) != 25 {
		t.Fatalf("expected 25/25 split, got %d/%d", len(p.Essential), len(p.Lazy))
	}

	// Estimate: each lazy tool's full description is dropped from inline
	// tokens. Hint is just names + a short banner. Hint should be a small
	// fraction (< 10%) of the saved description bytes.
	savedBytes := 0
	for _, t := range p.Lazy {
		savedBytes += len(t.Description)
	}
	if savedBytes <= 0 {
		t.Fatal("expected lazy descriptions to dominate saved bytes")
	}
	if len(hint)*10 > savedBytes {
		t.Errorf("lazy hint should be at most ~10%% of saved description bytes; hint=%d bytes, saved=%d bytes",
			len(hint), savedBytes)
	}
}
