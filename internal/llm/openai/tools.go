package openai

import (
	llmtypes "github.com/hollis-labs/go-llm-types"
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// translateTools converts nanite's internal tool definitions into the SDK's
// ChatCompletionToolParam shape. The InputSchema map flows through as the
// SDK's FunctionParameters (also a map[string]any) without copying.
func translateTools(tools []llmtypes.ToolDefinition) []sdk.ChatCompletionToolParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]sdk.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		fn := shared.FunctionDefinitionParam{
			Name:       t.Name,
			Parameters: shared.FunctionParameters(t.InputSchema),
		}
		if t.Description != "" {
			fn.Description = sdk.String(t.Description)
		}
		if t.Strict != nil {
			fn.Strict = sdk.Bool(*t.Strict)
		}
		out = append(out, sdk.ChatCompletionToolParam{Function: fn})
	}
	return out
}
