package context

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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

// namedStage bundles a Stage function with a stable identifier for logging
// and the stages_applied response on /compact.
type namedStage struct {
	Name string
	Fn   Stage
}

// DefaultStages returns the escalation stages in order:
// 1. Drop Context slot enrichment
// 2. Dedupe identical tool_call+tool_result pairs (CW-20260419-0004 Part 2)
// 3. Summarize oldest conversation messages
// 4. Strip tool blocks from non-tool-use spans
//
// Dedupe runs BEFORE summarize so the summarizer doesn't burn tokens
// summarizing duplicate tool chatter, and BEFORE strip so strip's fallback
// "N blocks removed" note doesn't hide the fact that some of those blocks
// were exact duplicates.
func DefaultStages() []namedStage {
	return []namedStage{
		{"drop_enrichment", stageDropEnrichment},
		{"dedupe_tool_results", stageDedupeToolResults},
		{"summarize_oldest", stageSummarizeOldest},
		{"strip_tool_blocks", stageStripToolBlocks},
	}
}

// Run executes the compaction pipeline. Returns nil result if no compaction
// was needed.
func (p *CompactionPipeline) Run(ctx context.Context) (*CompactionResult, error) {
	if !p.Window.NeedsCompaction() {
		return nil, nil
	}
	return p.runStages(ctx, true)
}

// RunForce executes every compaction stage unconditionally and returns the
// result. Used by the manual /compact endpoint where the user asked for a
// compaction even though the budget hasn't been exceeded yet.
func (p *CompactionPipeline) RunForce(ctx context.Context) (*CompactionResult, error) {
	return p.runStages(ctx, false)
}

func (p *CompactionPipeline) runStages(ctx context.Context, recheckBetweenStages bool) (*CompactionResult, error) {
	result := &CompactionResult{Mode: p.Mode}

	for _, ns := range DefaultStages() {
		p.refreshConversationSlot()
		if recheckBetweenStages && !p.Window.NeedsCompaction() {
			break
		}

		progress, err := ns.Fn(ctx, p)
		if err != nil {
			return result, fmt.Errorf("compaction stage %s failed: %w", ns.Name, err)
		}
		if progress {
			result.StagesApplied = append(result.StagesApplied, ns.Name)
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
	if slot == nil || slot.Content == "" || !slot.Compactable {
		return false, nil
	}
	savedTokens := slot.TokenCount
	p.Window.SetContent(SlotContext, "")
	p.Window.SetFlags(SlotContext, SlotFlags{EnrichmentActive: false})
	slog.Info("compaction: dropped context enrichment", "saved_tokens", savedTokens)
	return true, nil
}

// --- Stage 2: Summarize oldest messages ---

func stageSummarizeOldest(ctx context.Context, p *CompactionPipeline) (bool, error) {
	if p.Summarizer == nil {
		slog.Warn("compaction: no summarizer configured, skipping summary stage")
		return false, nil
	}
	if conv := p.Window.Slot(SlotConversation); conv != nil && !conv.Compactable {
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

	slog.Info("compaction: summarized messages",
		"count", len(compactSpan), "old_tokens", oldTokens, "new_tokens", newTokens,
		"saved", oldTokens-newTokens)

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
		slog.Info("compaction: stripped tool blocks from conversation", "count", stripped)
	}
	return stripped > 0, nil
}

// --- Stage: dedupe identical tool_call+tool_result pairs ---
//
// Walks the conversation and collapses exact-duplicate tool invocations
// (same tool_name + same canonical inputs) to a short reference pointing
// at the first occurrence. Deterministic, no LLM involvement.
//
// Why this matters: tool-heavy turns frequently repeat the same call —
// the agent re-reads the same file, re-lists the same task set, re-queries
// the same sprint. Each repeat pays the full tool-result size in the
// conversation slot. Dedupe drops every copy after the first.
//
// Design: we identify invocations by (tool_name, canonical(input_json)).
// The first occurrence's (tool_use_id) is used as the reference target.
// Subsequent tool_use blocks keep their ID but their paired tool_result
// block's Content is replaced with a short pointer string. The tool_use
// block itself is left intact so the conversation's role/block shape
// stays valid for the provider.
//
// CW-20260419-0004 Part 2.
func stageDedupeToolResults(ctx context.Context, p *CompactionPipeline) (bool, error) {
	// First pass: collect tool_use blocks in order, hash their signatures.
	type sig struct {
		name  string
		input string // canonicalized
	}
	firstSeen := map[sig]string{} // signature -> tool_use_id of first occurrence
	// Map tool_use_id -> signature for duplicates we encounter later.
	duplicateOf := map[string]string{}

	for mi := range p.ConversationMessages {
		m := &p.ConversationMessages[mi]
		for _, b := range m.ContentBlocks {
			if b.Type != "tool_use" {
				continue
			}
			s := sig{name: b.Name, input: canonicalizeToolInput(b.Input)}
			if firstID, seen := firstSeen[s]; seen {
				duplicateOf[b.ID] = firstID
			} else {
				firstSeen[s] = b.ID
			}
		}
	}

	if len(duplicateOf) == 0 {
		return false, nil
	}

	// Second pass: replace the Content of any tool_result whose ToolUseID
	// is a duplicate with a pointer-back-to-original. Idempotent — blocks
	// that already carry a `[DUPLICATE ...]` pointer are skipped so
	// re-running the stage on an already-compacted conversation is a no-op.
	const dupMarker = "[DUPLICATE — identical tool call"
	replaced := 0
	for mi := range p.ConversationMessages {
		m := &p.ConversationMessages[mi]
		for bi := range m.ContentBlocks {
			b := &m.ContentBlocks[bi]
			if b.Type != "tool_result" {
				continue
			}
			if strings.HasPrefix(b.Content, dupMarker) {
				continue // already pointered by a prior pass
			}
			firstID, ok := duplicateOf[b.ToolUseID]
			if !ok {
				continue
			}
			b.Content = fmt.Sprintf("%s earlier in conversation, tool_use_id=%s. Reuse that result.]", dupMarker, firstID)
			replaced++
		}
	}

	if replaced > 0 {
		slog.Info("compaction: deduped identical tool_result blocks",
			"replaced", replaced, "unique_signatures", len(firstSeen))
	}
	return replaced > 0, nil
}

// canonicalizeToolInput produces a stable string representation of a
// tool_use block's Input field for dedupe equality. Maps with reordered
// keys still compare equal after canonicalization. Nil/missing input is
// represented as the empty string.
func canonicalizeToolInput(input *map[string]any) string {
	if input == nil || len(*input) == 0 {
		return ""
	}
	keys := make([]string, 0, len(*input))
	for k := range *input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		fmt.Fprintf(&b, "%v", (*input)[k])
		b.WriteByte('\x1f') // unit separator — cannot appear in normal values
	}
	return b.String()
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

