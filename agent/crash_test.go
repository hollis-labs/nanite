package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestResumeAfterSIGKILLMidTool(t *testing.T) {
	const env = "NANITE_CRASH_FIXTURE_DIR"
	if dir := os.Getenv(env); dir != "" {
		crashDuringTool(t, dir)
		return
	}
	dir := t.TempDir()
	files := map[string]string{
		"agent.md": `---
schema_version: "2"
definition_id: def:crash-test
revision: "1"
name: crash-test
description: Crash fixture.
behavior:
  purpose: Test crash recovery.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Recovery test.
`,
		"host.yaml": "kind: embedded\nlifetime: application\napplication_id: crash-test\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResumeAfterSIGKILLMidTool$") // #nosec G204 G702 -- Runs the current test binary with fixed test flags.
	cmd.Env = append(os.Environ(), env+"="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	marker := filepath.Join(dir, "tool-active")
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("child never entered tool handler")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exited *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exited) {
		t.Fatalf("kill wait: %v", err)
	}

	a, err := agent.Open(ctx, agent.Options{Dir: dir, Provider: fake.New(fake.Text("recovered")), GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := a.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := c.Interrupted()
	if interrupted == nil {
		t.Fatal("reopen did not mark turn interrupted")
	}
	if _, err := c.Send(ctx, agent.Input{Text: "new turn"}); err == nil {
		t.Fatal("sent with an interrupted turn")
	}
	run, err := c.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	if err != nil || result.Text != "recovered" || run.ID() != interrupted.ID {
		t.Fatalf("resume result %+v, %v", result, err)
	}
	history, err := c.History(ctx)
	if err != nil || len(history) != 4 {
		t.Fatalf("history %+v, %v", history, err)
	}
	call, reply := history[1].Blocks[0], history[2].Blocks[0]
	if call.Type != "tool_use" || reply.Type != "tool_result" || reply.ToolUseID != call.ID ||
		!reply.IsError || !strings.Contains(reply.Content, "not retried") || history[3].Blocks[0].Text != "recovered" {
		t.Fatalf("unpaired history after crash: %+v", history)
	}
	if c.Interrupted() != nil {
		t.Fatal("resumed turn is still interrupted")
	}
}

func crashDuringTool(t *testing.T, dir string) {
	t.Helper()
	response := fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call-crash", Name: "blocking_tool"}},
		{Type: llmtypes.EventDone},
	}}
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(response), GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Tools().SetGroup("fixture", agent.Tool{Name: "blocking_tool", InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ agent.ToolCall) (agent.ToolResult, error) {
			if err := os.WriteFile(filepath.Join(dir, "tool-active"), nil, 0600); err != nil { // #nosec G703 -- Reads a transcript in the test temporary directory.
				return agent.ToolResult{}, err
			}
			<-ctx.Done()
			return agent.ToolResult{}, ctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), agent.Input{Text: "trigger tool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
}
