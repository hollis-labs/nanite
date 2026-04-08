package context

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/go-providers/provider"
)

// CompactionMode determines how the summary prompt is shaped.
// The active mode comes from the broker's Layer 3 classifier.
const (
	CompactionModeGeneral  = "general"
	CompactionModeCode     = "code"
	CompactionModePlan     = "plan"
	CompactionModeResearch = "research"
)

// CompactionResult captures what happened during compaction for event emission
// and debugging.
type CompactionResult struct {
	Summary         string // LLM-generated summary that replaced the compacted span
	RemovedMessages int    // count of messages replaced
	TokensSaved     int    // tokens reclaimed
	Mode            string // compaction mode used
	StagesApplied   []string
	RawMessages     []provider.ChatMessage // original messages (for plugin extraction)
}

// Summarizer makes a simple non-streaming LLM call. Implemented by the
// provider layer — the compaction system doesn't know which model runs it.
type Summarizer interface {
	Summarize(ctx context.Context, systemPrompt string, messages []provider.ChatMessage) (string, error)
}

// CompactionPipeline runs escalating stages to bring the conversation slot
// within budget. Each stage is a self-contained action that returns true if
// it made progress (reduced tokens). Stages run in order until the budget is
// satisfied or all stages are exhausted.
type CompactionPipeline struct {
	Window    *ContextWindow
	Estimator TokenEstimator
	Summarizer Summarizer
	Mode       string // compaction mode (code, plan, research, general)

	// ConversationMessages is the current message list. Stages may modify it.
	ConversationMessages []provider.ChatMessage
}

// Stage is a single compaction action. Returns true if it made progress.
type Stage func(ctx context.Context, p *CompactionPipeline) (bool, error)

// DefaultStages returns the escalation stages in order:
// 1. Drop Context slot enrichment
// 2. Summarize oldest conversation messages
// 3. Strip tool blocks from non-tool-use spans
// 4. Reduce Tools slot (signal to broker — not directly modifying tools here)
func DefaultStages() []Stage {
	return []Stage{
		stageDropEnrichment,
		stageSummarizeOldest,
		stageStripToolBlocks,
	}
}

// Run executes the compaction pipeline. Returns nil result if no compaction
// was needed.
func (p *CompactionPipeline) Run(ctx context.Context) (*CompactionResult, error) {
	if !p.Window.NeedsCompaction() {
		return nil, nil
	}

	result := &CompactionResult{
		Mode: p.Mode,
	}

	stages := DefaultStages()
	for _, stage := range stages {
		// Recheck — an earlier stage may have freed enough.
		p.refreshConversationSlot()
		if !p.Window.NeedsCompaction() {
			break
		}

		progress, err := stage(ctx, p)
		if err != nil {
			return result, fmt.Errorf("compaction stage failed: %w", err)
		}
		if progress {
			result.StagesApplied = append(result.StagesApplied, stageName(stage))
		}
	}

	p.refreshConversationSlot()
	return result, nil
}

// refreshConversationSlot recomputes the conversation slot content and tokens
// from the current message list.
func (p *CompactionPipeline) refreshConversationSlot() {
	content := serializeMessages(p.ConversationMessages, p.Estimator)
	p.Window.SetContent(SlotConversation, content)
}

// --- Stage 1: Drop Context slot enrichment ---

func stageDropEnrichment(ctx context.Context, p *CompactionPipeline) (bool, error) {
	slot := p.Window.Slot(SlotContext)
	if slot == nil || slot.Content == "" {
		return false, nil
	}
	savedTokens := slot.TokenCount
	p.Window.SetContent(SlotContext, "")
	p.Window.SetFlags(SlotContext, SlotFlags{EnrichmentActive: false})
	log.Printf("compaction: dropped context enrichment, saved ~%d tokens", savedTokens)
	return true, nil
}

// --- Stage 2: Summarize oldest messages ---

func stageSummarizeOldest(ctx context.Context, p *CompactionPipeline) (bool, error) {
	if p.Summarizer == nil {
		log.Printf("compaction: no summarizer configured, skipping summary stage")
		return false, nil
	}
	msgs := p.ConversationMessages
	if len(msgs) <= 2 {
		return false, nil // nothing worth summarizing
	}

	// Identify the compaction span: keep the last 4 messages (2 turns),
	// summarize everything before that.
	keepCount := 4
	if keepCount > len(msgs) {
		keepCount = len(msgs)
	}
	spanEnd := len(msgs) - keepCount
	if spanEnd <= 0 {
		return false, nil
	}

	compactSpan := msgs[:spanEnd]
	keepSpan := msgs[spanEnd:]

	// Build mode-aware summarization prompt.
	sysPrompt := summarySystemPrompt(p.Mode)

	summary, err := p.Summarizer.Summarize(ctx, sysPrompt, compactSpan)
	if err != nil {
		return false, fmt.Errorf("summarize oldest: %w", err)
	}

	// Compute tokens saved.
	oldTokens := 0
	for _, m := range compactSpan {
		oldTokens += p.Estimator.Estimate(m.Content)
		for _, b := range m.ContentBlocks {
			oldTokens += p.Estimator.Estimate(b.Text) + p.Estimator.Estimate(b.Content)
		}
	}
	newTokens := p.Estimator.Estimate(summary)

	// Replace span with summary message + kept messages.
	summaryMsg := provider.ChatMessage{
		Role:    "user",
		Content: fmt.Sprintf("[Conversation summary — %d messages compacted]\n\n%s", len(compactSpan), summary),
	}

	p.ConversationMessages = append([]provider.ChatMessage{summaryMsg}, keepSpan...)

	log.Printf("compaction: summarized %d messages (%d→%d tokens, saved ~%d)",
		len(compactSpan), oldTokens, newTokens, oldTokens-newTokens)

	return true, nil
}

// --- Stage 3: Strip tool blocks from non-tool-use spans ---

func stageStripToolBlocks(ctx context.Context, p *CompactionPipeline) (bool, error) {
	stripped := 0
	for i := range p.ConversationMessages {
		m := &p.ConversationMessages[i]
		if len(m.ContentBlocks) == 0 {
			continue
		}

		// Check if this message has any tool_use blocks.
		hasToolUse := false
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_use" || b.Type == "tool_result" {
				hasToolUse = true
				break
			}
		}
		if !hasToolUse {
			continue
		}

		// Strip tool blocks, keep text blocks. Also add a compact reference.
		var kept []provider.ContentBlock
		var removedCount int
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_use" || b.Type == "tool_result" {
				removedCount++
				continue
			}
			kept = append(kept, b)
		}

		if removedCount > 0 {
			// Add a note about removed tool blocks.
			kept = append(kept, provider.ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("[%d tool call/result blocks stripped during compaction]", removedCount),
			})
			m.ContentBlocks = kept
			stripped += removedCount
		}
	}

	if stripped > 0 {
		log.Printf("compaction: stripped %d tool blocks from conversation", stripped)
	}
	return stripped > 0, nil
}

// summarySystemPrompt returns a mode-aware prompt for the summarization model.
func summarySystemPrompt(mode string) string {
	base := "You are a conversation summarizer. Produce a concise summary that preserves key information. Be factual and specific."

	switch mode {
	case CompactionModeCode:
		return base + "\n\nFocus on: code changes made, files modified, errors encountered, solutions applied. Preserve exact file paths and function names."
	case CompactionModePlan:
		return base + "\n\nFocus on: decisions made, alternatives considered, rationale for choices. Preserve specific commitments and next steps."
	case CompactionModeResearch:
		return base + "\n\nFocus on: findings discovered, sources consulted, conclusions drawn. Preserve specific data points and references."
	default:
		return base
	}
}

// serializeMessages converts a message list to a string for token estimation.
// This is used internally by the compaction pipeline to update the conversation
// slot content.
func serializeMessages(msgs []provider.ChatMessage, est TokenEstimator) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		for _, block := range m.ContentBlocks {
			b.WriteString(block.Text)
			b.WriteString(block.Content)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// stageName returns a human-readable name for a stage function (for logging).
func stageName(s Stage) string {
	// We can't get the function name at runtime without reflect tricks.
	// Use the stage index from DefaultStages instead — the caller knows the order.
	return "stage"
}
