package context

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// stubSummarizer counts calls so tests can assert the summarizer was (or
// wasn't) invoked. Returns a fixed string when called.
type stubSummarizer struct {
	calls   int
	returns string
}

func (s *stubSummarizer) Summarize(_ context.Context, _ string, _ []llmtypes.ChatMessage) (string, error) {
	s.calls++
	if s.returns == "" {
		return "summarized text", nil
	}
	return s.returns, nil
}

// makeWindowWithBigConversation creates a ContextWindow whose SlotConversation
// is over budget so NeedsCompaction() returns true. Used by the deferral tests
// to verify short-circuit behavior.
func makeWindowWithBigConversation(t *testing.T) *ContextWindow {
	t.Helper()
	cw := NewContextWindow(8000, DefaultEstimator{})
	// Stuff the conversation slot with enough text that compaction is needed.
	bigText := strings.Repeat("turn content sentence here. ", 400)
	cw.SetContent(SlotConversation, bigText)
	return cw
}

func TestStageSummarizeOldest_DefersToHandoff(t *testing.T) {
	cw := makeWindowWithBigConversation(t)
	// Populate SlotHandoff so the deferral short-circuit fires.
	cw.SetContent(SlotHandoff, "[handoff present]")

	stub := &stubSummarizer{}

	// Build a long message list so the stage would normally proceed.
	msgs := make([]llmtypes.ChatMessage, 0, 12)
	for i := 0; i < 12; i++ {
		msgs = append(msgs, llmtypes.ChatMessage{
			Role:    "user",
			Content: strings.Repeat("filler ", 50),
		})
	}

	p := &CompactionPipeline{
		Window:               cw,
		Estimator:            DefaultEstimator{},
		Summarizer:           stub,
		ConversationMessages: msgs,
	}

	progress, err := stageSummarizeOldest(context.Background(), p)
	if err != nil {
		t.Fatalf("stage error: %v", err)
	}
	if progress {
		t.Errorf("stage should not report progress when handoff defers it")
	}
	if stub.calls != 0 {
		t.Errorf("summarizer should not be invoked when handoff is present; got %d calls", stub.calls)
	}
}

func TestStageSummarizeOldest_NegativeSavingsGuard(t *testing.T) {
	cw := makeWindowWithBigConversation(t)
	stub := &stubSummarizer{}

	// Tiny conversation: well below SummarizeMinTokens.
	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "ok"},
		{Role: "assistant", Content: "yep"},
		{Role: "user", Content: "thanks"},
		{Role: "assistant", Content: "you're welcome"},
	}

	p := &CompactionPipeline{
		Window:               cw,
		Estimator:            DefaultEstimator{},
		Summarizer:           stub,
		ConversationMessages: msgs,
	}

	progress, err := stageSummarizeOldest(context.Background(), p)
	if err != nil {
		t.Fatalf("stage error: %v", err)
	}
	if progress {
		t.Errorf("stage should not progress when span is below SummarizeMinTokens")
	}
	if stub.calls != 0 {
		t.Errorf("summarizer should not be invoked under negative-savings guard; got %d calls", stub.calls)
	}
}

func TestStageSummarizeOldest_RunsForLargeSpanWithoutHandoff(t *testing.T) {
	cw := makeWindowWithBigConversation(t)
	stub := &stubSummarizer{returns: "ok"}

	// Span large enough to clear SummarizeMinTokens.
	msgs := make([]llmtypes.ChatMessage, 0, 10)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, llmtypes.ChatMessage{
			Role:    "user",
			Content: strings.Repeat("substantive content with many words here to clear the threshold ", 4),
		})
	}

	p := &CompactionPipeline{
		Window:               cw,
		Estimator:            DefaultEstimator{},
		Summarizer:           stub,
		ConversationMessages: msgs,
	}

	progress, err := stageSummarizeOldest(context.Background(), p)
	if err != nil {
		t.Fatalf("stage error: %v", err)
	}
	if !progress {
		t.Errorf("stage should progress for a large span without handoff")
	}
	if stub.calls != 1 {
		t.Errorf("summarizer should be invoked exactly once; got %d", stub.calls)
	}
}
