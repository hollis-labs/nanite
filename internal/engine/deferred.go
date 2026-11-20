package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/recover"
	"github.com/hollis-labs/nanite/internal/tools"
)

// deferTools runs after variant filtering, so discovery cannot expose tools
// that the current conversation is not allowed to use.
func (x *execution) deferTools() error {
	x.deferredSpecs = make(map[string]llmtypes.ToolDefinition)
	x.described = make(map[string]bool)
	var visible []llmtypes.ToolDefinition
	var listing []string
	for _, spec := range x.specs {
		if !x.catalog[spec.Name].Deferred {
			visible = append(visible, spec)
			continue
		}
		x.deferredSpecs[spec.Name] = spec
		description := strings.Join(strings.Fields(spec.Description), " ")
		if runes := []rune(description); len(runes) > 240 {
			description = string(runes[:240]) + "…"
		}
		listing = append(listing, spec.Name+": "+description)
	}
	if len(listing) == 0 {
		return nil
	}
	if _, exists := x.catalog["tool_describe"]; exists {
		return fmt.Errorf("tool_describe is reserved for deferred tool discovery")
	}
	slices.Sort(listing)
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`)
	description := "Load a deferred tool's full schema before calling it. It becomes available for the rest of this turn. Deferred tools:\n" + strings.Join(listing, "\n")
	x.catalog["tool_describe"] = tools.Tool{Name: "tool_describe", Description: description, InputSchema: schema, ReadOnly: true,
		Handler: func(_ context.Context, call tools.Call) (tools.Result, error) {
			var args struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(call.Input, &args); err != nil {
				return tools.Result{}, err
			}
			spec, ok := x.deferredSpecs[args.Name]
			if !ok {
				return tools.Result{Text: "Deferred tool unavailable: " + args.Name, IsError: true}, nil
			}
			raw, err := json.Marshal(spec)
			if err != nil {
				return tools.Result{}, err
			}
			x.describeMu.Lock()
			x.described[args.Name] = true
			x.describeMu.Unlock()
			return tools.Result{Text: string(raw)}, nil
		}}
	x.validators["tool_describe"] = recover.NewValidator(schema)
	var input map[string]any
	if err := json.Unmarshal(schema, &input); err != nil {
		return err
	}
	x.specs = append(visible, llmtypes.ToolDefinition{Name: "tool_describe", Description: description, InputSchema: input})
	return nil
}

// Called after the parallel tool batch has joined. Append in name order to
// keep request prefixes deterministic regardless of completion order.
func (x *execution) promoteTools() {
	x.describeMu.Lock()
	defer x.describeMu.Unlock()
	var names []string
	for name := range x.described {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !slices.ContainsFunc(x.specs, func(s llmtypes.ToolDefinition) bool { return s.Name == name }) {
			x.specs = append(x.specs, x.deferredSpecs[name])
		}
	}
	if len(names) > 0 {
		x.sizeWindow()
		clear(x.described)
	}
}
