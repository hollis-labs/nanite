package selftools

import (
	"context"
	"encoding/json"

	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

type JobTools struct {
	StartShell func(context.Context, json.RawMessage) (string, error)
	Spawn      func(context.Context, subagent.Start) (string, error)
	Status     func(context.Context, string, string) (string, error)
	Cancel     func(context.Context, string, string) (string, error)
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
		out = append(out, spec("subagent_spawn", "Give a worker or planner a focused task.", `{"role":{"enum":["worker","planner"]},"task":{"type":"string"},"background":{"type":"boolean"}}`,
			[]string{"role", "task"}, false, t.Spawn))
	}
	for _, entry := range []struct{ prefix, kind string }{{"background", "shell"}, {"subagent", "subagent"}} {
		out = append(out,
			spec(entry.prefix+"_status", "Read a job's status and available output.", idProperties, []string{"id"}, true,
				func(ctx context.Context, args struct{ ID string }) (string, error) {
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
