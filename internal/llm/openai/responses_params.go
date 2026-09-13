package openai

import (
	"context"
	"encoding/json"
	"fmt"

	llmtypes "github.com/hollis-labs/go-llm-types"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// responseOutputBlock carries the complete provider output through the local
// tool loop. It preserves encrypted reasoning, call IDs and message phases in
// their original order; it is never rendered as text or sent to other providers.
const responseOutputBlock = "openai_response_output"

func buildResponseParams(ctx context.Context, req llmtypes.ChatRequest) (responses.ResponseNewParams, error) {
	params := responses.ResponseNewParams{
		Model: req.Model, Store: sdk.Bool(false), Reasoning: responseReasoning(ctx),
		Include: []responses.ResponseIncludable{"reasoning.encrypted_content"},
	}
	if sys := req.EffectiveSystemPrompt(); sys != "" {
		params.Instructions = sdk.String(sys)
	}
	if req.MaxTokens > 0 {
		params.MaxOutputTokens = sdk.Int(int64(req.MaxTokens))
	}
	input := make([]responses.ResponseInputItemUnionParam, 0, len(req.Messages))
	for _, message := range req.Messages {
		items, err := responseMessage(message)
		if err != nil {
			return responses.ResponseNewParams{}, err
		}
		input = append(input, items...)
	}
	params.Input.OfInputItemList = input
	for _, tool := range req.Tools {
		// Responses defaults omitted strict to true. Nanite's contract defaults
		// to non-strict, so send false explicitly unless the tool opts in.
		strict := tool.Strict != nil && *tool.Strict
		fn := responses.FunctionToolParam{
			Name: tool.Name, Parameters: tool.InputSchema, Strict: sdk.Bool(strict),
		}
		if tool.Description != "" {
			fn.Description = sdk.String(tool.Description)
		}
		params.Tools = append(params.Tools, responses.ToolUnionParam{OfFunction: &fn})
	}
	return params, nil
}

func responseMessage(message llmtypes.ChatMessage) ([]responses.ResponseInputItemUnionParam, error) {
	if message.Role != "user" && message.Role != "assistant" && message.Role != "system" && message.Role != "developer" && message.Role != "tool" {
		return nil, fmt.Errorf("openai: unsupported message role %q", message.Role)
	}
	if message.Role == "assistant" {
		for _, block := range message.ContentBlocks {
			if block.Type == responseOutputBlock {
				var rawItems []json.RawMessage
				if err := json.Unmarshal([]byte(block.Text), &rawItems); err != nil {
					return nil, fmt.Errorf("openai: invalid response continuation: %w", err)
				}
				items := make([]responses.ResponseInputItemUnionParam, 0, len(rawItems))
				for _, raw := range rawItems {
					items = append(items, param.Override[responses.ResponseInputItemUnionParam](raw))
				}
				return items, nil
			}
		}
	}
	var items []responses.ResponseInputItemUnionParam
	text := func(content string) {
		if content != "" {
			items = append(items, responses.ResponseInputItemUnionParam{OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRole(message.Role),
				Content: responses.EasyInputMessageContentUnionParam{OfString: sdk.String(content)},
			}})
		}
	}
	if len(message.ContentBlocks) == 0 {
		if message.Role == "tool" {
			return nil, fmt.Errorf("openai: tool result is missing its call ID")
		}
		text(message.Content)
		return items, nil
	}
	for _, block := range message.ContentBlocks {
		switch block.Type {
		case "text", "":
			text(block.Text)
		case "tool_use":
			input := map[string]any{}
			if block.Input != nil {
				input = *block.Input
			}
			args, err := json.Marshal(input)
			if err != nil {
				return nil, fmt.Errorf("openai: marshal tool input: %w", err)
			}
			items = append(items, responses.ResponseInputItemUnionParam{OfFunctionCall: &responses.ResponseFunctionToolCallParam{
				CallID: block.ID, Name: block.Name, Arguments: string(args),
			}})
		case "tool_result":
			if block.ToolUseID == "" {
				return nil, fmt.Errorf("openai: tool result is missing its call ID")
			}
			content := block.Content
			if block.IsError && content == "" {
				content = "tool execution error"
			}
			items = append(items, responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
				CallID: sdk.String(block.ToolUseID),
				Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{OfString: sdk.String(content)},
			}})
		}
	}
	return items, nil
}
