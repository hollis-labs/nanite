package agent

import (
	"os"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/toolregistry"
)

func TestToolRegistryEmbeddingConfiguration(t *testing.T) {
	source := t.TempDir()
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll("tools/echo", 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("tools/echo/tool.yaml", []byte("description: Echo\nversion: 1.0.0\nkind: command\nentry: run.sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("tools/echo/run.sh", []byte("#!/bin/sh\ncat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	client := toolregistry.LocalClient(t.TempDir())
	record, err := toolregistry.Publish(t.Context(), client, source, "echo", "msg://agent/agent-mux/agt_fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		dir := agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: registry-test\n")
		p := fake.New(fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "install", Name: "tool_install", Input: map[string]any{"ref": record.Ref()}}}, {Type: llmtypes.EventDone}}}, fake.Text("done"))
		opts := Options{Dir: dir, GlobalSkillsDir: "-", Provider: p, Gate: AllowGate, Builtins: []BuiltinGroup{BuiltinTools}}
		if enabled {
			opts.ToolRegistry = &client
		}
		a, err := Open(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		c, err := a.NewConversation(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		run, err := c.Send(t.Context(), Input{Text: "install"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		_, err = authored.LoadTool(dir, "echo")
		if enabled && err != nil {
			t.Fatalf("configured registry not forwarded: %v", err)
		}
		if !enabled && err == nil {
			t.Fatal("registry builtin available without client")
		}
	}
}
