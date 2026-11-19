package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestTurnReloadAndPolicy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for name, raw := range map[string]string{
		"agent.md": `---
schema_version: "2"
definition_id: def:sample-stable
revision: "1"
name: sample-stable
description: A stable example operative.
behavior:
  purpose: Reply to an example chat turn.
requirements: {}
harness_profile:
  permissions:
    profile: example
continuity:
  mode: durable
---
Reply clearly and briefly.
`,
		"host.yaml": `kind: stable
lifetime: on-demand
seat_id: sample-stable

runtime:
  provider: anthropic
  max_steps: 50
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tool := func(id string) fake.Response {
		return fake.Response{Events: []llmtypes.StreamEvent{
			{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: id, Name: "host_check"}},
			{Type: llmtypes.EventDone},
		}}
	}
	a, err := agent.Open(ctx, agent.Options{Dir: root, GlobalSkillsDir: "-", Provider: fake.New(tool("call-1"), fake.Text("Allowed"), tool("call-2"), fake.Text("Denied"))})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var calls atomic.Int32
	if err := a.Tools().SetGroup("fixture", agent.Tool{
		Name: "host_check", InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			calls.Add(1)
			return agent.ToolResult{Text: "checked"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	c, err := a.NewConversation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	send := func(want string) {
		t.Helper()
		run, err := c.Send(ctx, agent.Input{Text: "check"})
		if err != nil {
			t.Fatal(err)
		}
		var last uint64
		var finish bool
		var permission bool
		for event := range run.Events() {
			switch e := event.(type) {
			case agent.RunStarted:
				last = e.Seq
			case agent.PermissionResolved:
				permission = true
			case agent.RunFinished:
				finish = e.Seq > last
			}
		}
		got, err := run.Wait()
		if err != nil || got.Text != want || !finish || !permission {
			t.Fatalf("got %+v, %v, terminal=%v, permission=%v", got, err, finish, permission)
		}
	}
	send("Allowed")
	if err := os.WriteFile(filepath.Join(root, "permissions.yaml"), []byte("permissions:\n  rules:\n    - tool: host_check\n      behavior: deny\n"), 0600); err != nil {
		t.Fatal(err)
	}
	send("Denied")
	if calls.Load() != 1 {
		t.Fatalf("handler called %d times", calls.Load())
	}
	if err := a.RebuildIndex(ctx); err != nil {
		t.Fatal(err)
	}
	history, err := c.History(ctx)
	if err != nil || len(history) != 8 || !history[6].Blocks[0].IsError {
		t.Fatalf("history %+v, %v", history, err)
	}
	if _, err := a.Conversation(ctx, "missing"); !errors.Is(err, agent.ErrNoConversation) {
		t.Fatalf("missing: %v", err)
	}
}
