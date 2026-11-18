package chatrunner

import (
	"context"
	"encoding/json"
	"fmt"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/agent"
)

// Level says how much a host tool can change, and so whether it asks first.
type Level string

// The tool levels.
const (
	// LevelRead only reads. It runs without asking.
	LevelRead Level = "read"
	// LevelAct does something that is easy to undo. It runs without asking.
	LevelAct Level = "act"
	// LevelWrite changes something. It asks first; an answer for the
	// conversation, or a standing grant, covers later calls.
	LevelWrite Level = "write"
	// LevelConfirm asks every time. No answer covers a later call.
	LevelConfirm Level = "confirm"
)

// effective is the level the adapter applies: an empty or unknown level is
// write.
func (l Level) effective() Level {
	switch l {
	case LevelRead, LevelAct, LevelWrite, LevelConfirm:
		return l
	}
	return LevelWrite
}

// Tool is one tool the host offers the agent. It runs in the host's process
// when the model calls it and appears on the stream as an ordinary tool_call
// and tool_result.
type Tool struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema object.
	InputSchema json.RawMessage
	// Level decides whether a call asks first. Empty or unknown is LevelWrite.
	Level Level
	// Describe says, for a person, what this call will do. It is shown with
	// the approval request. Nil uses the tool's Description.
	Describe func(input json.RawMessage) string
	// Run executes an approved call. The context carries the turn; see
	// TurnFromContext.
	Run func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error)
}

// Call identifies one tool call for the host's Ask and Granted functions.
type Call struct {
	// ConversationID is the chat conversation's ID.
	ConversationID string
	Tool           string
	Input          json.RawMessage
}

const toolGroup = "chatrunner"

// SetTools replaces the host's tools. The change applies from the next turn;
// a turn in progress keeps the tools it started with.
func (r *Runner) SetTools(tools ...Tool) error {
	specs := make([]agent.Tool, 0, len(tools))
	levels := make(map[string]Tool, len(tools))
	for _, t := range tools {
		if t.Run == nil {
			return fmt.Errorf("chatrunner: tool %q has no Run function", t.Name)
		}
		level := t.Level.effective()
		specs = append(specs, agent.Tool{
			Name: t.Name, Description: t.Description, InputSchema: t.InputSchema,
			ReadOnly:    level == LevelRead,
			Destructive: level == LevelWrite || level == LevelConfirm,
			Handler:     t.Run,
		})
		levels[t.Name] = t
	}
	if err := r.agent.Tools().SetGroup(toolGroup, specs...); err != nil {
		return fmt.Errorf("chatrunner: %w", err)
	}
	r.mu.Lock()
	r.tools = levels
	r.mu.Unlock()
	return nil
}

func (r *Runner) tool(name string) (Tool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tools[name]
	return t, ok
}

// pdp makes a call ask where the host's levels say so and Nanite's own rules
// might not: a confirm tool in any mode, and any host-tool call the host's
// Ask function marks. It never loosens a decision. A host PDP, when there is
// one, is consulted first.
type pdp struct {
	runner *Runner
	host   policy.PDP
}

func (p pdp) Evaluate(ctx context.Context, req policy.Request) (policy.Decision, error) {
	decision := policy.Decision{Effect: policy.Allow}
	if p.host != nil {
		d, err := p.host.Evaluate(ctx, req)
		if err != nil || d.Effect == policy.Deny || len(d.Obligations) > 0 {
			return d, err
		}
		decision = d
	}
	t, ok := p.runner.tool(req.Resource.ID)
	if !ok {
		return decision, nil
	}
	if t.Level.effective() == LevelConfirm {
		return policy.Decision{Effect: policy.Ask, Reason: "this tool asks every time"}, nil
	}
	if ask := p.runner.opts.Ask; ask != nil {
		input, _ := json.Marshal(req.Context["input"])
		if ask(ctx, Call{ConversationID: p.runner.chatID(req.Scope), Tool: t.Name, Input: input}) {
			return policy.Decision{Effect: policy.Ask, Reason: "the application asks before this call"}, nil
		}
	}
	return decision, nil
}

// gate turns Nanite's request for approval into a wait for the answer the
// chat Service delivers through Run.Answer.
type gate struct{ runner *Runner }

func (g gate) Decide(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
	run := g.runner.activeRun(req.ConversationID)
	if run == nil {
		return agent.PermissionDecision{Reason: "no chat turn is waiting for this approval"}, nil
	}
	t, hosted := g.runner.tool(req.Tool)
	confirm := hosted && t.Level.effective() == LevelConfirm
	p := run.approval(req.ID)
	if granted := g.runner.opts.Granted; granted != nil && !confirm &&
		granted(ctx, Call{ConversationID: run.turn.ConversationID, Tool: req.Tool, Input: req.Input}) {
		p.settle(true)
		return agent.PermissionDecision{Allow: true, Reason: "standing grant"}, nil
	}
	p.settle(false)
	select {
	case d := <-p.answer:
		out := agent.PermissionDecision{Allow: d.Allow, Reason: d.Reason}
		if d.Allow && !confirm && (d.Scope == chat.ScopeConversation || d.Scope == chat.ScopeAlways) {
			// "conversation" and "always" both stand for the rest of this
			// conversation; the host's store keeps "always" beyond it.
			out.Scope = agent.ScopeConversation
		}
		if out.Reason == "" {
			out.Reason = "answered in the chat"
		}
		return out, nil
	case <-ctx.Done():
		return agent.PermissionDecision{Reason: "the turn ended before the approval was answered"}, nil
	}
}

// describe is the approval request's description for a person.
func (r *Runner) describe(tool string, input json.RawMessage) string {
	if t, ok := r.tool(tool); ok {
		if t.Describe != nil {
			if s := t.Describe(input); s != "" {
				return s
			}
		}
		if t.Description != "" {
			return t.Description
		}
	}
	return "Run the tool " + tool
}
