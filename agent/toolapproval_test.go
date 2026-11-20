package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestToolApprovalsUseExplicitHostDataDir(t *testing.T) {
	dir := agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: approval-test\n")
	pkgDir := filepath.Join(dir, "tools", "fixture")
	if err := os.MkdirAll(pkgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "tool.yaml"), []byte("description: d\nkind: mcp\nentry: run.sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil { //nolint:gosec // G306: the test package entry must be executable.
		t.Fatal(err)
	}
	data := t.TempDir()
	provider := fake.New(fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "approve", Name: "tool__fixture", Input: map[string]any{}}}, {Type: llmtypes.EventDone}}}, fake.Text("approved"))
	a, err := Open(t.Context(), Options{Dir: dir, DataDir: data, GlobalSkillsDir: "-", Provider: provider, Builtins: []BuiltinGroup{BuiltinTools}, Gate: AllowGate})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), Input{Text: "approve the package"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(data, "tool-approvals"))
	if err != nil || len(files) != 1 {
		t.Fatalf("host data approvals: %v %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".nanite", "tool-approvals")); !os.IsNotExist(err) {
		t.Fatalf("approval stored in agent directory: %v", err)
	}
}
