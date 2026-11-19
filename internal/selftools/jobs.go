package selftools

import (
	"context"
	"encoding/json"

	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

type JobTools struct {
	StartShell func(context.Context, json.RawMessage) (string, error)
	// Spawn and Status set Result.IsError when a subagent stopped at a limit
	// or failed, so the model reads the outcome as structural, not prose.
	Spawn  func(context.Context, subagent.Start) (tools.Result, error)
	Status func(context.Context, string, string) (tools.Result, error)
	Cancel func(context.Context, string, string) (string, error)
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
	if t.Spawn != nil {
		out = append(out, specResult("subagent_spawn", "Give a worker or planner a focused task. A result with status \"limit\" means the subagent stopped at a step, token or context limit before finishing; its output is partial.", `{"role":{"enum":["worker","planner"]},"task":{"type":"string"},"background":{"type":"boolean"}}`,
			[]string{"role", "task"}, false, t.Spawn))
	}
	for _, entry := range []struct{ prefix, kind string }{{"background", "shell"}, {"subagent", "subagent"}} {
		out = append(out,
			specResult(entry.prefix+"_status", "Read a job's status and available output.", idProperties, []string{"id"}, true,
				func(ctx context.Context, args struct{ ID string }) (tools.Result, error) {
					return t.Status(ctx, entry.kind, args.ID)
				}),
			spec(entry.prefix+"_cancel", "Cancel a running job.", idProperties, []string{"id"}, false,
				func(ctx context.Context, args struct{ ID string }) (string, error) {
					return t.Cancel(ctx, entry.kind, args.ID)
				}),
		)
	}
	return out
}
