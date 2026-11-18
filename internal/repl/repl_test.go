package repl

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
)

type conversation struct {
	*engine.Engine
	id string
}

func (c conversation) Send(ctx context.Context, input engine.Input) (*engine.Run, error) {
	return c.Engine.Send(ctx, c.id, input)
}

func TestScriptedTerminal(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	files := map[string]string{
		"agent.md": `---
schema_version: "2"
definition_id: def:repl-fixture
revision: "1"
name: repl-fixture
description: Scripted terminal fixture.
behavior:
  purpose: Exercise the terminal.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Answer briefly.
`,
		"host.yaml": "kind: embedded\nlifetime: application\napplication_id: terminal\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(ctx, filepath.Join(dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	question := fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "question", Name: "ask_user", Input: map[string]any{"prompt": "Ready?", "schema": map[string]any{"type": "boolean"}}}},
		{Type: llmtypes.EventDone},
	}}
	core, err := engine.New(engine.Config{Dir: dir, GlobalSkillsDir: "-", Store: db, Provider: fake.New(fake.Text("First reply"), question, fake.Text("Second reply"), fake.Text("One-shot reply"), fake.Text("Stream reply")), Slots: slots.NewStore(), Tools: &tools.Groups{}, Skills: &skills.Groups{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(core.Close)
	fresh := func(ctx context.Context) (Sender, error) {
		now := time.Now().UTC()
		id := store.NewID(now)
		return conversation{core, id}, db.CreateConversation(ctx, store.Conversation{ID: id, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now})
	}
	c, err := fresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	terminal := NewTerminal(ctx, strings.NewReader("hello\n/slots\n/new\nagain\ninvalid\ntrue\n/quit\n"), &out)
	if err := terminal.Chat(ctx, fresh, c); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"First reply", `"universal"`, "[ask_user]", "Ready?", "parse answer", "Second reply"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q: %s", want, out.String())
		}
	}
	out.Reset()
	if err := One(ctx, c, "one-shot", "json", &out); err != nil {
		t.Fatal(err)
	}
	var result struct{ Text string }
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Text != "One-shot reply" {
		t.Fatalf("one-shot = %+v (%v)", result, err)
	}
	out.Reset()
	if err := One(ctx, c, "stream", "stream-json", &out); err != nil || !strings.Contains(out.String(), `"type":"RunFinished"`) {
		t.Fatalf("stream = %s (%v)", out.String(), err)
	}
	if err := One(ctx, c, "unused", "unsupported", &out); err == nil {
		t.Fatal("unsupported format succeeded")
	}
}

func TestTerminalPermissionAndResumePrompts(t *testing.T) {
	var out bytes.Buffer
	term := NewTerminal(t.Context(), strings.NewReader("always\nno\nyes"), &out)
	first, err := term.Decide(t.Context(), permission.PermissionRequest{Tool: "bash", Input: json.RawMessage(`{"command":"printf example"}`), Reason: "approval required"})
	if err != nil || !first.Allow || first.Scope != permission.ScopeConversation || !strings.Contains(out.String(), "printf example") {
		t.Fatalf("approval = %+v, %v, %s", first, err, out.String())
	}
	second, err := term.Decide(t.Context(), permission.PermissionRequest{Tool: "write_file"})
	if err != nil || second.Allow {
		t.Fatalf("denial = %+v, %v", second, err)
	}
	if ok, err := term.ConfirmResume(t.Context(), "fixture-turn"); !ok || err != nil {
		t.Fatalf("resume = %v, %v", ok, err)
	}
}
