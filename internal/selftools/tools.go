package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
)

func New(db *store.Store, conversation string, roots []string) []tools.Tool {
	workspace := workspace{roots: roots}
	return []tools.Tool{
		spec("todo_list", "Read the current conversation's checklist.", `{}`, nil, true,
			func(ctx context.Context, _ struct{}) (string, error) {
				items, err := db.Todos(ctx, conversation)
				if err != nil {
					return "", err
				}
				data, err := json.Marshal(items)
				return string(data), err
			}),
		spec("todo_write", "Replace the current conversation's checklist.", `{"items":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"status":{"enum":["pending","in_progress","completed"]}},"required":["text","status"]}}}`, []string{"items"}, false,
			func(ctx context.Context, args struct{ Items []store.Todo }) (string, error) {
				if args.Items == nil {
					return "", fmt.Errorf("items required")
				}
				for _, t := range args.Items {
					if strings.TrimSpace(t.Text) == "" || (t.Status != "pending" && t.Status != "in_progress" && t.Status != "completed") {
						return "", fmt.Errorf("invalid checklist item")
					}
				}
				return "Checklist updated.", db.ReplaceTodos(ctx, conversation, args.Items)
			}),
		spec("scratchpad_read", "Read a scratchpad entry.", `{"key":{"type":"string"}}`, []string{"key"}, true,
			func(ctx context.Context, args struct{ Key string }) (string, error) {
				if args.Key == "" {
					return "", fmt.Errorf("key required")
				}
				return db.Scratch(ctx, conversation, args.Key)
			}),
		spec("scratchpad_write", "Write a scratchpad entry.", `{"key":{"type":"string"},"content":{"type":"string"}}`, []string{"key", "content"}, false,
			func(ctx context.Context, args struct{ Key, Content string }) (string, error) {
				if args.Key == "" {
					return "", fmt.Errorf("key required")
				}
				return "Scratchpad updated.", db.SetScratch(ctx, conversation, args.Key, args.Content)
			}),
		spec("read_file", "Read a workspace file.", `{"path":{"type":"string"}}`, []string{"path"}, true, workspace.read),
		spec("list_dir", "List a workspace directory.", `{"path":{"type":"string"}}`, []string{"path"}, true, workspace.list),
		spec("write_file", "Create or replace a workspace file.", `{"path":{"type":"string"},"content":{"type":"string"}}`, []string{"path", "content"}, false, workspace.write),
		spec("edit_file", "Replace one exact occurrence in a workspace file.", `{"path":{"type":"string"},"old":{"type":"string"},"new":{"type":"string"}}`, []string{"path", "old", "new"}, false, workspace.edit),
	}
}

func spec[Args any](name, description, properties string, required []string, readOnly bool, handle func(context.Context, Args) (string, error)) tools.Tool {
	fields, _ := json.Marshal(required)
	schema := `{"type":"object","properties":` + properties + `,"additionalProperties":false`
	if len(required) > 0 {
		schema += `,"required":` + string(fields)
	}
	schema += "}"
	return tools.Tool{Name: name, Description: description, ReadOnly: readOnly, InputSchema: json.RawMessage(schema),
		Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var args Args
			if err := json.Unmarshal(call.Input, &args); err != nil {
				return tools.Result{}, err
			}
			text, err := handle(ctx, args)
			return tools.Result{Text: text}, err
		},
	}
}
