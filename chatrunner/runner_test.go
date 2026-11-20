package chatrunner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/chat/chattest"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// brain is an offline model. The user's text is its script: "call:a,b;call:c"
// calls tools a and b in the first model step and c in the second, then says
// "done"; "long" never finishes; "overflow" fails once with a context
// overflow; anything else is answered "ok".
type brain struct {
	mu          sync.Mutex
	requests    []llmtypes.ChatRequest
	overflowed  bool
	failSummary bool
	// noticeBackground, when set, is a goal a notice turn starts as a
	// background subtask.
	noticeBackground string
	// plan is what the front desk's planner prompt gets back.
	plan string
	// planned records the planner requests.
	planned []llmtypes.ChatRequest
	// hang makes a "slow:" subtask stall until it is cancelled.
	hang bool
}

func isToolResult(m llmtypes.ChatMessage) bool {
	return slices.ContainsFunc(m.ContentBlocks, func(b llmtypes.ContentBlock) bool { return b.Type == "tool_result" })
}

func messageText(m llmtypes.ChatMessage) string {
	text := m.Content
	for _, b := range m.ContentBlocks {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return text
}

func (b *brain) respond(req llmtypes.ChatRequest) fake.Response {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requests = append(b.requests, req)
	script, steps := "", 0
	for _, m := range req.Messages {
		switch {
		case m.Role == "user" && !isToolResult(m):
			script, steps = messageText(m), 0
		case m.Role == "assistant":
			steps++
		}
	}
	if goal, ok := strings.CutPrefix(script, "Goal: "); ok {
		// A subtask's first message: its goal is the child's script.
		script, _, _ = strings.Cut(goal, "\n")
	}
	switch {
	case script == agent.NoticeTurnText && b.noticeBackground != "":
		if steps > 0 {
			return fake.Text("done")
		}
		return fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call-bg", Name: "subtask",
			Input: map[string]any{"variant": "worker", "goal": b.noticeBackground, "return_spec": "a line", "background": true}}}, {Type: llmtypes.EventDone}}}
	case strings.HasPrefix(script, "report:"):
		// "report:<text>" is a subtask that returns <text> as its result.
		if steps > 0 {
			return fake.Text("done")
		}
		return fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "ret-1", Name: "return_result",
			Input: map[string]any{"status": "ok", "result": strings.TrimPrefix(script, "report:")}}}, {Type: llmtypes.EventDone}}}
	case strings.HasPrefix(script, "slow:"):
		// "slow:<text>" stalls while hang is set, else reports <text>.
		if b.hang {
			return fake.Response{Stall: true, Events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "working"}}}
		}
		if steps > 0 {
			return fake.Text("done")
		}
		return fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "ret-1", Name: "return_result",
			Input: map[string]any{"status": "ok", "result": strings.TrimPrefix(script, "slow:")}}}, {Type: llmtypes.EventDone}}}
	case strings.HasPrefix(script, "subtask:"):
		// "subtask:<variant>:<goal>" starts one subtask, then says "done".
		if steps > 0 {
			return fake.Text("done")
		}
		variant, goal, _ := strings.Cut(strings.TrimPrefix(script, "subtask:"), ":")
		return fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call-sub", Name: "subtask",
			Input: map[string]any{"variant": variant, "goal": goal, "return_spec": "a line"}}}, {Type: llmtypes.EventDone}}}
	case strings.HasPrefix(script, "resumable:"):
		// "resumable:<text>" stalls while hang is set (so the process can be
		// killed mid-turn), else answers <text>.
		if b.hang {
			return fake.Response{Stall: true, Events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "working"}}}
		}
		return fake.Text(strings.TrimPrefix(script, "resumable:"))
	case script == "long":
		return fake.Response{Stall: true, Events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "working"}}}
	case script == "overflow" && !b.overflowed:
		b.overflowed = true
		return fake.Response{Err: &failure.Error{Class: failure.Overflow, Cause: errors.New("context too long")}}
	case strings.HasPrefix(script, "call:"):
		plan := strings.Split(script, ";")
		if steps >= len(plan) {
			return fake.Text("done")
		}
		var evs []llmtypes.StreamEvent
		for i, name := range strings.Split(strings.TrimPrefix(plan[steps], "call:"), ",") {
			evs = append(evs, llmtypes.StreamEvent{Type: llmtypes.EventToolUse,
				ToolUse: &llmtypes.ToolUseBlock{ID: fmt.Sprintf("call-%d-%d", steps, i), Name: name, Input: map[string]any{"note": "n"}}})
		}
		return fake.Response{Events: append(evs, llmtypes.StreamEvent{Type: llmtypes.EventDone})}
	}
	return fake.Text("ok")
}

func (b *brain) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	return fake.New(b.respond(req)).StreamChat(ctx, req)
}

func (b *brain) Complete(_ context.Context, req llmtypes.ChatRequest) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.Contains(req.SystemPrompt, "You are the front desk") {
		b.planned = append(b.planned, req)
		return b.plan, nil
	}
	if b.failSummary {
		return "", errors.New("summary unavailable")
	}
	return "a summary of the earlier conversation", nil
}

func (*brain) Capabilities() llmtypes.ProviderCapabilities { return llmtypes.ProviderCapabilities{} }

// saw reports whether any request the model was sent contains text.
func (b *brain) saw(text string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, req := range b.requests {
		all := req.EffectiveSystemPrompt()
		for _, m := range req.Messages {
			all += "\n" + messageText(m)
		}
		if strings.Contains(all, text) {
			return true
		}
	}
	return false
}

// last is everything the model was sent in its most recent request.
func (b *brain) request() llmtypes.ChatRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests[len(b.requests)-1]
}

func (b *brain) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	req := b.requests[len(b.requests)-1]
	text := req.EffectiveSystemPrompt()
	for _, m := range req.Messages {
		text += "\n" + messageText(m)
	}
	return text
}

type host struct {
	runner *chatrunner.Runner
	brain  *brain
	calls  sync.Map // tool name -> *atomic.Int32
	askFor atomic.Value
	grant  atomic.Value
}

func (h *host) ran(tool string) int32 {
	n, _ := h.calls.LoadOrStore(tool, new(atomic.Int32))
	return n.(*atomic.Int32).Load()
}

const schema = `{"type":"object","properties":{"note":{"type":"string"}}}`

func newHost(t *testing.T, builtins ...agent.BuiltinGroup) *host {
	t.Helper()
	dir := t.TempDir()
	definition := "---\nschema_version: \"2\"\ndefinition_id: def:chat-test\nrevision: \"1\"\nname: chat-test\ndescription: Chat test.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\nTest instructions.\n"
	profile := "kind: embedded\nlifetime: application\napplication_id: chat-test\nruntime:\n  provider: anthropic\n  model: test-model\n"
	for name, text := range map[string]string{"agent.md": definition, "host.yaml": profile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := &host{brain: &brain{}}
	h.askFor.Store("")
	h.grant.Store("")
	tool := func(name string, level chatrunner.Level) chatrunner.Tool {
		return chatrunner.Tool{Name: name, Description: "Test tool " + name, InputSchema: json.RawMessage(schema), Level: level,
			Run: func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
				if turn, ok := chatrunner.TurnFromContext(ctx); !ok || turn.ConversationID == "" {
					return agent.ToolResult{}, errors.New("the tool was not given its turn")
				}
				n, _ := h.calls.LoadOrStore(name, new(atomic.Int32))
				n.(*atomic.Int32).Add(1)
				return agent.ToolResult{Text: name + " ran"}, nil
			}}
	}
	r, err := chatrunner.Open(t.Context(), chatrunner.Options{
		Agent: agent.Options{Dir: dir, Provider: h.brain, GlobalSkillsDir: "-", Builtins: builtins},
		Tools: []chatrunner.Tool{
			tool("read_note", chatrunner.LevelRead), tool("ping", chatrunner.LevelAct),
			tool("write_note", chatrunner.LevelWrite), tool("wipe", chatrunner.LevelConfirm), tool("unmarked", ""),
		},
		Ask: func(_ context.Context, c chatrunner.Call) bool {
			return c.Tool == h.askFor.Load() && c.ConversationID != ""
		},
		Granted: func(_ context.Context, c chatrunner.Call) bool { return c.Tool == h.grant.Load() },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h.runner = r
	return h
}

// play runs one turn to its end, answering each approval with answer.
func (h *host) play(t *testing.T, turn chat.Turn, answer chat.Decision) []chatstream.Event {
	t.Helper()
	if turn.ConversationID == "" {
		turn.ConversationID = "conv_1"
	}
	turn.RunID = "run"
	run, err := h.runner.Start(t.Context(), turn)
	if err != nil {
		t.Fatalf("Start(%q): %v", turn.Text, err)
	}
	var evs []chatstream.Event
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
		if ev.Verb == chatstream.VerbApprovalRequest {
			if err := run.Answer(t.Context(), ev.ApprovalID, answer); err != nil {
				t.Errorf("Answer: %v", err)
			}
		}
	}
	chattest.CheckRun(t, evs)
	if last := evs[len(evs)-1]; last.Verb != chatstream.VerbRunFinish {
		t.Fatalf("turn %q ended with %s %s %s", turn.Text, last.Verb, last.Code, last.Message)
	}
	return evs
}

func verbs(evs []chatstream.Event, verb chatstream.Verb) []chatstream.Event {
	var out []chatstream.Event
	for _, ev := range evs {
		if ev.Verb == verb {
			out = append(out, ev)
		}
	}
	return out
}

func standing(t *testing.T, ev chatstream.Event) bool {
	t.Helper()
	var d struct {
		Description string `json:"description"`
		Standing    *bool  `json:"standing"`
		Tool        string `json:"tool"`
	}
	if err := json.Unmarshal(ev.Descriptor, &d); err != nil || d.Standing == nil || d.Description == "" || d.Tool == "" {
		t.Fatalf("approval descriptor %s: %v", ev.Descriptor, err)
	}
	return *d.Standing
}

func TestRunnerConformance(t *testing.T) {
	chattest.RunnerConformance(t, chattest.RunnerHarness{
		New:          func(t *testing.T) chat.Runner { return newHost(t).runner },
		LongText:     "long",
		ApprovalText: "call:write_note",
	})
}

func TestEventMapping(t *testing.T) {
	h := newHost(t)
	evs := h.play(t, chat.Turn{Text: "call:read_note"}, chat.Decision{})
	var got []string
	for _, ev := range evs {
		s := string(ev.Verb)
		if ev.Verb == chatstream.VerbPartStart || ev.Verb == chatstream.VerbActivity {
			s += ":" + ev.Kind
		}
		got = append(got, s)
	}
	want := []string{
		"run.start", "activity:nanite.slots",
		"step.start", "message.start", "part.start:tool_call", "part.end", "part.start:tool_result", "part.end", "message.end", "step.finish",
		"step.start", "message.start", "part.start:text", "part.delta", "part.end", "message.end", "step.finish",
		"run.finish",
	}
	// Usage reports come where the provider sends them; the offline one sends none.
	got = slices.DeleteFunc(got, func(s string) bool { return s == "usage" })
	if !slices.Equal(got, want) {
		t.Fatalf("events:\n got %v\nwant %v", got, want)
	}
	if evs[0].Provider != "anthropic" || evs[0].Model != "test-model" {
		t.Errorf("run.start provider %q model %q", evs[0].Provider, evs[0].Model)
	}
	fin := evs[len(evs)-1]
	if fin.Reason != string(chatstream.FinishStop) || fin.Usage == nil {
		t.Errorf("run.finish reason %q usage %v", fin.Reason, fin.Usage)
	}
	for _, ev := range evs {
		if ev.Verb == chatstream.VerbPartEnd && strings.HasSuffix(ev.PartID, "#result") {
			var final struct {
				Text    string `json:"text"`
				IsError bool   `json:"is_error"`
			}
			if err := json.Unmarshal(ev.Final, &final); err != nil || final.Text != "read_note ran" || final.IsError {
				t.Errorf("tool_result final %s", ev.Final)
			}
		}
	}
	info, err := h.runner.Info(t.Context(), "conv_1")
	if err != nil || !info.Deltas || !info.Approvals || info.RunnerRef != chatrunner.ConversationID("conv_1") {
		t.Errorf("Info = %+v, %v", info, err)
	}
	if list, err := h.runner.Agent().Conversations(t.Context()); err != nil || len(list) != 1 || list[0].ID != info.RunnerRef {
		t.Errorf("conversations = %+v, %v; want only %s", list, err, info.RunnerRef)
	}
}

func TestToolLevels(t *testing.T) {
	h := newHost(t)
	once := chat.Decision{Allow: true, Scope: chat.ScopeOnce}
	forConversation := chat.Decision{Allow: true, Scope: chat.ScopeConversation}
	asks := func(text string, d chat.Decision) []chatstream.Event {
		t.Helper()
		return verbs(h.play(t, chat.Turn{Text: text}, d), chatstream.VerbApprovalRequest)
	}

	// read and act run without asking.
	if got := asks("call:read_note,ping", once); len(got) != 0 || h.ran("read_note") != 1 || h.ran("ping") != 1 {
		t.Fatalf("read and act: %d approvals, read ran %d, act ran %d", len(got), h.ran("read_note"), h.ran("ping"))
	}

	// write asks, runs after allow, and an answer for the conversation stands.
	got := asks("call:write_note", forConversation)
	if len(got) != 1 || !standing(t, got[0]) || h.ran("write_note") != 1 {
		t.Fatalf("write: %d approvals, ran %d", len(got), h.ran("write_note"))
	}
	if got[0].CallID == "" || got[0].ApprovalID == "" || got[0].Mode != chatstream.ApprovalInBand {
		t.Errorf("approval.request = %+v", got[0])
	}
	if again := asks("call:write_note", once); len(again) != 0 || h.ran("write_note") != 2 {
		t.Fatalf("write after a conversation answer: %d approvals, ran %d", len(got), h.ran("write_note"))
	}

	// A denied call does not run.
	if denied := asks("call:unmarked", chat.Decision{}); len(denied) != 1 || h.ran("unmarked") != 0 {
		t.Fatalf("a tool with no level: %d approvals, ran %d; want it treated as write and denied", len(got), h.ran("unmarked"))
	}

	// confirm asks every time, even after an allow for the conversation.
	got = asks("call:wipe;call:wipe", forConversation)
	if len(got) != 2 || standing(t, got[0]) || h.ran("wipe") != 2 {
		t.Fatalf("confirm: %d approvals, standing %t, ran %d", len(got), len(got) > 0 && standing(t, got[0]), h.ran("wipe"))
	}

	// The host can make one call ask, whatever the tool's level.
	h.askFor.Store("read_note")
	if got := asks("call:read_note", once); len(got) != 1 || h.ran("read_note") != 2 {
		t.Fatalf("a read tool the host marks ask-first: %d approvals, ran %d", len(got), h.ran("read_note"))
	}
	h.askFor.Store("")

	// A standing grant the host holds covers a write call in a new
	// conversation, and never a confirm tool.
	h.grant.Store("write_note")
	evs := h.play(t, chat.Turn{ConversationID: "conv_2", Text: "call:write_note"}, once)
	if n := len(verbs(evs, chatstream.VerbApprovalRequest)); n != 0 || h.ran("write_note") != 3 {
		t.Fatalf("a granted write: %d approvals, ran %d", n, h.ran("write_note"))
	}
	h.grant.Store("wipe")
	evs = h.play(t, chat.Turn{ConversationID: "conv_2", Text: "call:wipe"}, once)
	if n := len(verbs(evs, chatstream.VerbApprovalRequest)); n != 1 {
		t.Fatalf("a confirm tool with a grant on file asked %d times, want 1", n)
	}
}

func TestSetToolsReplacesTheGroup(t *testing.T) {
	h := newHost(t)
	if err := h.runner.SetTools(); err != nil {
		t.Fatal(err)
	}
	evs := h.play(t, chat.Turn{Text: "call:read_note"}, chat.Decision{})
	if h.ran("read_note") != 0 {
		t.Fatal("a removed tool ran")
	}
	for _, ev := range evs {
		if ev.Verb == chatstream.VerbPartEnd && strings.HasSuffix(ev.PartID, "#result") && !strings.Contains(string(ev.Final), "unavailable") {
			t.Errorf("result of a removed tool: %s", ev.Final)
		}
	}
}

func TestTurnContextTravelsWithItsMessage(t *testing.T) {
	h := newHost(t)
	h.play(t, chat.Turn{Text: "first", Context: json.RawMessage(`"DESK-MARKER on the desk"`)}, chat.Decision{})
	first := h.brain.request()
	if !strings.Contains(h.brain.last(), "DESK-MARKER on the desk") {
		t.Fatal("the turn's context is not in that turn's request")
	}
	if strings.Contains(first.EffectiveSystemPrompt(), "DESK-MARKER") {
		t.Fatal("the turn's context is in the system prompt, which breaks the cache")
	}
	h.play(t, chat.Turn{Text: "second"}, chat.Decision{})
	second := h.brain.request()
	// The first turn's context stays with the first message, unchanged, and
	// is not repeated as the second turn's context.
	if first.EffectiveSystemPrompt() != second.EffectiveSystemPrompt() {
		t.Fatal("the system prompt changed between turns")
	}
	if messageText(first.Messages[0]) != messageText(second.Messages[0]) {
		t.Fatalf("the first message changed between turns:\n%q\n%q", messageText(first.Messages[0]), messageText(second.Messages[0]))
	}
	if newest := second.Messages[len(second.Messages)-1]; strings.Contains(messageText(newest), "DESK-MARKER") {
		t.Fatal("the first turn's context was repeated in the second turn's message")
	}
	h.play(t, chat.Turn{Text: "third", Context: json.RawMessage(`{"open":["OBJECT-MARKER"]}`)}, chat.Decision{})
	if !strings.Contains(h.brain.last(), `{"open":["OBJECT-MARKER"]}`) {
		t.Fatal("a context that is not a string was not passed as JSON")
	}
}

func TestRecordedExchangeReachesTheModel(t *testing.T) {
	h := newHost(t)
	turn := chat.Turn{ConversationID: "conv_1", Text: "what is on the desk"}
	if err := h.runner.Record(t.Context(), turn, "HOST-ANSWER three notes"); err != nil {
		t.Fatal(err)
	}
	h.play(t, chat.Turn{Text: "and then"}, chat.Decision{})
	if got := h.brain.last(); !strings.Contains(got, "what is on the desk") || !strings.Contains(got, "HOST-ANSWER three notes") {
		t.Fatalf("the recorded exchange is not in the next request:\n%s", got)
	}
}

func TestCancelEndsWithRunAbortOnly(t *testing.T) {
	h := newHost(t)
	run, err := h.runner.Start(t.Context(), chat.Turn{ConversationID: "conv_1", RunID: "run", Text: "long"})
	if err != nil {
		t.Fatal(err)
	}
	var terminals []chatstream.Verb
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Verb == chatstream.VerbPartDelta {
			run.Cancel()
		}
		if ev.IsTerminal() {
			terminals = append(terminals, ev.Verb)
		}
	}
	if len(terminals) != 1 || terminals[0] != chatstream.VerbRunAbort {
		t.Fatalf("terminal events %v, want one run.abort", terminals)
	}
	// The conversation takes the next turn.
	h.play(t, chat.Turn{Text: "next"}, chat.Decision{})
}

func compactStates(evs []chatstream.Event) []string {
	var states []string
	for _, ev := range evs {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityCompact {
			var v struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal(ev.Value, &v)
			states = append(states, v.State)
		}
	}
	return states
}

func TestCompactionIsReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail bool
		want []string
	}{{"done", false, []string{"start", "done"}}, {"failed", true, []string{"start", "failed"}}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHost(t)
			h.brain.failSummary = tc.fail
			for i := range 3 {
				turn := chat.Turn{ConversationID: "conv_1", Text: fmt.Sprintf("question %d %s", i, strings.Repeat("older words ", 100))}
				if err := h.runner.Record(t.Context(), turn, strings.Repeat("older answer ", 100)); err != nil {
					t.Fatal(err)
				}
			}
			run, err := h.runner.Start(t.Context(), chat.Turn{ConversationID: "conv_1", RunID: "run", Text: "overflow"})
			if err != nil {
				t.Fatal(err)
			}
			var evs []chatstream.Event
			for ev, err := range run.Events() {
				if err != nil {
					t.Fatal(err)
				}
				evs = append(evs, ev)
			}
			chattest.CheckRun(t, evs)
			if got := compactStates(evs); !slices.Equal(got, tc.want) {
				t.Fatalf("nanite.compact states %v, want %v", got, tc.want)
			}
		})
	}
}
