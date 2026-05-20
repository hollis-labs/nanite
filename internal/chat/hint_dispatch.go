package chat

// hint_dispatch.go — Think-block v2 dynamic hint dispatch (F5 / CW-20260420-0022).
//
// Provides ThinkToolBlockDynamic which dispatches the hint-selector peer
// agent via the PeerQuery primitive (dispatch.Spawner) to choose relevant
// hints for the current turn's think-tool block.
//
// Fallback chain (env-flag combinations):
//
//	NANITE_THINK_BLOCK_V1=false                        → v0 (thinkToolBlock)
//	NANITE_THINK_BLOCK_V1=true (default), V2=false     → v1 static (thinkToolBlockV1)
//	NANITE_THINK_BLOCK_V1=true, V2=true                → v2 dynamic (this file)
//	  peer dispatch succeeds                             → rendered dynamic block
//	  peer dispatch fails / empty / over budget         → fallback to v1 static
//
// The v2 dispatch is intentionally synchronous and bounded by a short
// context deadline — it runs at system-prompt assembly time (before the
// LLM turn) so latency matters. The budget guard truncates to 200 tokens
// even when the peer returns more IDs than fit.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// HintSelectorSlug is the agent profile slug for the hint-selector peer.
// Backed by internal/agent/builtin/profiles/hint-selector.md (the file
// source-of-truth ingested at boot by AutoIngestAgents). The previous
// config/agents/hint-selector.yaml file was dead config and was
// removed in CW-20260519-0123.
const HintSelectorSlug = "hint-selector"

// HintSelectOpts carries the per-request context used by v2 dynamic hint
// selection. It is assembled by the caller (ContextClient) and passed into
// assembleSystemPromptFromTemplates. All fields are optional: nil Dispatcher
// or empty strings cause a graceful fallback to v1 static.
type HintSelectOpts struct {
	// Ctx is the request context (may carry a deadline).
	Ctx context.Context
	// Dispatcher is the peer dispatch interface. nil → fallback to v1.
	Dispatcher HintDispatcher
	// UserInput is the current user message text.
	UserInput string
	// ScopeTier is the classifier output ("trivial", "small", "medium", "large", "open").
	ScopeTier string
	// ReflexMatchID is the matched reflex ID, or "" if no reflex matched.
	ReflexMatchID string
}

// thinkBlockMaxTokens is the shared token budget for v1 and v2 blocks.
const thinkBlockMaxTokens = 200

// HintDispatcher is the narrow interface F5's dynamic dispatch needs.
// The production wiring injects a dispatch.Spawner-backed implementation;
// tests inject a stub.
//
// Dispatch sends a JSON payload to the hint-selector peer and returns the
// raw response text. The caller parses the JSON array of hint IDs from
// the response.
//
// Contract:
//   - payload is a JSON object: {user_input, scope_tier, reflex_match, hint_catalog}
//   - response is a JSON array of hint ID strings, e.g. ["scratchpad","memory_recall"]
//   - On failure (timeout, peer unavailable, etc.) return "", err.
type HintDispatcher interface {
	Dispatch(ctx context.Context, payload string) (string, error)
}

// IsThinkBlockV2Enabled returns true when NANITE_THINK_BLOCK_V2_ENABLED=true
// is set in the environment. Default is OFF (v1 is the active default).
//
// Both V1 and V2 flags must be true for dynamic selection to activate:
//
//	V1=false           → v0 baseline (no hint block)
//	V1=true, V2=false  → v1 static (default)
//	V1=true, V2=true   → v2 dynamic (this path)
func IsThinkBlockV2Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_THINK_BLOCK_V2_ENABLED")))
	return v == "true" || v == "1" || v == "yes"
}

// ThinkToolBlockDynamic dispatches the hint-selector peer via dispatcher to
// select relevant hints for the current turn. Returns the rendered dynamic
// think-block on success, or falls back to thinkToolBlockV1 on any failure.
//
// Parameters:
//   - ctx: request context (may carry a deadline imposed by the caller)
//   - dispatcher: peer dispatch interface (nil → immediate fallback to v1)
//   - userInput: the current user message (used by the peer for context)
//   - scopeTier: the ScopeTier string from the classifier ("small", "open", etc.)
//   - reflexMatchID: the matched reflex ID if any (empty string = no match)
//
// If the rendered block exceeds thinkBlockMaxTokens, it is truncated to
// the first hints that fit within budget (last hint removed until fits).
func ThinkToolBlockDynamic(ctx context.Context, dispatcher HintDispatcher, userInput, scopeTier, reflexMatchID string) string {
	if dispatcher == nil {
		slog.Debug("chat: hint dispatch: no dispatcher configured — falling back to v1")
		return thinkToolBlockV1
	}

	catalog := BuiltinHints()
	if len(catalog) == 0 {
		slog.Warn("chat: hint dispatch: empty catalog — falling back to v1", "catalog_err", BuiltinHintsErr())
		return thinkToolBlockV1
	}

	// Build the peer request payload.
	summaries := SummarizeHints(catalog)
	payload, err := buildDispatchPayload(userInput, scopeTier, reflexMatchID, summaries)
	if err != nil {
		slog.Warn("chat: hint dispatch: failed to build payload — falling back to v1", "err", err)
		return thinkToolBlockV1
	}

	// Dispatch to the hint-selector peer.
	raw, err := dispatcher.Dispatch(ctx, payload)
	if err != nil {
		slog.Warn("chat: hint dispatch: peer dispatch failed — falling back to v1", "err", err)
		return thinkToolBlockV1
	}

	// Parse the peer's response: a JSON array of hint IDs.
	hintIDs, err := parseHintIDs(raw)
	if err != nil || len(hintIDs) == 0 {
		slog.Warn("chat: hint dispatch: peer response parse failed or empty — falling back to v1",
			"raw", raw, "err", err)
		return thinkToolBlockV1
	}

	// Resolve hint IDs → Hint structs.
	selected := resolveHints(catalog, hintIDs)
	if len(selected) == 0 {
		slog.Warn("chat: hint dispatch: no catalog entries matched peer IDs — falling back to v1",
			"ids", hintIDs)
		return thinkToolBlockV1
	}

	// Render dynamic block.
	block := renderDynamicBlock(selected)

	// Token budget guard: truncate if over budget.
	block = guardTokenBudget(block, selected)

	return block
}

// buildDispatchPayload marshals the peer request to JSON.
func buildDispatchPayload(userInput, scopeTier, reflexMatchID string, catalog []HintCatalogSummary) (string, error) {
	req := struct {
		UserInput    string               `json:"user_input"`
		ScopeTier    string               `json:"scope_tier"`
		ReflexMatch  string               `json:"reflex_match"`
		HintCatalog  []HintCatalogSummary `json:"hint_catalog"`
	}{
		UserInput:   userInput,
		ScopeTier:   scopeTier,
		ReflexMatch: reflexMatchID,
		HintCatalog: catalog,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("hint dispatch payload: %w", err)
	}
	return string(b), nil
}

// parseHintIDs unmarshals a JSON array of strings from the peer response.
// Returns an error if raw is empty or not a valid JSON array of strings.
func parseHintIDs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("hint dispatch: empty peer response")
	}
	// Tolerate prose wrapping: find the first '[' and last ']'.
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start >= 0 && end > start {
		raw = raw[start : end+1]
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("hint dispatch: parse peer ids: %w", err)
	}
	return ids, nil
}

// resolveHints looks up each ID in the catalog and returns matched hints in
// the order the peer returned them (peer's ranking wins).
func resolveHints(catalog []Hint, ids []string) []Hint {
	out := make([]Hint, 0, len(ids))
	for _, id := range ids {
		if h, ok := HintByID(catalog, id); ok {
			out = append(out, h)
		}
	}
	return out
}

// renderDynamicBlock renders the selected hints into a think-tool block string.
func renderDynamicBlock(selected []Hint) string {
	var b strings.Builder
	b.WriteString("\n\n## Before Responding — Consider Your Affordances\n")
	b.WriteString("Use the think tool to plan before multi-step tool sequences or when new context changes your approach.\n")
	for _, h := range selected {
		b.WriteString("\n- ")
		b.WriteString(h.Body)
	}
	return b.String()
}

// guardTokenBudget checks the rendered block against thinkBlockMaxTokens.
// If over budget, it re-renders with one fewer hint at a time until it fits.
// Returns the (possibly truncated) block.
func guardTokenBudget(block string, selected []Hint) string {
	content := strings.TrimLeft(block, "\n")
	if EstimateTokens(content) <= thinkBlockMaxTokens {
		return block
	}
	// Drop hints from the end until under budget.
	for len(selected) > 1 {
		selected = selected[:len(selected)-1]
		block = renderDynamicBlock(selected)
		content = strings.TrimLeft(block, "\n")
		if EstimateTokens(content) <= thinkBlockMaxTokens {
			return block
		}
	}
	// Single hint still over budget — fall back to v1 (never happens with
	// current hint bodies but guarded for future larger hints).
	if EstimateTokens(strings.TrimLeft(block, "\n")) > thinkBlockMaxTokens {
		slog.Warn("chat: hint dispatch: single hint exceeds budget — falling back to v1")
		return thinkToolBlockV1
	}
	return block
}
