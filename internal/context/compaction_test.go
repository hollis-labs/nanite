package context

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
)

// mockSummarizer returns a canned summary.
type mockSummarizer struct {
	called bool
}

func (m *mockSummarizer) Summarize(ctx context.Context, systemPrompt string, messages []provider.ChatMessage) (string, error) {
	m.called = true
	return fmt.Sprintf("Summary of %d messages.", len(messages)), nil
}

func makeMessages(n int, tokensPer int) []provider.ChatMessage {
	msgs := make([]provider.ChatMessage, n)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = provider.ChatMessage{
			Role:    role,
			Content: strings.Repeat("x", tokensPer*4), // 4 bytes per token
		}
	}
	return msgs
}

func TestCompactionPipeline_noCompactionNeeded(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	cw.SetContent(SlotSystem, "system")
	cw.SetContent(SlotConversation, "short conversation")

	p := &CompactionPipeline{
		Window:               cw,
		Estimator:            DefaultEstimator{},
		Mode:                 CompactionModeGeneral,
		ConversationMessages: []provider.ChatMessage{{Role: "user", Content: "hello"}},
	}

	result, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Error("expected nil result when no compaction needed")
	}
}

// TestStageDedupeToolResults verifies that identical tool_call+tool_result
// pairs get collapsed to a pointer-back reference. Deterministic, no LLM.
// CW-20260419-0004 Part 2.
func TestStageDedupeToolResults(t *testing.T) {
	input := map[string]any{"path": "/Users/x/foo.md"}
	bigResult := strings.Repeat("x", 4000)

	msgs := []provider.ChatMessage{
		// Turn 1: tool_use + tool_result (the original).
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "call-1", Name: "dev_read", Input: &input},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "call-1", Content: bigResult},
		}},
		// Turn 2: identical call — should become dedupe target.
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "call-2", Name: "dev_read", Input: &input},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "call-2", Content: bigResult},
		}},
		// Turn 3: different input — unique, should survive intact.
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "call-3", Name: "dev_read", Input: &map[string]any{"path": "/Users/x/bar.md"}},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "call-3", Content: bigResult},
		}},
	}

	p := &CompactionPipeline{
		ConversationMessages: msgs,
	}

	progress, err := stageDedupeToolResults(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !progress {
		t.Fatal("expected progress=true when duplicates present")
	}

	// Turn 1 (original) — untouched.
	if p.ConversationMessages[1].ContentBlocks[0].Content != bigResult {
		t.Error("original tool_result should be untouched")
	}
	// Turn 2 (duplicate) — replaced with pointer.
	dupContent := p.ConversationMessages[3].ContentBlocks[0].Content
	if !strings.Contains(dupContent, "DUPLICATE") || !strings.Contains(dupContent, "call-1") {
		t.Errorf("duplicate tool_result should point back to call-1; got %q", dupContent)
	}
	if len(dupContent) > 200 {
		t.Errorf("duplicate pointer should be short; got %d bytes", len(dupContent))
	}
	// Turn 3 (unique) — untouched.
	if p.ConversationMessages[5].ContentBlocks[0].Content != bigResult {
		t.Error("unique tool_result should be untouched")
	}

	// Second call with no new duplicates → no progress.
	progress, err = stageDedupeToolResults(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if progress {
		t.Error("idempotency: dedupe should not report progress on a second pass")
	}
}

// TestStageDedupeToolResults_DifferentResultsNotDeduped guards against
// silent data loss when the same tool is called twice with the same input
// but the result content differs between calls (e.g., a file was edited
// between reads). Dedupe must match on (name, input, result content) so
// genuinely changed state is preserved.
func TestStageDedupeToolResults_DifferentResultsNotDeduped(t *testing.T) {
	input := map[string]any{"path": "/Users/x/foo.md"}
	msgs := []provider.ChatMessage{
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "call-1", Name: "dev_read", Input: &input},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "call-1", Content: "original content"},
		}},
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "call-2", Name: "dev_read", Input: &input},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "call-2", Content: "content after edit"},
		}},
	}
	p := &CompactionPipeline{ConversationMessages: msgs}
	progress, err := stageDedupeToolResults(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if progress {
		t.Error("differing tool_result content must not be deduped")
	}
	if p.ConversationMessages[1].ContentBlocks[0].Content != "original content" {
		t.Error("first tool_result should be untouched")
	}
	if p.ConversationMessages[3].ContentBlocks[0].Content != "content after edit" {
		t.Errorf("second tool_result must not be replaced with a pointer; got %q",
			p.ConversationMessages[3].ContentBlocks[0].Content)
	}
}

// TestCanonicalizeToolInput_StableForNestedMaps guards against nondeterministic
// signatures from fmt.Sprintf("%v", ...) on nested maps (Go randomizes map
// iteration order). Many invocations on the same input must produce identical
// output.
func TestCanonicalizeToolInput_StableForNestedMaps(t *testing.T) {
	input := map[string]any{
		"nested": map[string]any{
			"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8,
		},
		"list": []any{
			map[string]any{"k1": "v1", "k2": "v2", "k3": "v3"},
			map[string]any{"x": 1, "y": 2, "z": 3},
		},
	}
	first := canonicalizeToolInput(&input)
	for i := 0; i < 200; i++ {
		got := canonicalizeToolInput(&input)
		if got != first {
			t.Fatalf("iteration %d: canonicalize not stable\nfirst: %s\ngot:   %s", i, first, got)
		}
	}
}

// TestStageDedupeToolResults_NoDuplicates verifies the stage is a no-op
// when every tool invocation is unique.
func TestStageDedupeToolResults_NoDuplicates(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "c1", Name: "dev_read", Input: &map[string]any{"path": "/a"}},
		}},
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "c2", Name: "dev_read", Input: &map[string]any{"path": "/b"}},
		}},
	}
	p := &CompactionPipeline{ConversationMessages: msgs}
	progress, err := stageDedupeToolResults(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if progress {
		t.Error("no duplicates → no progress")
	}
}

// TestCompactionPipeline_RunForce_bypassesGate confirms that RunForce runs
// stages even when the window reports NeedsCompaction()=false. This is the
// invariant the rate-budget recovery path depends on (PR #68): a request
// can exceed the per-minute rate budget while the model context window is
// nowhere near capacity, and we still need the stages to run. Regression
// guard against the c9 UAT failure where Run() returned nil in <1ms
// without applying any stages.
func TestCompactionPipeline_RunForce_bypassesGate(t *testing.T) {
	// Large window, small content — NeedsCompaction is false.
	cw := NewContextWindow(200_000, nil)
	cw.SetContent(SlotSystem, "system")
	cw.SetContent(SlotContext, strings.Repeat("x", 2000)) // 500 tokens of enrichment
	msgs := makeMessages(6, 200)                          // 1200 tokens — well under budget
	cw.SetContent(SlotConversation, serializeMessages(msgs, DefaultEstimator{}))

	if cw.NeedsCompaction() {
		t.Fatal("precondition: NeedsCompaction must be false for this test")
	}

	p := &CompactionPipeline{
		Window:               cw,
		Estimator:            DefaultEstimator{},
		Summarizer:           &mockSummarizer{},
		Mode:                 CompactionModeGeneral,
		ConversationMessages: msgs,
	}

	// Run() must be a no-op — proves the gate is engaged.
	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() err: %v", err)
	}
	if res != nil {
		t.Errorf("Run() should return nil when NeedsCompaction=false; got %+v", res)
	}

	// RunForce must still apply stages — proves the rate-budget path works.
	forced, err := p.RunForce(context.Background())
	if err != nil {
		t.Fatalf("RunForce err: %v", err)
	}
	if forced == nil || len(forced.StagesApplied) == 0 {
		t.Fatalf("RunForce must apply stages even when NeedsCompaction=false; got %+v", forced)
	}
	// drop_enrichment is the first stage and should clear the context slot
	// unconditionally when enrichment content exists.
	if cw.Slot(SlotContext).Content != "" {
		t.Error("RunForce should have dropped context enrichment")
	}
}

func TestCompactionPipeline_dropsEnrichment(t *testing.T) {
	// Small window to force compaction.
	cw := NewContextWindow(2000, nil) // budget = 1600 tokens

	cw.SetContent(SlotSystem, strings.Repeat("x", 400))  // 100 tokens
	cw.SetContent(SlotContext, strings.Repeat("x", 2000)) // 500 tokens of enrichment

	msgs := makeMessages(6, 200) // 6 messages × 200 tokens = 1200 tokens
	cw.SetContent(SlotConversation, serializeMessages(msgs, DefaultEstimator{}))

	p := &CompactionPipeline{
		Window:               cw,
		Estimator:            DefaultEstimator{},
		Mode:                 CompactionModeGeneral,
		ConversationMessages: msgs,
	}

	result, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected compaction result")
	}

	// Context slot should be cleared.
	if cw.Slot(SlotContext).Content != "" {
		t.Error("context enrichment should have been dropped")
	}
}

func TestCompactionPipeline_summarizesOldest(t *testing.T) {
	// Tiny window.
	cw := NewContextWindow(500, nil) // budget = 400 tokens

	cw.SetContent(SlotSystem, strings.Repeat("x", 40)) // 10 tokens

	msgs := makeMessages(10, 50) // 10 messages × 50 tokens = 500 tokens
	cw.SetContent(SlotConversation, serializeMessages(msgs, DefaultEstimator{}))

	summarizer := &mockSummarizer{}
	p := &CompactionPipeline{
		Window:               cw,
		Estimator:            DefaultEstimator{},
		Summarizer:           summarizer,
		Mode:                 CompactionModeCode,
		ConversationMessages: msgs,
	}

	result, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected compaction result")
	}
	if !summarizer.called {
		t.Error("summarizer should have been called")
	}
	// Should have fewer messages now (6 oldest summarized, 4 kept).
	if len(p.ConversationMessages) != 5 { // 1 summary + 4 kept
		t.Errorf("expected 5 messages after compaction, got %d", len(p.ConversationMessages))
	}
	if !strings.Contains(p.ConversationMessages[0].Content, "summary") {
		t.Error("first message should be a summary")
	}
}

func TestStageStripToolBlocks(t *testing.T) {
	// Test the stage function directly — avoids earlier stages resolving budget.
	cw := NewContextWindow(100_000, nil)

	emptyInput := map[string]any{}
	msgs := []provider.ChatMessage{
		{Role: "user", Content: "do something"},
		{
			Role: "assistant",
			ContentBlocks: []provider.ContentBlock{
				{Type: "text", Text: "Let me help."},
				{Type: "tool_use", ID: "t1", Name: "read", Input: &emptyInput},
			},
		},
		{
			Role: "user",
			ContentBlocks: []provider.ContentBlock{
				{Type: "tool_result", ToolUseID: "t1", Content: strings.Repeat("x", 400)},
			},
		},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "thanks"},
	}

	p := &CompactionPipeline{
		Window:               cw,
		Estimator:            DefaultEstimator{},
		Mode:                 CompactionModeGeneral,
		ConversationMessages: msgs,
	}

	progress, err := stageStripToolBlocks(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !progress {
		t.Error("expected progress from stripping tool blocks")
	}

	// Check tool blocks were stripped.
	strippedAny := false
	for _, m := range p.ConversationMessages {
		for _, b := range m.ContentBlocks {
			if strings.Contains(b.Text, "stripped during compaction") {
				strippedAny = true
			}
		}
	}
	if !strippedAny {
		t.Error("expected tool blocks to be stripped")
	}

	// Original tool_use and tool_result blocks should be gone.
	for _, m := range p.ConversationMessages {
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_use" || b.Type == "tool_result" {
				t.Errorf("found unstripped %s block", b.Type)
			}
		}
	}
}

func TestSummarySystemPrompt_modes(t *testing.T) {
	tests := []struct {
		mode     string
		contains string
	}{
		{CompactionModeCode, "file paths"},
		{CompactionModePlan, "decisions"},
		{CompactionModeResearch, "findings"},
		{CompactionModeGeneral, "concise summary"},
	}
	for _, tt := range tests {
		prompt := summarySystemPrompt(tt.mode)
		if !strings.Contains(prompt, tt.contains) {
			t.Errorf("mode %s prompt should contain %q", tt.mode, tt.contains)
		}
	}
}
