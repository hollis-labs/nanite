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
