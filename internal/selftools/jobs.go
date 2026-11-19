package selftools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

type JobTools struct {
	StartShell func(context.Context, json.RawMessage) (string, error)
	// Subtask sets Result.IsError when a subtask did not end ok, so the model
	// reads the outcome as structural, not prose. Variants are the choices
	// offered for its required variant field.
	Subtask  func(context.Context, subagent.Request) (tools.Result, error)
	Variants map[string]subagent.Variant
	// SubtaskBackground offers subtask's background field. Leave it false
	// unless background_status is offered too, so the model never gets a job
	// id it cannot poll.
	SubtaskBackground bool
	// Status and Cancel serve background shell commands and background
	// subtasks alike, by job id.
	Status func(context.Context, string) (tools.Result, error)
	Cancel func(context.Context, string) (string, error)
}

func (t JobTools) Tools() []tools.Tool {
	idProperties := `{"id":{"type":"string"}}`
	var out []tools.Tool
	if t.StartShell != nil {
		out = append(out, tools.Tool{Name: "background_start",
			Description: "Start a background Bash command. It can continue after this turn finishes.",
			Destructive: true, InputSchema: json.RawMessage(BashSchema),
			Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
				text, err := t.StartShell(ctx, call.Input)
				return tools.Result{Text: text}, err
			},
		})
	}
	if t.Subtask != nil {
		out = append(out, subtaskTool(t.Subtask, t.Variants, t.SubtaskBackground))
	}
	if t.StartShell != nil || t.Subtask != nil {
		out = append(out,
			specResult("background_status", "Read a background job's status and available output: a command from background_start or a subtask started with background true.", idProperties, []string{"id"}, true,
				func(ctx context.Context, args struct{ ID string }) (tools.Result, error) {
					return t.Status(ctx, args.ID)
				}),
			spec("background_cancel", "Cancel a running background job.", idProperties, []string{"id"}, false,
				func(ctx context.Context, args struct{ ID string }) (string, error) { return t.Cancel(ctx, args.ID) }),
		)
	}
	return out
}

func subtaskTool(run func(context.Context, subagent.Request) (tools.Result, error), variants map[string]subagent.Variant, background bool) tools.Tool {
	names := subagent.Names(variants)
	var listing strings.Builder
	for _, name := range names {
		listing.WriteString("\n- " + name)
		if d := variants[name].Description; d != "" {
			listing.WriteString(": " + d)
		}
	}
	enum, _ := json.Marshal(names)
	properties := `{"goal":{"type":"string","description":"What the subtask must achieve."},` +
		`"return_spec":{"description":"What to return: prose, or a JSON Schema object.","anyOf":[{"type":"string"},{"type":"object"}]},` +
		`"variant":{"type":"string","enum":` + string(enum) + `,"description":"Which kind of subtask runs it. Required; choose deliberately."},` +
		`"inputs":{"type":"object","description":"Values the subtask needs verbatim."},` +
		`"context":{"type":"object","description":"Parent context to share (last_n, include, full)."}`
	if background {
		properties += `,"background":{"type":"boolean","description":"Run in the background and get a job id for background_status; completion arrives as a notice."}`
	}
	properties += "}"
	description := "Delegate a focused goal to a child conversation and get back its result, not its tool traffic. " +
		"The result has a status: ok, partial, limit (stopped at a step, token or time budget; the result is incomplete), error or cancelled. Variants:" + listing.String()
	return specResult(subagent.ToolName, description, properties, []string{"goal", "return_spec", "variant"}, false, run)
}
