package service

// inspector_producers.go — I1 (CW-20260426-0004)
//
// Helper methods that translate chat-service internal types into inspector
// package types and call Service.Record*. These are intentionally thin
// adapters — the inspector package knows nothing about chat internals; all
// the conversion logic lives here.

import (
	llmtypes "github.com/hollis-labs/go-llm-types"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
)

// recordInspectorSlots builds a []inspector.SlotSnapshot from the
// SlotAssemblyResult and records it for the turn. Always writes all 8
// canonical slots so the UI sees a consistent array even when slots are empty.
func (s *chatServiceImpl) recordInspectorSlots(sessionID, turnID string, result *SlotAssemblyResult) {
	if s.inspector == nil || result == nil || result.Window == nil {
		return
	}

	// Sensitive slots: system, agent, rules carry policy/identity.
	sensitiveSlots := map[string]bool{
		ctxpkg.SlotSystem: true,
		ctxpkg.SlotAgent:  true,
		ctxpkg.SlotRules:  true,
	}

	snaps := make([]inspectsvc.SlotSnapshot, 0, len(ctxpkg.SlotOrder))
	for _, name := range ctxpkg.SlotOrder {
		slot := result.Window.Slot(name)
		var tokens int
		var cacheKey, content string
		if slot != nil {
			tokens = slot.TokenCount
			cacheKey = slot.CacheKey
			content = slot.Content
		}
		cached := result.Window.CacheHits[name]
		snaps = append(snaps, inspectsvc.SlotSnapshot{
			Name:         name,
			Tokens:       tokens,
			Cached:       cached,
			CacheKey:     cacheKey,
			Sensitive:    sensitiveSlots[name],
			Content:      content,
			TrafficLight: inspectsvc.TrafficLight(tokens, cached),
		})
	}
	s.inspector.RecordSlots(sessionID, turnID, snaps)
}

// recordInspectorLLMMessages builds a []inspector.LLMMessageRecord from the
// conversation messages that will be sent to the LLM, plus a synthetic system
// entry for the combined system prompt.
func (s *chatServiceImpl) recordInspectorLLMMessages(
	sessionID, turnID string,
	msgs []llmtypes.ChatMessage,
	systemPrompt string,
) {
	if s.inspector == nil {
		return
	}

	records := make([]inspectsvc.LLMMessageRecord, 0, len(msgs)+1)

	// Prepend a synthetic "system" record so the inspector shows slot content.
	if systemPrompt != "" {
		records = append(records, inspectsvc.LLMMessageRecord{
			Role:           "system",
			Content:        systemPrompt,
			Tokens:         estimateTokenCount(systemPrompt),
			Classification: "system_prompt",
		})
	}

	for _, m := range msgs {
		classification := classifyMessageRole(m.Role)
		records = append(records, inspectsvc.LLMMessageRecord{
			Role:           m.Role,
			Content:        m.Content,
			Tokens:         estimateTokenCount(m.Content),
			Classification: classification,
		})
	}

	s.inspector.RecordLLMMessages(sessionID, turnID, records)
}

// classifyMessageRole maps a provider role string to an internal classification.
func classifyMessageRole(role string) string {
	switch role {
	case "user":
		return "user_turn"
	case "assistant":
		return "assistant_turn"
	case "tool":
		return "tool_result"
	case "system":
		return "system_prompt"
	default:
		return role
	}
}

// estimateTokenCount returns a rough token estimate for a string.
// Uses the same heuristic as chat.EstimateTokens (chars/4).
func estimateTokenCount(s string) int {
	return (len(s) + 3) / 4
}
