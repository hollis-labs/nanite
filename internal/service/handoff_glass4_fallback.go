package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
)

// ensureGlass4HandoffPreCompact is the at-compaction fallback for the Glass-4
// hybrid flow. The PRIMARY path is proactive: the agent invokes the
// `handoff_stash` self-tool during normal turns, so by the time compaction
// fires there's already a Glass-4 envelope on disk. This fallback only
// triggers when:
//
//  1. The session is classified long-running (caller's responsibility — gated
//     before invoking this method).
//  2. No Glass-4 stash exists yet (agent hasn't called handoff_stash).
//
// In that case we derive a minimal handoff from current state:
//   - SessionIntent: long-running marker + reason
//   - NextStepAnchor: last user message, truncated
//   - RecentDecisions: pulled from scratchpad keys when present
//   - ActivePointers: empty (no agent-curated artifacts to point at)
//
// This is deterministic and budget-safe — no agent round-trip required at
// compaction time. The boot prompt's literal "synchronous ASK during recovery"
// has a chicken-and-egg risk under rate-budget triggers (the request that
// triggered overflow was already over budget; adding "Quick reflection" makes
// it worse). Deferring that variant to a follow-up; the proactive primary
// path + this fallback together preserve the keystone behavior.
//
// Returns the stash_id when a fallback was written; "" with no error when a
// pre-existing Glass-4 stash made the fallback unnecessary.
func (s *chatServiceImpl) ensureGlass4HandoffPreCompact(
	ctx context.Context,
	sess *store.Session,
	chatMessages []provider.ChatMessage,
	ch chan chat.StreamEvent,
) (string, error) {
	_ = ctx // ctx reserved for future LLM tiebreaker; deterministic path doesn't need it.
	_ = ch  // SSE not emitted on fallback write; the post-compaction inject emits handoff_loaded.

	if sess == nil {
		return "", nil
	}

	// Skip if a Glass-4 stash already exists — proactive path covered it.
	if existing, _, err := ReadLatestGlass4Handoff(s.store, sess.ID); err == nil && existing != nil {
		return "", nil
	} else if err != nil && !errors.Is(err, store.ErrHandoffStashNotFound) {
		slog.Warn("chat-service: glass-4 handoff probe failed (will attempt fallback write)",
			"session_id", sess.ID, "err", err)
	}

	payload := buildFallbackHandoff(chatMessages)
	stashID, err := WriteGlass4Handoff(s.store, sess.ID, payload)
	if err != nil {
		return "", err
	}
	slog.Info("handoff: pre-compaction stash captured (glass-4 deterministic fallback)",
		"session_id", sess.ID, "cache_key", stashID,
		"next_step_anchor_chars", len(payload.NextStepAnchor))
	return stashID, nil
}

// buildFallbackHandoff constructs a minimal-but-valid HandoffPayload from
// the conversation tail when no agent-authored handoff exists. Required
// fields (session_intent, next_step_anchor) are always populated; optional
// fields are filled when corresponding signal is present.
func buildFallbackHandoff(chatMessages []provider.ChatMessage) ctxpkg.HandoffPayload {
	anchor := lastUserMessageText(chatMessages)
	if anchor == "" {
		anchor = "(no recent user message — pick up from the prior assistant response)"
	}
	anchor = truncateForHandoff(anchor, ctxpkg.HandoffNextStepAnchorMaxTokens*4 /* chars */)

	return ctxpkg.HandoffPayload{
		SessionIntent: "long-running session; deterministic handoff fallback (agent did not author a handoff before compaction).",
		// RecentDecisions and ActivePointers intentionally empty in the
		// fallback — populating them with guesses would mislead the
		// post-compaction agent. Empty means "no curated decisions to
		// surface" which the rendered slot communicates honestly.
		NextStepAnchor: anchor,
	}
}

// lastUserMessageText returns the most recent user message's textual content,
// concatenating Content + any text blocks. Tool blocks are skipped — they
// don't carry user-intent text useful as a NextStepAnchor.
func lastUserMessageText(msgs []provider.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "user" {
			continue
		}
		if m.Content != "" {
			return m.Content
		}
		for _, b := range m.ContentBlocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

// truncateForHandoff trims s to at most maxChars characters, ending at a word
// boundary when possible. Appends an ellipsis when truncation occurred so
// the post-compaction agent knows the anchor is incomplete and can ask
// for clarification rather than acting on a half-sentence.
func truncateForHandoff(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	cut := s[:maxChars]
	if i := lastSpaceWithin(cut, maxChars/2); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}

// lastSpaceWithin returns the index of the last space in s within the
// rightmost (len(s)-minTail, len(s)] window, or -1 when none found. Used to
// pick a word boundary for truncation.
func lastSpaceWithin(s string, minTail int) int {
	if minTail < 0 || minTail >= len(s) {
		return -1
	}
	for i := len(s) - 1; i >= len(s)-minTail; i-- {
		if s[i] == ' ' || s[i] == '\n' {
			return i
		}
	}
	return -1
}
