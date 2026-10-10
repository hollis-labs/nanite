package contextbroker

import (
	"context"
	"reflect"
	"strings"
	"testing"

	ctxpkg "github.com/hollis-labs/substrate/agent/contextwindow"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// These behavioral expectations were exercised against the former context
// package before switching the host to the published contextwindow mechanism.
func TestPublishedContextAssemblyCharacterization(t *testing.T) {
	w := ctxpkg.NewContextWindowWithBudgetPct(1000, 0.5, nil)
	if w.TotalBudget != 500 || w.Slot(ctxpkg.SlotMode).Content != "" {
		t.Fatal("configured budget or inert mode changed")
	}
	w.Slot(ctxpkg.SlotSystem).MaxTokens = 2
	w.Slot(ctxpkg.SlotAgent).MaxTokens = 1
	w.SetContent(ctxpkg.SlotUniversal, "rules")
	w.SetContent(ctxpkg.SlotSystem, "αβγδεζ")
	w.SetContent(ctxpkg.SlotMemory, strings.Repeat("m", 40))
	w.SetFlags(ctxpkg.SlotMemory, ctxpkg.SlotFlags{LazyLoad: true, LoadHint: "load retained memory"})
	w.SetContent(ctxpkg.SlotAgent, "whole agent instructions")
	w.SetContent(ctxpkg.SlotUserContext, "user pin")
	w.SetContent(ctxpkg.SlotHandoff, "handoff pin")
	w.SetFlags(ctxpkg.SlotHandoff, ctxpkg.SlotFlags{AutoInject: true})
	w.SetContent(ctxpkg.SlotConversation, "turn")
	wantNames := []string{"universal", "system", "memory", "agent", "user_context", "handoff", "conversation"}
	wantContent := []string{"rules", "αβγδ\n[truncated]", "load retained memory", "whole agent instructions", "user pin", "handoff pin", "turn"}
	blocks := w.Assemble()
	if len(blocks) != len(wantNames) {
		t.Fatal("provider block shape changed", blocks)
	}
	for i, block := range blocks {
		if block.SlotName != wantNames[i] || block.Content != wantContent[i] || !block.Changed || block.CacheKey != ctxpkg.ComputeCacheKey(wantContent[i]) {
			t.Fatalf("unexpected first-turn block %d: %+v", i, block)
		}
	}
	for _, block := range w.Assemble() {
		if block.Changed || !w.CacheHits[block.SlotName] {
			t.Fatal("stable rendered prefix lost its cache key", block)
		}
	}
	w.SetFlags(ctxpkg.SlotMemory, ctxpkg.SlotFlags{})
	for _, block := range w.Assemble() {
		if block.Changed != (block.SlotName == ctxpkg.SlotMemory) {
			t.Fatal("pointer-to-content transition changed another slot", block)
		}
	}
	if w.Slot(ctxpkg.SlotUserContext).Compactable || w.Slot(ctxpkg.SlotHandoff).Compactable || !w.Slot(ctxpkg.SlotHandoff).Flags.AutoInject {
		t.Fatal("pinned continuity became compactable or lost host injection")
	}
	for _, pct := range []float64{0, -1, 2} {
		if got := ctxpkg.NewContextWindowWithBudgetPct(1000, pct, nil).TotalBudget; got != 800 {
			t.Fatal("invalid fraction changed the established fallback", pct, got)
		}
	}
	w.SetContent(ctxpkg.SlotConversation, strings.Repeat("c", 4000))
	if !w.NeedsCompaction() {
		t.Fatal("conversation overflow no longer requests compaction")
	}
}

type characterizationSummary struct{ calls int }

func (s *characterizationSummary) Summarize(context.Context, string, []llmtypes.ChatMessage) (string, error) {
	s.calls++
	return "unexpected summary", nil
}

type characterizationCompactionWriter struct{ event ctxpkg.CompactionEvent }

func (w *characterizationCompactionWriter) WriteCompactionEvent(_ context.Context, event ctxpkg.CompactionEvent) error {
	w.event = event
	return nil
}

func TestPublishedContextGlass4Characterization(t *testing.T) {
	w := ctxpkg.NewContextWindow(200000, nil)
	w.SetContent(ctxpkg.SlotContext, "discardable enrichment")
	w.SetContent(ctxpkg.SlotAgent, "immutable instructions")
	w.SetContent(ctxpkg.SlotUserContext, "explicit user pin")
	w.SetContent(ctxpkg.SlotHandoff, "pinned continuity")
	w.SetFlags(ctxpkg.SlotHandoff, ctxpkg.SlotFlags{AutoInject: true})
	var messages []llmtypes.ChatMessage
	for i := 0; i < 6; i++ {
		messages = append(messages, llmtypes.ChatMessage{Role: "user", Content: strings.Repeat("older prose ", 100)})
	}
	before := append([]llmtypes.ChatMessage(nil), messages...)
	summary := &characterizationSummary{}
	writer := &characterizationCompactionWriter{}
	p := ctxpkg.CompactionPipeline{Window: w, Estimator: ctxpkg.DefaultEstimator{}, Summarizer: summary, ConversationMessages: messages, Mode: ctxpkg.CompactionModeGeneral, SessionID: "private-characterization", CompactionEventWriter: writer}
	result, err := p.RunForce(t.Context())
	if err != nil || result == nil || !reflect.DeepEqual(result.StagesApplied, []string{"drop_enrichment"}) || summary.calls != 0 || !reflect.DeepEqual(p.ConversationMessages, before) {
		t.Fatal("Glass4 handoff did not suppress redundant summarization", result, err, summary.calls)
	}
	if w.Slot(ctxpkg.SlotContext).Content != "" || w.Slot(ctxpkg.SlotAgent).Content != "immutable instructions" || w.Slot(ctxpkg.SlotUserContext).Content != "explicit user pin" || w.Slot(ctxpkg.SlotHandoff).Content != "pinned continuity" {
		t.Fatal("compaction changed pinned context or retained enrichment")
	}
	if writer.event.ID == "" || writer.event.SessionID != p.SessionID || writer.event.SummaryMode != "general" || !reflect.DeepEqual(writer.event.StagesApplied, result.StagesApplied) {
		t.Fatal("compaction event lost opaque identity or stage provenance", writer.event)
	}
}
