package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
	sdk "github.com/openai/openai-go"
)

// buildChatParams maps a nanite ChatRequest into the SDK's
// ChatCompletionNewParams. It handles:
//
//   - Effective system prompt (SystemPrompt + non-empty SlotBlocks joined by
//     blank lines), modeled as the first system-role message when present.
//   - User / assistant messages with plain text content.
//   - Assistant tool_use blocks (rendered as assistant messages with
//     `tool_calls`) and tool_result blocks (rendered as `tool` role
//     messages keyed by ToolUseID).
//   - Tool definitions translated via translateTools.
//   - MaxTokens routed to MaxCompletionTokens (OpenAI's recommended field
//     for newer models).
func buildChatParams(req llmtypes.ChatRequest) (sdk.ChatCompletionNewParams, error) {
	msgs := make([]sdk.ChatCompletionMessageParamUnion, 0, 1+len(req.Messages))

	if sys := req.EffectiveSystemPrompt(); sys != "" {
		msgs = append(msgs, sdk.SystemMessage(sys))
	}

	for _, m := range req.Messages {
		converted, err := translateMessage(m)
		if err != nil {
			return sdk.ChatCompletionNewParams{}, err
		}
		msgs = append(msgs, converted...)
	}

	params := sdk.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = sdk.Int(int64(req.MaxTokens))
	}
	if tools := translateTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}
	return params, nil
}

// translateMessage converts a single nanite ChatMessage into one or more SDK
// message params. Most messages map 1:1; assistant messages with a mix of
// text and tool_use blocks become a single assistant message with
// tool_calls; tool_result blocks each become a separate `tool` role message
// (the OpenAI shape — one tool result message per tool call).
func translateMessage(m llmtypes.ChatMessage) ([]sdk.ChatCompletionMessageParamUnion, error) {
	switch m.Role {
	case "user":
		return userMessages(m)
	case "assistant":
		return assistantMessages(m)
	case "system":
		// System prompts normally arrive via ChatRequest.SystemPrompt; treat
		// any explicit "system" role messages as additional system messages.
		return []sdk.ChatCompletionMessageParamUnion{sdk.SystemMessage(textOf(m))}, nil
	case "tool":
		// Already-formed tool results may be embedded as ChatMessage{Role:"tool"};
		// nanite typically routes tool_result through ContentBlocks on a user
		// or assistant message instead — that path is handled below.
		return []sdk.ChatCompletionMessageParamUnion{
			sdk.ToolMessage(textOf(m), ""),
		}, nil
	default:
		return nil, fmt.Errorf("openai: unsupported message role %q", m.Role)
	}
}

// userMessages renders a user-role turn. tool_result content blocks attached
// to a user-role message are emitted as separate `tool` role messages
// (matching the Anthropic-shape input nanite emits when feeding tool
// results back through the conversation).
func userMessages(m llmtypes.ChatMessage) ([]sdk.ChatCompletionMessageParamUnion, error) {
	var (
		out         []sdk.ChatCompletionMessageParamUnion
		userText    strings.Builder
		toolResults []sdk.ChatCompletionMessageParamUnion
	)
	if len(m.ContentBlocks) == 0 {
		return []sdk.ChatCompletionMessageParamUnion{sdk.UserMessage(m.Content)}, nil
	}
	for _, b := range m.ContentBlocks {
		switch b.Type {
		case "text", "":
			if b.Text == "" {
				continue
			}
			if userText.Len() > 0 {
				userText.WriteString("\n")
			}
			userText.WriteString(b.Text)
		case "tool_result":
			content := b.Content
			if b.IsError && content == "" {
				content = "tool execution error"
			}
			toolResults = append(toolResults,
				sdk.ToolMessage(content, b.ToolUseID),
			)
		}
	}
	if userText.Len() > 0 {
		out = append(out, sdk.UserMessage(userText.String()))
	} else if m.Content != "" && len(toolResults) == 0 {
		out = append(out, sdk.UserMessage(m.Content))
	}
	out = append(out, toolResults...)
	if len(out) == 0 {
		out = append(out, sdk.UserMessage(""))
	}
	return out, nil
}

// assistantMessages renders an assistant turn that may contain text +
// tool_use blocks plus any tool_result blocks (the latter become standalone
// tool messages).
func assistantMessages(m llmtypes.ChatMessage) ([]sdk.ChatCompletionMessageParamUnion, error) {
	if len(m.ContentBlocks) == 0 {
		return []sdk.ChatCompletionMessageParamUnion{sdk.AssistantMessage(m.Content)}, nil
	}

	var (
		assistantText strings.Builder
		toolCalls     []sdk.ChatCompletionMessageToolCallParam
		toolResults   []sdk.ChatCompletionMessageParamUnion
	)
	for _, b := range m.ContentBlocks {
		switch b.Type {
		case "text", "":
			if b.Text == "" {
				continue
			}
			if assistantText.Len() > 0 {
				assistantText.WriteString("\n")
			}
			assistantText.WriteString(b.Text)
		case "thinking":
			// OpenAI's chat-completions API has no analog for Anthropic's
			// signed thinking blocks. Drop silently — nanite uses these for
			// Anthropic-only extended-thinking flows.
			continue
		case "tool_use":
			args := ""
			if b.Input != nil {
				raw, err := json.Marshal(*b.Input)
				if err != nil {
					return nil, fmt.Errorf("openai: marshal tool_use input: %w", err)
				}
				args = string(raw)
			}
			toolCalls = append(toolCalls, sdk.ChatCompletionMessageToolCallParam{
				ID: b.ID,
				Function: sdk.ChatCompletionMessageToolCallFunctionParam{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case "tool_result":
			content := b.Content
			if b.IsError && content == "" {
				content = "tool execution error"
			}
			toolResults = append(toolResults,
				sdk.ToolMessage(content, b.ToolUseID),
			)
		}
	}

	out := make([]sdk.ChatCompletionMessageParamUnion, 0, 1+len(toolResults))
	if assistantText.Len() > 0 || len(toolCalls) > 0 {
		am := sdk.ChatCompletionAssistantMessageParam{}
		if assistantText.Len() > 0 {
			am.Content.OfString = sdk.String(assistantText.String())
		}
		if len(toolCalls) > 0 {
			am.ToolCalls = toolCalls
		}
		out = append(out, sdk.ChatCompletionMessageParamUnion{OfAssistant: &am})
	}
	out = append(out, toolResults...)
	if len(out) == 0 {
		out = append(out, sdk.AssistantMessage(""))
	}
	return out, nil
}

// textOf returns ChatMessage.Content unless the message carries
// ContentBlocks, in which case the text-typed blocks are concatenated.
func textOf(m llmtypes.ChatMessage) string {
	if len(m.ContentBlocks) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, blk := range m.ContentBlocks {
		if blk.Type == "text" || blk.Type == "" {
			if blk.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(blk.Text)
		}
	}
	if b.Len() == 0 {
		return m.Content
	}
	return b.String()
}
