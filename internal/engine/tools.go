package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hollis-labs/libs/agent/trace"
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
	if x.info.ParentID != "" {
		// Only a subtask child can read its parent's history.
		catalog = append(catalog, x.parentHistory().Tools()...)
	}
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
				scope := x.info.ID
				if id, _ := args["id"].(string); id != "" && x.info.ParentID != "" {
					// A child may follow a pointer from the parent context it was given.
					scope = x.resultScope(ctx, id)
				}
				text, failed := item.handler(ctx, scope, args, 0)
				return tools.Result{Text: text, IsError: failed}, nil
			},
		})
	}
	// A withheld built-in is never sent to the model. Host tools are the host's own choice.
	catalog = slices.DeleteFunc(catalog, func(tool tools.Tool) bool { return !x.engine.cfg.Builtins.Offers(tool.Name) })
	x.catalog = make(map[string]tools.Tool)
	x.validators = make(map[string]recover.Validator)
	for _, tool := range append(catalog, host...) {
		if !subagent.AllowsTool(x.depth == 0, x.chain, x.canSpawn(), tool.Name, tool.ReadOnly) {
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

// authorize decides a call. asked reports that the decision came from the
// approval gate or an earlier approval in this conversation, not a rule.
func (x *execution) authorize(ctx context.Context, t tools.Tool, call llmtypes.ToolUseBlock, raw json.RawMessage) (_ permission.PermissionDecision, asked bool) {
	principal := "urn:nanite:agent:" + url.PathEscape(x.definition.Snapshot.Definition.DefinitionID)
	req := permission.PermissionRequest{ID: call.ID, ConversationID: x.info.ID, TurnID: x.run.id, Tool: t.Name,
		Input: x.engine.scrub.Bytes(raw), ReadOnly: t.ReadOnly, Destructive: t.Destructive}
	// A subtask inherits its root conversation's permissions: it is evaluated
	// as the root (scope and role), so the same rules, host PDP and grants
	// apply. The variant has already narrowed which tools it is offered.
	evalReq, role := req, x.info.Role
	var extra map[string]any
	if x.origin != nil {
		evalReq.ID, evalReq.ConversationID, role = x.relayID(req.ID), x.root.ID, x.root.Role
		extra = map[string]any{"subtask_variant": x.origin.Variant, "subtask_conversation": x.info.ID, "subtask_depth": x.origin.Depth}
	}
	// A panic while deciding fails closed: the call is denied.
	var effect policy.Decision
	err := x.engine.panics.Call("permission policy", func() (err error) {
		effect, err = permission.Evaluate(ctx, x.engine.cfg.Logger, x.engine.cfg.PDP, x.policy, principal, role, evalReq, call.Input, extra)
		return err
	}, panics.With("conversation", x.info.ID), panics.With("call", call.ID))
	decision := permission.PermissionDecision{Allow: effect.Effect == policy.Allow && err == nil, Reason: effect.Reason}
	if ref, ok := panics.Ref(err); ok {
		decision.Reason = "permission check failed (ref " + ref + ")"
	}
	if effect.Effect == policy.Ask && err == nil {
		asked = true
		// A conversation-scope allow covers the conversation it was given on
		// and the subtasks under it: a root's grant reaches its children, a
		// subtask's never reaches its parent or siblings.
		if x.granted(t.Name) {
			decision = permission.PermissionDecision{Allow: true, Scope: permission.ScopeConversation, Reason: "conversation approval"}
		} else {
			req.Reason = x.engine.scrub.Text(effect.Reason)
			gateReq := req
			gateReq.Input = raw
			if x.origin != nil {
				decision = x.relayApproval(ctx, req, gateReq)
			} else {
				// The root's own ask holds the ask slot from PermissionAsked
				// through PermissionResolved, below.
				release, err := x.engine.askTurn(ctx, x.root.ID)
				if err != nil {
					decision = permission.PermissionDecision{Reason: "approval not asked: " + err.Error()}
				} else {
					defer release()
					x.run.emit(ctx, func(meta events.EventMeta) events.Event { return events.PermissionAsked{EventMeta: meta, Request: req} })
					if askGap > 0 {
						time.Sleep(askGap)
					}
					decision = x.decide(ctx, gateReq)
				}
			}
			if decision.Allow && decision.Scope == permission.ScopeConversation {
				x.engine.grant(x.info.ID, t.Name)
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
	return decision, asked
}

func (x *execution) callTools(ctx context.Context, calls []llmtypes.ToolUseBlock) error {
	results := make([]llmtypes.ContentBlock, len(calls))
	jobs := make([]tooluse.Job, len(calls))
	args := make([]json.RawMessage, len(calls))
	// traced records each call for the turn's trace, in model order.
	traced := make([]trace.ToolCall, len(calls))
	for i, call := range calls {
		traced[i] = trace.ToolCall{ToolCallID: call.ID, Tool: x.engine.scrub.Text(call.Name), Permission: trace.PermissionNone, ErrorKind: trace.ErrorCancelled}
	}
	defer func() { x.toolCalls = append(x.toolCalls, traced...) }()
	// A step with return_result runs only its first return_result: once the
	// result is handed back nothing else may act, and a second return_result
	// in the same step must not use up the first one's repair.
	returnAt := -1
	if _, offered := x.catalog[subagent.ReturnToolName]; offered {
		returnAt = slices.IndexFunc(calls, func(c llmtypes.ToolUseBlock) bool { return c.Name == subagent.ReturnToolName })
	}
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
		if returnAt >= 0 && i != returnAt {
			// Not run, like a call a cancelled turn never reached.
			results[i].Content = "ignored: this step calls return_result, which must be the only call in its step; this call did not run"
			continue
		}
		traced[i].ErrorKind = trace.ErrorInvalidArgs
		if err != nil {
			results[i].Content = "invalid tool arguments"
			continue
		}
		t, ok := x.catalog[call.Name]
		if !ok {
			traced[i].ErrorKind = trace.ErrorUnknownTool
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

		// return_result only hands the result to the engine, so no policy is
		// asked: nothing may stop a child from ending its subtask.
		if t.Name != subagent.ReturnToolName {
			d, asked := x.authorize(ctx, t, call, raw)
			traced[i].Permission = tracePermission(d, asked)
			if !d.Allow {
				traced[i].ErrorKind = trace.ErrorDenied
				results[i].Content = "permission denied: " + d.Reason
				continue
			}
		}
		if _, loop := x.loop.Record(loopdetect.Signal{SessionID: x.info.ID, TurnID: x.run.id, ToolName: call.Name, Args: raw, Timestamp: time.Now()}); loop {
			traced[i].ErrorKind = trace.ErrorLoop
			results[i].Content = "repeated identical tool call; try another approach"
			continue
		}
		// A ready call stays "cancelled" until it runs, and "panic" while it
		// runs, so a call that never finishes is not recorded as a success.
		traced[i].ErrorKind = trace.ErrorCancelled
		// Consecutive read-only calls run side by side, and so do subtask
		// calls: subtask is not read-only (its child may write), but each
		// child runs in its own conversation, its approvals reach the owner
		// one at a time, and the agent-wide subtask limits bound how many run.
		// Every other call runs alone, in model order (runSegments).
		jobs[i] = tooluse.Job{Ready: true, Concurrent: t.ReadOnly || t.Name == subagent.ToolName}
	}
	runSegments(jobs, func(seg []tooluse.Job) { x.batch(ctx, seg, calls, args, results, traced) })
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

// runSegments runs ready jobs in model order: a run of consecutive concurrent
// jobs runs side by side, and a serial job runs alone, in its place.
func runSegments(jobs []tooluse.Job, run func([]tooluse.Job)) {
	for start := 0; start < len(jobs); {
		if !jobs[start].Ready {
			start++
			continue
		}
		end := start + 1
		if jobs[start].Concurrent {
			for end < len(jobs) && (!jobs[end].Ready || jobs[end].Concurrent) {
				end++
			}
		}
		seg := make([]tooluse.Job, len(jobs))
		copy(seg[start:end], jobs[start:end])
		run(seg)
		start = end
	}
}

func (x *execution) batch(ctx context.Context, jobs []tooluse.Job, calls []llmtypes.ToolUseBlock, args []json.RawMessage, results []llmtypes.ContentBlock, traced []trace.ToolCall) {
	tooluse.Batch(ctx, jobs, x.definition.Runtime.ToolConcurrency, func(i int, _ bool) {
		call := calls[i]
		if ctx.Err() != nil {
			traced[i].ErrorKind = trace.ErrorCancelled
			results[i].Content = "cancelled"
			return
		}
		started := time.Now()
		traced[i].ErrorKind = trace.ErrorPanic
		var kind trace.ErrorKind
		defer func() { traced[i].DurationMS = time.Since(started).Milliseconds() }()
		toolCtx, endSpan := events.ToolSpan(ctx, x.engine.scrub.Text(call.Name))
		defer endSpan()

		var result tools.Result
		// A panicking tool yields an error result carrying the panic ref.
		err := x.engine.panics.Call("tool "+call.Name, func() (err error) {
			result, err = x.catalog[call.Name].Handler(x.askContext(context.WithValue(toolCtx, callIDKey{}, call.ID), call.Name), tools.Call{ID: call.ID, Name: call.Name, Input: args[i]})
			return err
		}, panics.With("conversation", x.info.ID), panics.With("call", call.ID))
		if err != nil {
			result = tools.Result{Text: err.Error(), IsError: true}
		}
		switch _, panicked := panics.Ref(err); {
		case panicked:
			kind = trace.ErrorPanic
		case result.IsError:
			kind = trace.ErrorTool
		}
		result.Text = x.engine.scrub.Text(result.Text)
		view, err := x.engine.cache.Present(ctx, x.info.ID, toolresult.Meta{Tool: call.Name, CallID: call.ID, IsError: result.IsError}, result.Text, 0)
		if err == nil {
			result.Text = view.Content
		}
		if ctx.Err() != nil {
			kind = trace.ErrorCancelled
			result = tools.Result{Text: "cancelled", IsError: true}
		}
		results[i].Content, results[i].IsError = result.Text, result.IsError
		x.run.emit(ctx, func(meta events.EventMeta) events.Event {
			return events.ToolCallFinished{EventMeta: meta, CallID: call.ID, Result: result, IsError: result.IsError, Duration: time.Since(started)}
		})
		traced[i].ErrorKind = kind
	}, func(i int) { results[i].Content, traced[i].ErrorKind = "cancelled", trace.ErrorCancelled }, nil)
}

// tracePermission maps a permission decision to the trace vocabulary.
func tracePermission(d permission.PermissionDecision, asked bool) trace.Permission {
	switch {
	case !d.Allow:
		return trace.PermissionDeny
	case asked:
		return trace.PermissionApproved
	}
	return trace.PermissionAllow
}

func (x *execution) cancelledTools(calls []llmtypes.ToolUseBlock) error {
	blocks := make([]llmtypes.ContentBlock, len(calls))
	for i, call := range calls {
		blocks[i] = llmtypes.ContentBlock{Type: "tool_result", ToolUseID: call.ID, Content: "turn interrupted before execution", IsError: true}
	}
	return x.message("tool", blocks)
}

// ask asks the root conversation's own question, one ask at a time.
func (x *execution) ask(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	release, err := x.engine.askTurn(ctx, x.root.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	return x.run.ask(ctx, x.engine.scrub.Text(prompt), schema, x.engine.scrub.Bytes(schema), nil)
}

// callIDKey carries the tool call ID into its handler's context.
type callIDKey struct{}

func callIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(callIDKey{}).(string)
	return id
}

// askContext gives an MCP tool call the conversation's way to answer an
// elicitation: a root chat asks on its own run; a subtask under a root chat
// relays the question to the root, attributed to the subtask.
func (x *execution) askContext(ctx context.Context, tool string) context.Context {
	if !strings.HasPrefix(tool, "mcp__") {
		return ctx
	}
	var ask mcpclient.Ask
	if x.root.ParentID == "" && x.root.Role == "chat" {
		ask = x.ask
		if x.origin != nil {
			ask = x.relayQuestion
		}
	}
	return mcpclient.WithAsk(ctx, ask)
}
