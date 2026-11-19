package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hollis-labs/libs/core/panics"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/agent/core/loopdetect"
	"github.com/hollis-labs/libs/agent/core/toolresult"
	"github.com/hollis-labs/libs/agent/core/tooluse"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/mcpclient"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/recover"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

func (x *execution) prepareTools(host, mcp []tools.Tool, skillCatalog skills.Catalog) error {
	cache := x.engine.cache
	catalog := selftools.New(x.engine.cfg.Store, x.info.ID, x.engine.cfg.WorkspaceRoots)
	catalog = append(catalog, selftools.Question(func(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
		return x.ask(ctx, prompt, schema)
	}))

	catalog = append(catalog, selftools.Skills(skillCatalog)...)
	catalog = append(catalog, mcp...)
	catalog = append(catalog, x.jobTools()...)
	roots := x.engine.cfg.WorkspaceRoots
	if len(roots) > 0 {
		catalog = append(catalog, selftools.Bash(shell.Plain{}, roots[0], x.definition.Runtime.Env))
	}

	for _, item := range []struct {
		spec    toolresult.ToolSpec
		handler func(context.Context, string, map[string]any, int) (string, bool)
	}{{cache.FetchSpec(), cache.HandleFetch}, {cache.SearchSpec(), cache.HandleSearch}} {
		schema, err := json.Marshal(item.spec.InputSchema)
		if err != nil {
			return err
		}
		catalog = append(catalog, tools.Tool{Name: item.spec.Name, Description: item.spec.Description, InputSchema: schema, ReadOnly: true,
			Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
				args, err := permission.Args(call.Input)
				if err != nil {
					return tools.Result{}, err
				}
				text, failed := item.handler(ctx, x.info.ID, args, 0)
				return tools.Result{Text: text, IsError: failed}, nil
			},
		})
	}
	// A withheld built-in is never sent to the model. Host tools are the host's own choice.
	catalog = slices.DeleteFunc(catalog, func(tool tools.Tool) bool { return !x.engine.cfg.Builtins.Offers(tool.Name) })
	x.catalog = make(map[string]tools.Tool)
	x.validators = make(map[string]recover.Validator)
	for _, tool := range append(catalog, host...) {
		if !subagent.AllowsTool(x.depth == 0, x.variant, x.canSpawn(), tool.Name, tool.ReadOnly) {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return fmt.Errorf("tool %s schema: %w", tool.Name, err)
		}
		x.catalog[tool.Name] = tool
		x.validators[tool.Name] = recover.NewValidator(tool.InputSchema)
		x.specs = append(x.specs, llmtypes.ToolDefinition{Name: tool.Name, Description: tool.Description, InputSchema: schema})
	}
	return nil
}

func (x *execution) authorize(ctx context.Context, t tools.Tool, call llmtypes.ToolUseBlock, raw json.RawMessage) permission.PermissionDecision {
	principal := "urn:nanite:agent:" + url.PathEscape(x.definition.Snapshot.Definition.DefinitionID)
	req := permission.PermissionRequest{ID: call.ID, ConversationID: x.info.ID, TurnID: x.run.id, Tool: t.Name,
		Input: x.engine.scrub.Bytes(raw), ReadOnly: t.ReadOnly, Destructive: t.Destructive}
	// A panic while deciding fails closed: the call is denied.
	var effect policy.Decision
	err := x.engine.panics.Call("permission policy", func() (err error) {
		effect, err = permission.Evaluate(ctx, x.engine.cfg.Logger, x.engine.cfg.PDP, x.policy, principal, x.info.Role, req, call.Input)
		return err
	}, panics.With("conversation", x.info.ID), panics.With("call", call.ID))
	decision := permission.PermissionDecision{Allow: effect.Effect == policy.Allow && err == nil, Reason: effect.Reason}
	if ref, ok := panics.Ref(err); ok {
		decision.Reason = "permission check failed (ref " + ref + ")"
	}
	if effect.Effect == policy.Ask && err == nil {
		e := x.engine
		e.mu.Lock()
		granted := e.grants[x.info.ID][t.Name]
		e.mu.Unlock()
		if granted {
			decision = permission.PermissionDecision{Allow: true, Scope: permission.ScopeConversation, Reason: "conversation approval"}
		} else {
			req.Reason = x.engine.scrub.Text(effect.Reason)
			x.run.emit(ctx, func(meta events.EventMeta) events.Event { return events.PermissionAsked{EventMeta: meta, Request: req} })
			gateReq := req
			gateReq.Input = raw
			err = e.panics.Call("approval gate", func() (err error) {
				decision, err = e.cfg.Gate.Decide(ctx, gateReq)
				return err
			}, panics.With("conversation", x.info.ID), panics.With("call", call.ID))
			if err != nil {
				decision = permission.PermissionDecision{Reason: "approval gate failed"}
			}
			if decision.Allow && decision.Scope == permission.ScopeConversation {
				e.mu.Lock()
				if e.grants[x.info.ID] == nil {
					e.grants[x.info.ID] = make(map[string]bool)
				}
				e.grants[x.info.ID][t.Name] = true
				e.mu.Unlock()
			}
		}
	}
	decision.Reason = x.engine.scrub.Text(decision.Reason)
	now := time.Now().UTC()
	payload := struct {
		TurnID, CallID, Tool string
		Decision             permission.PermissionDecision
	}{x.run.id, call.ID, t.Name, decision}
	if err := x.journal.RecordPermission(store.NewID(now), now, payload); err != nil {
		decision = permission.PermissionDecision{Reason: "could not persist permission decision"}
	}
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.PermissionResolved{EventMeta: meta, ID: call.ID, Decision: decision}
	})
	return decision
}

func (x *execution) callTools(ctx context.Context, calls []llmtypes.ToolUseBlock) error {
	results := make([]llmtypes.ContentBlock, len(calls))
	jobs := make([]tooluse.Job, len(calls))
	args := make([]json.RawMessage, len(calls))
	// Resolve approvals in model order so interactive prompts cannot overlap.
	for i, call := range calls {
		results[i] = llmtypes.ContentBlock{Type: "tool_result", ToolUseID: call.ID, Content: "tool did not complete", IsError: true}
		if ctx.Err() != nil {
			results[i].Content = "cancelled"
			continue
		}
		raw, err := json.Marshal(call.Input)
		if err != nil || string(raw) == "null" {
			raw = []byte("{}")
		}
		args[i] = raw
		// Every call the model made is reported, including one that cannot run.
		started := x.engine.scrub.Bytes(raw)
		x.run.emit(ctx, func(meta events.EventMeta) events.Event {
			return events.ToolCallStarted{EventMeta: meta, CallID: call.ID, Name: x.engine.scrub.Text(call.Name), Input: started}
		})
		if err != nil {
			results[i].Content = "invalid tool arguments"
			continue
		}
		t, ok := x.catalog[call.Name]
		if !ok {
			results[i].Content = "tool unavailable: " + call.Name
			continue
		}
		if err := x.validators[t.Name].Validate(call.Input); err != nil {
			results[i].Content = "invalid tool arguments: " + x.engine.scrub.Text(err.Error())
			continue
		}

		if call.Name == "bash" || call.Name == "background_start" {
			if _, err := selftools.ParseBash(raw); err != nil {
				results[i].Content = x.engine.scrub.Text(err.Error())
				continue
			}
		}

		if d := x.authorize(ctx, t, call, raw); !d.Allow {
			results[i].Content = "permission denied: " + d.Reason
			continue
		}
		if _, loop := x.loop.Record(loopdetect.Signal{SessionID: x.info.ID, TurnID: x.run.id, ToolName: call.Name, Args: raw, Timestamp: time.Now()}); loop {
			results[i].Content = "repeated identical tool call; try another approach"
			continue
		}
		jobs[i] = tooluse.Job{Ready: true, Concurrent: t.ReadOnly}
	}
	tooluse.Batch(ctx, jobs, x.definition.Runtime.ToolConcurrency, func(i int, _ bool) {
		call := calls[i]
		if ctx.Err() != nil {
			results[i].Content = "cancelled"
			return
		}
		started := time.Now()
		toolCtx, endSpan := events.ToolSpan(ctx, x.engine.scrub.Text(call.Name))
		defer endSpan()

		var result tools.Result
		// A panicking tool yields an error result carrying the panic ref.
		err := x.engine.panics.Call("tool "+call.Name, func() (err error) {
			result, err = x.catalog[call.Name].Handler(x.askContext(toolCtx, call.Name), tools.Call{ID: call.ID, Name: call.Name, Input: args[i]})
			return err
		}, panics.With("conversation", x.info.ID), panics.With("call", call.ID))
		if err != nil {
			result = tools.Result{Text: err.Error(), IsError: true}
		}
		result.Text = x.engine.scrub.Text(result.Text)
		view, err := x.engine.cache.Present(ctx, x.info.ID, toolresult.Meta{Tool: call.Name, CallID: call.ID, IsError: result.IsError}, result.Text, 0)
		if err == nil {
			result.Text = view.Content
		}
		if ctx.Err() != nil {
			result = tools.Result{Text: "cancelled", IsError: true}
		}
		results[i].Content, results[i].IsError = result.Text, result.IsError
		x.run.emit(ctx, func(meta events.EventMeta) events.Event {
			return events.ToolCallFinished{EventMeta: meta, CallID: call.ID, Result: result, IsError: result.IsError, Duration: time.Since(started)}
		})
	}, func(i int) { results[i].Content = "cancelled" }, nil)
	for i, job := range jobs {
		if job.Ready {
			continue
		}
		result := tools.Result{Text: results[i].Content, IsError: true}
		x.run.emit(ctx, func(meta events.EventMeta) events.Event {
			return events.ToolCallFinished{EventMeta: meta, CallID: calls[i].ID, Result: result, IsError: true}
		})
	}
	if err := x.message("tool", results); err != nil {
		return err
	}
	return ctx.Err()
}

func (x *execution) cancelledTools(calls []llmtypes.ToolUseBlock) error {
	blocks := make([]llmtypes.ContentBlock, len(calls))
	for i, call := range calls {
		blocks[i] = llmtypes.ContentBlock{Type: "tool_result", ToolUseID: call.ID, Content: "turn interrupted before execution", IsError: true}
	}
	return x.message("tool", blocks)
}

func (x *execution) ask(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	return x.run.ask(ctx, x.engine.scrub.Text(prompt), schema, x.engine.scrub.Bytes(schema))
}

func (x *execution) askContext(ctx context.Context, tool string) context.Context {
	if !strings.HasPrefix(tool, "mcp__") {
		return ctx
	}
	var ask mcpclient.Ask
	if x.info.ParentID == "" && x.info.Role == "chat" {
		ask = x.ask
	}
	return mcpclient.WithAsk(ctx, ask)
}
