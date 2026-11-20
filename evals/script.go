package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/slots"
)

// ScriptedModel is the model name an offline run reports.
const ScriptedModel = "scripted"

// Script is the offline run's model: the steps of the root conversation, and
// of each subtask conversation, keyed by the subtask's goal.
type Script struct {
	Root     []Step
	Children map[string][]Step
}

// Step is one model response: tool calls, or a final text. A step made with
// FetchIfStored decides from the request.
type Step struct {
	calls  []ToolUse
	text   string
	decide func(llmtypes.ChatRequest) Step
}

// ToolUse is one scripted tool call.
type ToolUse struct {
	Name  string
	Input map[string]any
}

// Calls is a step that calls the tools, all in one response.
func Calls(calls ...ToolUse) Step { return Step{calls: calls} }

// Say is a step that answers with text and ends the turn.
func Say(text string) Step { return Step{text: text} }

// Return is a subtask's last step: return_result with a status and result.
func Return(status, result string) Step {
	return Calls(Call("return_result", "status", status, "result", result))
}

var storedRef = regexp.MustCompile(`tool_result://([0-9A-Za-z]+)`)

// FetchIfStored reads the last tool result's stored body with
// fetch_tool_result when the result was too big to show in full (it carries
// a tool_result:// pointer); otherwise it is the step given.
func FetchIfStored(otherwise Step) Step {
	return Step{decide: func(req llmtypes.ChatRequest) Step {
		last := req.Messages[len(req.Messages)-1]
		for _, b := range last.ContentBlocks {
			if m := storedRef.FindStringSubmatch(b.Content); b.Type == "tool_result" && m != nil {
				return Calls(Call("fetch_tool_result", "id", m[1]))
			}
		}
		return otherwise
	}}
}

// Call builds a tool call from name and key, value pairs.
func Call(name string, kv ...any) ToolUse {
	in := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		in[kv[i].(string)] = kv[i+1]
	}
	return ToolUse{Name: name, Input: in}
}

// scripted serves a Script. It tells the conversations apart by their first
// message (a subtask's starts with "Goal: <goal>") and finds the step by how
// many answers the conversation already has, so concurrent subtasks get their
// own steps whatever order they run in.
//
// Its usage is an estimate from the request's size (about four characters a
// token), so the offline run checks the harness end to end; it is not
// evidence about any model.
type scripted struct {
	script Script
	// dirs are the run's directory: it is counted as one word, so the
	// estimate does not depend on where the run is.
	dirs []string
	mu   sync.Mutex
	errs []string
}

var _ llmcontracts.Provider = (*scripted)(nil)

func newScripted(s Script) *scripted { return &scripted{script: s} }

func (p *scripted) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, steps := "root", p.script.Root
	if goal := goalOf(req.Messages); goal != "" {
		key, steps = goal, p.script.Children[goal]
	}
	n := 0
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			n++
		}
	}
	if n >= len(steps) {
		msg := fmt.Sprintf("scripted model: no step %d for %q", n, key)
		p.mu.Lock()
		p.errs = append(p.errs, msg)
		p.mu.Unlock()
		return nil, fmt.Errorf("%s", msg)
	}
	step := steps[n]
	if step.decide != nil {
		step = step.decide(req)
	}
	var events []llmtypes.StreamEvent
	out := 0
	if step.text != "" {
		events = append(events, llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: step.text})
		out += len(step.text)
	}
	for i, c := range step.calls {
		raw, _ := json.Marshal(c.Input)
		out += len(raw) + len(c.Name)
		events = append(events, llmtypes.StreamEvent{Type: llmtypes.EventToolUse,
			ToolUse: &llmtypes.ToolUseBlock{ID: fmt.Sprintf("s%d_%d", n, i), Name: c.Name, Input: c.Input}})
	}
	stop := llmtypes.StopReasonEndTurn
	if len(step.calls) > 0 {
		stop = llmtypes.StopReasonToolUse
	}
	events = append(events,
		llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: requestTokens(req, p.dirs), OutputTokens: out/4 + 1, StopReason: stop}},
		llmtypes.StreamEvent{Type: llmtypes.EventDone})
	ch := make(chan llmtypes.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// Complete serves the engine's non-streaming requests (compaction summaries
// and argument repair); none of the scripts trigger them.
func (p *scripted) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "", fmt.Errorf("scripted model: Complete is not scripted")
}

func (p *scripted) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{SupportsToolCalling: true, MaxTokens: 8192, ContextWindowSize: 200000}
}

// failures are requests the script had no step for.
func (p *scripted) failures() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.errs...)
}

// goalOf returns a subtask conversation's goal, from its first user message.
func goalOf(msgs []llmtypes.ChatMessage) string {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		text := m.Content
		for _, b := range m.ContentBlocks {
			if b.Type == "text" && b.Name != slots.TurnContextBlock {
				text += b.Text
			}
		}
		i := strings.Index(text, "Goal: ")
		if i < 0 {
			return ""
		}
		goal := text[i+len("Goal: "):]
		if j := strings.IndexByte(goal, '\n'); j >= 0 {
			goal = goal[:j]
		}
		return strings.TrimSpace(goal)
	}
	return ""
}

// requestTokens estimates a request's input tokens from its size. The run's
// directory, the platform, IDs and numbers (times, dates) are counted as one
// fixed-size word, so the estimate is the same on every run and machine.
func requestTokens(req llmtypes.ChatRequest, dirs []string) int {
	var b strings.Builder
	b.WriteString(req.SystemPrompt)
	for _, s := range req.SlotBlocks {
		b.WriteString(s.Content)
	}
	msgs, _ := json.Marshal(req.Messages)
	tools, _ := json.Marshal(req.Tools)
	b.Write(msgs)
	b.Write(tools)
	text := b.String()
	for _, d := range dirs {
		if d != "" {
			text = strings.ReplaceAll(text, d, "/run")
		}
	}
	text = strings.ReplaceAll(text, runtime.GOOS+"/"+runtime.GOARCH, "os/arch")
	return len(volatile.ReplaceAllString(text, "#")) / 4
}

// volatile matches what differs between otherwise identical runs: ULIDs and
// other long IDs, and digit runs.
var volatile = regexp.MustCompile(`[0-9A-Za-z_-]*[0-9][0-9A-Za-z_-]*`)
