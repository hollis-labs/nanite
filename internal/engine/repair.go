package engine

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/recover"
)

const repairPrompt = `Repair the JSON tool arguments using the provided schema and validator error.
Only rearrange, rename, wrap, unwrap, or drop existing fields, or use defaults from the schema.
Never invent a value. Return {"repaired_args":object,"missing_required":[]} on success.
If required values are unavailable, return {"repaired_args":null,"missing_required":["field"]}.
Return exactly one JSON object.`

func (x *execution) repairArgs(ctx context.Context, s *step) {
	if strings.EqualFold(os.Getenv("NANITE_AUTO_REPAIR"), "false") {
		return
	}
	for i, call := range s.calls {
		validator, known := x.validators[call.Name]
		if !known || ctx.Err() != nil {
			continue
		}
		err := validator.Validate(call.Input)
		if !validator.Classify(err) {
			continue
		}
		tool := x.catalog[call.Name]
		raw, err := json.Marshal(struct {
			Tool      string
			Arguments map[string]any
			Schema    json.RawMessage
			Error     string
		}{call.Name, call.Input, tool.InputSchema, err.Error()})
		if err != nil || len(raw) > 32<<10 {
			continue
		}
		spanCtx, endSpan := events.ModelSpan(ctx, x.engine.scrub.Text(x.definition.Runtime.Model))
		repairCtx, cancel := context.WithTimeout(spanCtx, 30*time.Second)
		response, err := x.provider.Complete(repairCtx, llmtypes.ChatRequest{Model: x.definition.Runtime.Model,
			SystemPrompt: repairPrompt, MaxTokens: 1024, Messages: []llmtypes.ChatMessage{{Role: "user", Content: x.engine.scrub.Text(string(raw))}}})
		cancel()
		endSpan()
		if err != nil {
			continue
		}
		fixed, err := recover.ParseRepair(response, call.Input, tool.InputSchema)
		if err != nil || validator.Validate(fixed) != nil {
			continue
		}
		encoded, err := json.Marshal(fixed)
		if err != nil {
			continue
		}
		var clean map[string]any
		if err := json.Unmarshal(x.engine.scrub.Bytes(encoded), &clean); err != nil {
			continue
		}
		s.calls[i].Input = fixed
		for j, block := range s.blocks {
			if block.Type == "tool_use" && block.ID == call.ID {
				s.blocks[j].Input = &clean
				break
			}
		}
	}
}
