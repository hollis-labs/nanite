package engine

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/recover"
	"github.com/hollis-labs/nanite/internal/tools"
)

func TestClosingReplyAtLimits(t *testing.T) {
	for _, limit := range []string{"max_steps", "max_tokens"} {
		t.Run(limit, func(t *testing.T) {
			first := toolResponse("unknown")
			if limit == "max_tokens" {
				first = fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "partial"}, {Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: limit}}, {Type: llmtypes.EventDone}}}
			}
			p := &watchedProvider{Provider: fake.New(first, fake.Text("Work remains."))}
			e, db, id := setupEngine(t, p, "runtime:\n  max_steps: 1\n")
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := run.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if result.StopReason != limit || result.Steps != 2 || !strings.HasSuffix(result.Text, "Work remains.") {
				t.Fatalf("result: %+v", result)
			}
			if len(p.requests) != 2 || len(p.requests[1].Tools) != 0 {
				t.Fatalf("closing request: %+v", p.requests)
			}
			history, err := db.History(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if history[len(history)-1].Blocks[0].Text != "Work remains." {
				t.Fatalf("closing reply not durable: %+v", history)
			}
		})
	}
}

func TestClosingReplyNeverRunsToolsOrRetries(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "tool ignored", true: "error"}[failure], func(t *testing.T) {
			last := toolResponse("host_check")
			if failure {
				last = fake.Response{Err: errors.New("closing failed")}
			}
			p := &watchedProvider{Provider: fake.New(toolResponse("unknown"), last)}
			e, db, id := setupEngine(t, p, "runtime:\n  max_steps: 1\n")
			called := false
			if err := e.cfg.Tools.SetGroup("test", tools.Tool{Name: "host_check", InputSchema: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, tools.Call) (tools.Result, error) { called = true; return tools.Result{}, nil }}); err != nil {
				t.Fatal(err)
			}
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := run.Wait()
			if failure != (err != nil) || called || len(p.requests) != 2 || result.StopReason != "max_steps" {
				t.Fatalf("result=%+v err=%v called=%v requests=%d", result, err, called, len(p.requests))
			}
			if !failure {
				h, err := db.History(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				if !h[len(h)-1].Blocks[0].IsError {
					t.Fatal("unpaired closing tool call")
				}
			}
		})
	}
}

func TestDeferredToolsDescribeAndPromote(t *testing.T) {
	describe := fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "describe", Name: "tool_describe", Input: map[string]any{"name": "cold"}}}, {Type: llmtypes.EventDone}}}
	p := &watchedProvider{Provider: fake.New(describe, toolResponse("cold"), fake.Text("done"), fake.Text("next turn"))}
	e, _, id := setupEngine(t, p, "")
	called := false
	if err := e.cfg.Tools.SetGroup("test", tools.Tool{Name: "cold", Description: "A cold tool.\nMore details.", Deferred: true, ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object","properties":{"unique_field":{"type":"string"}}}`), Handler: func(context.Context, tools.Call) (tools.Result, error) {
		called = true
		return tools.Result{Text: "ok"}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		run, err := e.Send(t.Context(), id, Input{Text: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	has := func(index int, name string) bool {
		return slices.ContainsFunc(p.requests[index].Tools, func(s llmtypes.ToolDefinition) bool { return s.Name == name })
	}
	if !called || has(0, "cold") || !has(0, "tool_describe") || !has(1, "cold") || !has(2, "cold") || has(3, "cold") {
		t.Fatalf("unexpected promotion; called=%v requests=%+v", called, p.requests)
	}
	raw, _ := json.Marshal(p.requests[0].Tools)
	if strings.Contains(string(raw), "unique_field") {
		t.Fatal("deferred schema leaked into first request")
	}
}

func TestRuntimeLimitOverrides(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(toolResponse("unknown"), fake.Text("closing"))}
	e, _, id := setupEngine(t, p, "runtime:\n  max_steps: 5\n  max_output_tokens: 100\n")
	e.cfg.MaxSteps = 1
	e.cfg.MaxOutputTokens = 200
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "max_steps" || len(p.requests) != 2 {
		t.Fatalf("result %+v", result)
	}
	for _, req := range p.requests {
		if req.MaxTokens != 200 {
			t.Fatalf("max tokens=%d", req.MaxTokens)
		}
	}
}

func TestClosingReplyDoesNotExceedTokenBudget(t *testing.T) {
	first := toolResponse("unknown")
	first.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 10}}}, first.Events...)
	p := &watchedProvider{Provider: fake.New(first)}
	e, _, id := setupEngine(t, p, "runtime:\n  max_steps: 1\n")
	e.cfg.TurnTokenBudget = 10
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 {
		t.Fatal("closing request exceeded token budget")
	}
}

func TestClosingReplyCancellation(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(toolResponse("unknown"), fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "closing partial"}}, Stall: true})}
	e, db, id := setupEngine(t, p, "runtime:\n  max_steps: 1\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	for event := range run.Events() {
		if delta, ok := event.(events.TextDelta); ok && delta.Text == "closing partial" {
			run.Cancel()
		}
	}
	result, err := run.Wait()
	if !errors.Is(err, context.Canceled) || result.StopReason != "max_steps" || result.Text != "closing partial" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	h, err := db.History(t.Context(), id)
	if err != nil || h[len(h)-1].Blocks[0].Text != "closing partial" {
		t.Fatalf("partial reply missing: %+v %v", h, err)
	}
}

func TestDeferredDiscoveryRefusesUnknownAndReservedNames(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	x := execution{catalog: map[string]tools.Tool{"cold": {Name: "cold", Deferred: true, InputSchema: schema}}, validators: make(map[string]recover.Validator), specs: []llmtypes.ToolDefinition{{Name: "cold", InputSchema: map[string]any{"type": "object"}}}}
	if err := x.deferTools(); err != nil {
		t.Fatal(err)
	}
	result, err := x.catalog["tool_describe"].Handler(t.Context(), tools.Call{Input: json.RawMessage(`{"name":"hidden"}`)})
	if err != nil || !result.IsError || len(x.described) != 0 {
		t.Fatalf("unknown discovery: %+v %v", result, err)
	}
	if err := (&tools.Groups{}).SetGroup("host", tools.Tool{Name: "tool_describe", InputSchema: schema, Handler: func(context.Context, tools.Call) (tools.Result, error) { return tools.Result{}, nil }}); err == nil {
		t.Fatal("host stole discovery name")
	}
}

func TestParallelDeferredDescriptions(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	x := execution{catalog: make(map[string]tools.Tool), validators: make(map[string]recover.Validator)}
	for _, name := range []string{"zebra", "alpha"} {
		x.catalog[name] = tools.Tool{Name: name, Deferred: true, InputSchema: schema}
		x.specs = append(x.specs, llmtypes.ToolDefinition{Name: name, InputSchema: map[string]any{"type": "object"}})
	}
	if err := x.deferTools(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		name := []string{"zebra", "alpha"}[i%2]
		wg.Go(func() {
			raw, _ := json.Marshal(map[string]string{"name": name})
			if _, err := x.catalog["tool_describe"].Handler(t.Context(), tools.Call{Input: raw}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	x.promoteTools()
	if len(x.specs) != 3 || x.specs[1].Name != "alpha" || x.specs[2].Name != "zebra" {
		t.Fatalf("promotion order or duplicates: %+v", x.specs)
	}
	x.promoteTools()
	if len(x.specs) != 3 {
		t.Fatal("repeated promotion duplicated schemas")
	}
}
