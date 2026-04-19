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
