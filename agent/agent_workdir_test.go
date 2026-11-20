package agent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestWorkDirMustBeAnAbsoluteDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, work := range []string{"work", filepath.Join(dir, "missing"), file} {
		_, err := agent.Open(t.Context(), agent.Options{Dir: t.TempDir(), WorkDir: work, GlobalSkillsDir: "-", Provider: fake.New(fake.Text("x"))})
		if err == nil || !strings.Contains(err.Error(), "WorkDir") {
			t.Errorf("WorkDir %q: %v", work, err)
		}
	}
}

// TestWorkDirIsWhereToolsWork: with WorkDir apart from the agent directory,
// a relative path resolves in WorkDir and bash runs there, while the
// process's working directory is somewhere else entirely.
func TestWorkDirIsWhereToolsWork(t *testing.T) {
	t.Chdir(t.TempDir())
	root := t.TempDir()
	writeAgent(t, root)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("from the work directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	call := func(id, name string, input map[string]any) fake.Response {
		return fake.Response{Events: []llmtypes.StreamEvent{
			{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: id, Name: name, Input: input}},
			{Type: llmtypes.EventDone},
		}}
	}
	a, err := agent.Open(t.Context(), agent.Options{Dir: root, WorkDir: work, GlobalSkillsDir: "-", Gate: agent.AllowGate, Builtins: agent.AllBuiltins(),
		Provider: fake.New(call("read", "read_file", map[string]any{"path": "note.txt"}), call("pwd", "bash", map[string]any{"command": "pwd -P"}), fake.Text("done"))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	conv, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := conv.Send(t.Context(), agent.Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for ev := range run.Events() {
		if f, ok := ev.(agent.ToolCallFinished); ok {
			got[f.CallID] = f.Result.Text
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got["read"], "from the work directory") {
		t.Errorf("read_file note.txt: %q", got["read"])
	}
	if strings.TrimSpace(got["pwd"]) != work {
		t.Errorf("bash pwd: %q, want %q", got["pwd"], work)
	}
}

func writeAgent(t *testing.T, root string) {
	t.Helper()
	for name, raw := range map[string]string{
		"agent.md": "---\nschema_version: \"2\"\ndefinition_id: def:workdir\nrevision: \"1\"\nname: workdir\ndescription: WorkDir test agent.\nbehavior:\n  purpose: Use tools.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: example\ncontinuity:\n  mode: durable\n---\nUse tools.\n",
		"host.yaml": "kind: stable\nlifetime: on-demand\nseat_id: workdir\n\nruntime:\n  provider: anthropic\n  max_steps: 10\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
