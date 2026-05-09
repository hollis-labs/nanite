package anthropic

import (
	"bytes"
	"context"
	"encoding/json"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// buildSystemBlocks renders the system portion of a request as []TextBlockParam.
// When the request carries SlotBlocks, each block becomes its own block; the
// last unchanged slot block carries cache_control if a "system" hint is set.
// When there are no slots, falls back to a single block off SystemPrompt.
//
// Mirrors the deleted adapter's buildSystemFromRequest behavior so the wire
// format identical between the old and new adapters.
func (c *Client) buildSystemBlocks(in llmtypes.ChatRequest) []sdk.TextBlockParam {
	if len(in.SlotBlocks) == 0 {
		if in.SystemPrompt == "" {
			return nil
		}
		block := sdk.TextBlockParam{Text: in.SystemPrompt}
		if c.hasCacheHint("system") {
			block.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		return []sdk.TextBlockParam{block}
	}
	out := make([]sdk.TextBlockParam, 0, len(in.SlotBlocks)+1)
	if in.SystemPrompt != "" {
		block := sdk.TextBlockParam{Text: in.SystemPrompt}
		if c.hasCacheHint("system") {
			block.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		out = append(out, block)
	}
	for _, s := range in.SlotBlocks {
		if s.Content == "" {
			continue
		}
		block := sdk.TextBlockParam{Text: s.Content}
		if !s.Changed {
			block.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		out = append(out, block)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildTools converts internal tool definitions into the SDK's
// []ToolUnionParam shape, applying cache_control on the last entry when a
// "tools" hint is set. Strict pass-through preserves the
// CW-20260420-0007 default-non-strict contract — the wrapper only sets
// Strict=true when ToolDefinition.Strict explicitly points to true.
func (c *Client) buildTools(tools []llmtypes.ToolDefinition) []sdk.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	shouldCache := c.hasCacheHint("tools")
	out := make([]sdk.ToolUnionParam, len(tools))
	for i, t := range tools {
		toolParam := sdk.ToolParam{
			Name: t.Name,
			InputSchema: sdk.ToolInputSchemaParam{
				Properties: extractSchemaProperties(t.InputSchema),
				Required:   extractSchemaRequired(t.InputSchema),
			},
		}
		if t.Description != "" {
			toolParam.Description = sdk.String(t.Description)
		}
		if t.Strict != nil && *t.Strict {
			toolParam.Strict = sdk.Bool(true)
		}
		if shouldCache && i == len(tools)-1 {
			toolParam.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		out[i] = sdk.ToolUnionParam{OfTool: &toolParam}
	}
	return out
}

// buildMessages translates internal ChatMessages into MessageParam[]. Trailing
// user messages get cache_control on their last non-thinking content block
// when "recent_message" hints are configured (count == number of "recent_message"
// hints). Ordering follows the same recipe the deleted adapter used:
// hint count 2 means the last two user messages get cache_control.
func (c *Client) buildMessages(messages []llmtypes.ChatMessage) []sdk.MessageParam {
	cacheCount := c.recentMessageCacheCount()

	// Identify the indices of the trailing user messages (last N user messages
	// where N=cacheCount). Walk backwards counting user messages.
	cacheableUserIdx := map[int]bool{}
	if cacheCount > 0 {
		seen := 0
		for i := len(messages) - 1; i >= 0 && seen < cacheCount; i-- {
			if messages[i].Role == "user" {
				cacheableUserIdx[i] = true
				seen++
			}
		}
	}

	out := make([]sdk.MessageParam, 0, len(messages))
	for i, m := range messages {
		var role sdk.MessageParamRole
		switch m.Role {
		case "assistant":
			role = sdk.MessageParamRoleAssistant
		default:
			// "user" plus any unknown role falls through as user — matches
			// the lenient shape the deleted adapter accepted (it never
			// errored on role).
			role = sdk.MessageParamRoleUser
		}

		applyCache := cacheableUserIdx[i]
		blocks := contentBlocksFromMessage(m, applyCache)
		out = append(out, sdk.MessageParam{Role: role, Content: blocks})
	}
	return out
}

// contentBlocksFromMessage flattens a ChatMessage into ContentBlockParamUnion[].
// Blocks ordering is preserved. When applyCache is true, cache_control is set
// on the last non-thinking block — Anthropic rejects cache_control on
// thinking blocks. When the message has no ContentBlocks, falls back to a
// single text block from m.Content.
func contentBlocksFromMessage(m llmtypes.ChatMessage, applyCache bool) []sdk.ContentBlockParamUnion {
	if len(m.ContentBlocks) == 0 {
		if m.Content == "" {
			return nil
		}
		text := sdk.TextBlockParam{Text: m.Content}
		if applyCache {
			text.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		return []sdk.ContentBlockParamUnion{{OfText: &text}}
	}

	out := make([]sdk.ContentBlockParamUnion, 0, len(m.ContentBlocks))
	// Find last non-thinking block index for cache_control placement.
	lastNonThinking := -1
	if applyCache {
		for i := len(m.ContentBlocks) - 1; i >= 0; i-- {
			if m.ContentBlocks[i].Type != "thinking" {
				lastNonThinking = i
				break
			}
		}
	}

	for i, b := range m.ContentBlocks {
		switch b.Type {
		case "text":
			text := sdk.TextBlockParam{Text: b.Text}
			if applyCache && i == lastNonThinking {
				text.CacheControl = sdk.NewCacheControlEphemeralParam()
			}
			out = append(out, sdk.ContentBlockParamUnion{OfText: &text})
		case "tool_use":
			var input any
			if b.Input != nil {
				input = *b.Input
			} else {
				input = map[string]any{}
			}
			tu := sdk.ToolUseBlockParam{
				ID:    b.ID,
				Name:  b.Name,
				Input: input,
			}
			if applyCache && i == lastNonThinking {
				tu.CacheControl = sdk.NewCacheControlEphemeralParam()
			}
			out = append(out, sdk.ContentBlockParamUnion{OfToolUse: &tu})
		case "tool_result":
			tr := sdk.ToolResultBlockParam{
				ToolUseID: b.ToolUseID,
				Content: []sdk.ToolResultBlockParamContentUnion{
					{OfText: &sdk.TextBlockParam{Text: b.Content}},
				},
			}
			if b.IsError {
				tr.IsError = sdk.Bool(true)
			}
			if applyCache && i == lastNonThinking {
				tr.CacheControl = sdk.NewCacheControlEphemeralParam()
			}
			out = append(out, sdk.ContentBlockParamUnion{OfToolResult: &tr})
		case "thinking":
			// Anthropic requires both thinking text and signature; without
			// signature the block is invalid and rejected. Mirror the
			// deleted adapter's tolerance: emit when both are present.
			if b.Text == "" || b.Signature == "" {
				continue
			}
			out = append(out, sdk.ContentBlockParamUnion{
				OfThinking: &sdk.ThinkingBlockParam{
					Thinking:  b.Text,
					Signature: b.Signature,
				},
			})
		}
	}
	return out
}

// buildMessageParams constructs the SDK MessageNewParams for a ChatRequest,
// applying cache_control + thinking_config when configured. Used by both
// the streaming and non-streaming paths so request shape stays consistent.
func (c *Client) buildMessageParams(in llmtypes.ChatRequest, model string, interleavedThinking bool, reasoningCfg llmcontracts.ReasoningConfig) sdk.MessageNewParams {
	params := sdk.MessageNewParams{
		Model:     model,
		MaxTokens: resolveMaxTokens(in),
		Messages:  c.buildMessages(in.Messages),
	}
	if sys := c.buildSystemBlocks(in); len(sys) > 0 {
		params.System = sys
	}
	if tools := c.buildTools(in.Tools); len(tools) > 0 {
		params.Tools = tools
	}
	if interleavedThinking && reasoningCfg.BudgetTokens > 0 {
		params.Thinking = sdk.ThinkingConfigParamOfEnabled(int64(reasoningCfg.BudgetTokens))
	}
	return params
}

// extractSchemaProperties pulls the "properties" map out of a JSON-schema
// document (passed as map[string]any) for use in ToolInputSchemaParam.
// Returns nil when properties are absent — Anthropic accepts an empty
// properties object.
func extractSchemaProperties(schema map[string]any) any {
	if schema == nil {
		return nil
	}
	if p, ok := schema["properties"]; ok {
		return p
	}
	return nil
}

// extractSchemaRequired pulls the "required" array out of a JSON-schema
// document, normalising to []string when present.
func extractSchemaRequired(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// EstimateCacheablePrefix implements llmcontracts.Cacheable. Builds the same
// MessageNewParams the wrapper would send, marshals it, and returns the
// offset of the last cache_control marker / 4 (token approximation).
//
// This stays in lock-step with the rate-budget pre-flight in StreamChat —
// both consume the same payload bytes and the same heuristic, just expressed
// in different units. Future readers changing one should change the other
// together.
func (c *Client) EstimateCacheablePrefix(ctx context.Context, in llmtypes.ChatRequest) int {
	if len(c.cacheHints) == 0 {
		return 0
	}
	model := resolveModel(in)
	reasoningCfg := llmcontracts.ReasoningConfigFromContext(ctx)
	interleavedThinking := shouldEnableInterleavedThinking(reasoningCfg, model)
	params := c.buildMessageParams(in, model, interleavedThinking, reasoningCfg)
	payload, err := json.Marshal(params)
	if err != nil {
		return 0
	}
	return computeCacheablePrefixBytes(payload, c.cacheHints) / 4
}

// computeCacheablePrefixBytes approximates the byte size of the cached
// prefix in payload as the offset of the last `"cache_control":{` marker.
// Mirrors the heuristic from the deleted adapter; see provider/anthropic.go
// rev abb3a08 for the prose justification.
func computeCacheablePrefixBytes(payload []byte, hints []llmcontracts.CacheHint) int {
	if len(hints) == 0 {
		return 0
	}
	idx := bytes.LastIndex(payload, []byte(`"cache_control":{`))
	if idx < 0 {
		return 0
	}
	return idx
}

// betaHeaderRequestOption returns a per-call option setting the
// `anthropic-beta` header. Combines the baseline prompt-caching beta with
// optional interleaved-thinking when applicable.
func betaHeaderRequestOption(interleavedThinking bool) option.RequestOption {
	value := "prompt-caching-2024-07-31"
	if interleavedThinking {
		value += "," + InterleavedThinkingBetaHeader
	}
	return option.WithHeader("anthropic-beta", value)
}
