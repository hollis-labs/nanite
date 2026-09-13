package openai

import (
	llmtypes "github.com/hollis-labs/go-llm-types"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

// translateTools converts nanite's internal tool definitions into the SDK's
// ChatCompletionToolParam shape. The InputSchema map flows through as the
// SDK's FunctionParameters (also a map[string]any) without copying.
func translateTools(tools []llmtypes.ToolDefinition) []sdk.ChatCompletionToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]sdk.ChatCompletionToolUnionParam, 0, len(tools))
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
		out = append(out, sdk.ChatCompletionToolUnionParam{OfFunction: &sdk.ChatCompletionFunctionToolParam{Function: fn}})
	}
	return out
}
