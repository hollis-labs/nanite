package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/substrate/agent/context"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// ErrCompactionSessionNotFound distinguishes a missing session from failures
// resolving its agent or assembling its context.
var ErrCompactionSessionNotFound = errors.New("session not found")

// ManualCompactionResult describes the stages and savings of a forced compaction.
type ManualCompactionResult struct {
	Summary       string
	StagesApplied []string
	TokensSaved   int
	Mode          string
}

// CompactSession assembles the session context, forces compaction, records its
// metadata, and publishes the resulting lifecycle and stream events.
func (c *Container) CompactSession(ctx context.Context, sessionID string) (*ManualCompactionResult, error) {
	if locker, ok := c.Chat.(ConversationLocker); ok {
		release, lockErr := locker.LockConversation(ctx, sessionID)
		if lockErr != nil {
			return nil, lockErr
		}
		defer release()
	}
	session, err := c.Sessions.Get(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCompactionSessionNotFound
		}
		return nil, err
	}

	agent, err := c.Agents.ResolveForSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	settings, _ := c.Settings.Get(ctx)
	windowSize := 0
	if settings != nil {
		windowSize = settings.ContextWindowTokens
	}

	result, err := c.Context.AssembleSlots(ctx, session, agent, []llmtypes.ToolDefinition{}, "", windowSize, "")
	if err != nil {
		return nil, err
	}

	// A context service may finish assembly as cancellation arrives. Refuse
	// before starting any new handoff or compaction effects in that case.
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if c.CompactionHandoffs != nil {
		if _, handoffErr := ensureGlass4Handoff(c.CompactionHandoffs, session, result.Messages); handoffErr != nil {
			slog.Warn("service: glass-4 pre-compaction handoff fallback failed (non-fatal)",
				"session_id", sessionID, "err", handoffErr)
		}
	}

	summarizer := BuildSummarizer(c.Providers, c.ProviderDefaults, settings)
	mode := ClassifyCompactionMode(agent)
	pipeline := &ctxpkg.CompactionPipeline{
		Window:                result.Window,
		Estimator:             ctxpkg.DefaultEstimator{},
		Summarizer:            summarizer,
		Mode:                  mode,
		ConversationMessages:  result.Messages,
		SessionID:             sessionID,
		CompactionEventWriter: c.CompactionEvents,
	}

	tokensBefore := result.Window.UsedTokens()
	if c.Events != nil {
		c.Events.EmitPreCompact(ctx, sessionID, len(result.Messages), "manual")
	}
	cr, err := pipeline.RunForce(ctx)
	if err != nil {
		slog.Warn("service: compaction pipeline failed", "session_id", sessionID, "err", err)
		return nil, fmt.Errorf("compaction failed: %w", err)
	}
	tokensAfter := result.Window.UsedTokens()
	tokensSaved := tokensBefore - tokensAfter
	if tokensSaved < 0 {
		tokensSaved = 0
	}

	summary := ""
	stages := []string{}
	if cr != nil {
		summary = cr.Summary
		stages = cr.StagesApplied
	}
	if summary == "" {
		// Stage 2 (summarize) is the only stage that produces a summary today.
		// When summarizer is unavailable Stage 2 skips, so derive a placeholder
		// from the manifest of stages that did fire so the session card can
		// still render something useful.
		if len(stages) > 0 {
			summary = fmt.Sprintf("Compaction applied %d stage(s); no LLM summary produced (summarizer unavailable).", len(stages))
		}
	}

	// Match automatic compaction: inject only after a successful pipeline,
	// with savings measured before the continuity slot is added.
	handoffEvents := make(chan chat.StreamEvent, 1)
	if c.CompactionHandoffs != nil {
		if _, handoffErr := InjectGlass4HandoffSlot(c.CompactionHandoffs, result.Window, sessionID, handoffEvents); handoffErr != nil {
			slog.Warn("service: glass-4 handoff inject failed (non-fatal)",
				"session_id", sessionID, "err", handoffErr)
		}
	}
	result.Messages = pipeline.ConversationMessages
	result.Blocks = result.Window.Assemble()
	result.NeedsCompaction = result.Window.NeedsCompaction()
	result.SystemPrompt = rebuildLegacySystemPrompt(result.Window)

	if c.Events != nil {
		c.Events.EmitPostCompact(ctx, sessionID, tokensSaved, stages)
	}

	if c.Streams != nil {
		payload := chat.SlotChangedV1{
			Slot:         ctxpkg.SlotConversation,
			Change:       chat.SlotChangeSummarized,
			Reasoning:    fmt.Sprintf("/compact requested; %d stage(s) applied.", len(stages)),
			TokensBefore: tokensBefore,
			TokensAfter:  tokensAfter,
		}
		// Reuse the chat helper's marshaling+defaulting via a side channel:
		// we wrap the same emit logic by sending through a single-buffered
		// chan and forwarding to active streams.
		envCh := make(chan chat.StreamEvent, 1)
		if emitErr := chat.EmitSlotChangedEvent(envCh, payload); emitErr != nil {
			slog.Warn("service: slot_changed marshal failed", "session_id", sessionID, "err", emitErr)
		} else {
			evt := <-envCh
			c.Streams.BroadcastSessionStreamEvent(sessionID, evt)
		}
	}
	close(handoffEvents)
	if c.Streams != nil {
		for event := range handoffEvents {
			c.Streams.BroadcastSessionStreamEvent(sessionID, event)
		}
	}

	return &ManualCompactionResult{
		Summary: summary, StagesApplied: stages, TokensSaved: tokensSaved, Mode: mode,
	}, nil
}
